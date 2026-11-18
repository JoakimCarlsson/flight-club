// Package virgin searches Virgin Atlantic reward seat availability, priced in
// Virgin Points (Flying Club).
//
// virginatlantic.com's live award search needs a signed-in Flying Club
// member: its GraphQL searchOffers query answers "Must be logged in to a
// valid account" without one. The public Reward Seat Checker
// (virginatlantic.com/reward-flight-finder) instead loads a month calendar
// from its own GraphQL endpoint, which answers a plain POST without a
// session, cookie or token. For each day of the month and each cabin it
// gives the lowest points price across every Virgin Atlantic flight that
// day, the lowest taxes in GBP, the seats left and whether the price is a
// Saver. It names no flights or times, so offers are Estimated, carry no
// segments, and are departing on the query's date at midnight UTC.
//
// The checker covers Virgin Atlantic's own routes between airports, e.g. LHR
// to JFK; city codes and other routes answer REWARD_SEAT_UNSUPPORTED_ROUTE,
// which is returned as no availability. One answer covers a whole month, so
// answers are cached for cacheTTL and a week of days costs one request.
package virgin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/joakimcarlsson/flight-club/internal/award"
)

// Key is the program key Virgin Atlantic offers carry.
const Key = "virgin"

// graphqlURL is the Reward Seat Checker's GraphQL endpoint.
const graphqlURL = "https://www.virginatlantic.com/reward-flight-finder/api/graphql"

// bookingBase is the flight search page a traveller books a reward seat
// from once signed in.
const bookingBase = "https://www.virginatlantic.com/flights/search/slice"

// userAgent is a current desktop Chrome, which the site answers like a
// browser.
const userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36"

// maxBody is the most of an answer that is read, well above the tens of
// kilobytes a month answers with.
const maxBody = 4 << 20

// cacheTTL is how long a month's calendar is reused for other days of it.
const cacheTTL = 10 * time.Minute

// unsupportedRoute is the error code the checker answers a route it does not
// cover with.
const unsupportedRoute = "REWARD_SEAT_UNSUPPORTED_ROUTE"

// calendarQuery asks for one route's reward seat calendar for one month.
const calendarQuery = `query RewardSeatAvailability(
  $request: RewardSeatAvailabilityRequestInput!
) {
  rewardSeatAvailability(request: $request) {
    month
    year
    originPrettyName
    destinationPrettyName
    pointsDays {
      date
      seats {
        awardEconomy { ...cabin }
        awardPremium { ...cabin }
        awardUpperClass { ...cabin }
      }
    }
  }
}
fragment cabin on RewardSeatCabin {
  cabinPointsValue
  price
  currency
  isSaverAward
  cabinClassSeatCount
  cabinClassSeatCountString
}`

// Program searches the Virgin Atlantic Reward Seat Checker.
type Program struct {
	client *http.Client
	mu     sync.Mutex
	cache  map[string]cached
}

// cached is one month's calendar and when it was fetched.
type cached struct {
	days    map[string]day
	fetched time.Time
}

// New builds a Program that makes its requests with client.
func New(client *http.Client) *Program {
	return &Program{client: client, cache: make(map[string]cached)}
}

// Key returns the program key, "virgin".
func (p *Program) Key() string {
	return Key
}

// Search returns Virgin Atlantic's estimated reward seat offers for q, one
// per cabin with seats for every passenger. It returns an error when the
// site cannot be reached, answers with a status other than 200, or answers
// with an error other than an unsupported route.
func (p *Program) Search(
	ctx context.Context,
	q award.Query,
) ([]award.Offer, error) {
	days, err := p.month(ctx, q.Origin, q.Destination, q.Date)
	if err != nil {
		return nil, err
	}
	d, ok := days[q.Date.Format(time.DateOnly)]
	if !ok {
		return []award.Offer{}, nil
	}
	return offers(q, d), nil
}

// month returns the calendar of the month of date from origin to
// destination keyed by YYYY-MM-DD, from the cache when it is fresh. It
// returns the error fetchMonth fails with.
func (p *Program) month(
	ctx context.Context,
	origin, destination string,
	date time.Time,
) (map[string]day, error) {
	key := origin + destination + date.Format("2006-01")
	p.mu.Lock()
	c, ok := p.cache[key]
	p.mu.Unlock()
	if ok && time.Since(c.fetched) < cacheTTL {
		return c.days, nil
	}
	days, err := p.fetchMonth(ctx, origin, destination, date)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, v := range p.cache {
		if time.Since(v.fetched) >= cacheTTL {
			delete(p.cache, k)
		}
	}
	p.cache[key] = cached{days: days, fetched: time.Now()}
	return days, nil
}

// fetchMonth asks the checker for the month of date from origin to
// destination and returns its days keyed by YYYY-MM-DD. It returns an empty
// map for a route the checker does not cover, and an error when the request
// fails, the status is not 200 or the answer cannot be read.
func (p *Program) fetchMonth(
	ctx context.Context,
	origin, destination string,
	date time.Time,
) (map[string]day, error) {
	body, err := json.Marshal(request{
		Query: calendarQuery,
		Variables: variables{
			Request: calendarRequest(origin, destination, date),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("virgin: encoding request: %w", err)
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		graphqlURL,
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("virgin: building request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "en-GB,en;q=0.9")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("virgin: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("virgin: answered %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("virgin: reading answer: %w", err)
	}
	return parseCalendar(data)
}

// calendarRequest is the checker's request for the month of date from
// origin to destination.
func calendarRequest(origin, destination string, date time.Time) monthRequest {
	return monthRequest{
		Slice: slice{
			Origin:      origin,
			Destination: destination,
			Departure:   date.Format("2006-01") + "-01",
		},
		Years:  []int{date.Year()},
		Months: []string{strings.ToUpper(date.Month().String())},
	}
}

// request is a GraphQL request body.
type request struct {
	// Query is the GraphQL document.
	Query string `json:"query"`
	// Variables are its variables.
	Variables variables `json:"variables"`
}

// variables are the variables of calendarQuery.
type variables struct {
	// Request is the calendar asked for.
	Request monthRequest `json:"request"`
}

// monthRequest is a RewardSeatAvailabilityRequestInput.
type monthRequest struct {
	// Slice is the route and the first day of the month.
	Slice slice `json:"slice"`
	// Years are the years of the months asked for.
	Years []int `json:"years"`
	// Months are the upper-case English month names asked for.
	Months []string `json:"months"`
}

// slice is a RewardSeatSliceInput.
type slice struct {
	// Origin is the IATA airport code departed from.
	Origin string `json:"origin"`
	// Destination is the IATA airport code arrived at.
	Destination string `json:"destination"`
	// Departure is the first day of the month, YYYY-MM-DD.
	Departure string `json:"departure"`
}

// response is the checker's GraphQL answer.
type response struct {
	// Data holds the calendar, null on error.
	Data struct {
		// RewardSeatAvailability is one entry per month asked for.
		RewardSeatAvailability []monthData `json:"rewardSeatAvailability"`
	} `json:"data"`
	// Errors are GraphQL errors.
	Errors []gqlError `json:"errors"`
}

// gqlError is one GraphQL error.
type gqlError struct {
	// Message says what went wrong.
	Message string `json:"message"`
	// Extensions carry the error code.
	Extensions struct {
		// Code is the machine-readable error, e.g. unsupportedRoute.
		Code string `json:"code"`
	} `json:"extensions"`
}

// monthData is one month of a route's calendar.
type monthData struct {
	// PointsDays are the days of the month that have reward seats.
	PointsDays []day `json:"pointsDays"`
}

// day is one day of the calendar.
type day struct {
	// Date is YYYY-MM-DD.
	Date string `json:"date"`
	// Seats are the day's lowest prices per cabin.
	Seats struct {
		// AwardEconomy is Economy, nil when there is no space.
		AwardEconomy *cabinDay `json:"awardEconomy"`
		// AwardPremium is Premium, nil when there is no space.
		AwardPremium *cabinDay `json:"awardPremium"`
		// AwardUpperClass is Upper Class, nil when there is no space.
		AwardUpperClass *cabinDay `json:"awardUpperClass"`
	} `json:"seats"`
}

// cabinDay is one cabin's lowest reward price on one day.
type cabinDay struct {
	// CabinPointsValue is the points for one passenger one way.
	CabinPointsValue float64 `json:"cabinPointsValue"`
	// Price is the lowest taxes, fees and charges for one passenger.
	Price float64 `json:"price"`
	// Currency is the ISO 4217 code of Price.
	Currency string `json:"currency"`
	// IsSaverAward is set when the points price is a Saver price.
	IsSaverAward bool `json:"isSaverAward"`
	// CabinClassSeatCount is the seats left, capped at 9.
	CabinClassSeatCount int `json:"cabinClassSeatCount"`
	// CabinClassSeatCountString is the seats left as shown, e.g. "9+".
	CabinClassSeatCountString string `json:"cabinClassSeatCountString"`
}

// parseCalendar decodes a checker answer into its days keyed by YYYY-MM-DD.
// It returns an empty map for an unsupported route, and an error when the
// answer is not JSON or carries any other GraphQL error.
func parseCalendar(data []byte) (map[string]day, error) {
	var res response
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("virgin: reading answer: %w", err)
	}
	days := make(map[string]day)
	for _, e := range res.Errors {
		if e.Extensions.Code == unsupportedRoute {
			return days, nil
		}
	}
	if len(res.Errors) > 0 {
		return nil, fmt.Errorf("virgin: %s", res.Errors[0].Message)
	}
	for _, m := range res.Data.RewardSeatAvailability {
		for _, d := range m.PointsDays {
			days[d.Date] = d
		}
	}
	return days, nil
}

// offers turns each cabin of d with seats for every passenger of q into an
// estimated offer.
func offers(q award.Query, d day) []award.Offer {
	out := []award.Offer{}
	cabins := []struct {
		cabin award.Cabin
		name  string
		data  *cabinDay
	}{
		{award.Economy, "Economy", d.Seats.AwardEconomy},
		{award.Premium, "Premium", d.Seats.AwardPremium},
		{award.Business, "Upper Class", d.Seats.AwardUpperClass},
	}
	for _, c := range cabins {
		if c.data == nil || c.data.CabinPointsValue <= 0 ||
			c.data.CabinClassSeatCount < q.Passengers {
			continue
		}
		out = append(out, offer(q, c.cabin, c.name, *c.data))
	}
	return out
}

// offer builds the estimated offer for q in cabin, which Virgin calls name,
// priced as c.
func offer(
	q award.Query,
	cabin award.Cabin,
	name string,
	c cabinDay,
) award.Offer {
	points := int(c.CabinPointsValue)
	n := q.Passengers
	seats := c.CabinClassSeatCount
	fare := "STANDARD"
	if c.IsSaverAward {
		fare = "SAVER"
	}
	depart := time.Date(
		q.Date.Year(), q.Date.Month(), q.Date.Day(), 0, 0, 0, 0, time.UTC,
	)
	notes := []string{
		"Virgin Atlantic " + name + " " + strings.ToLower(fare) +
			" reward: lowest points and taxes across all flights that day",
		"Flights and times are not given; search signed in to book",
		"Seats left: " + c.CabinClassSeatCountString,
	}
	return award.Offer{
		Program:      Key,
		Availability: award.Estimated,
		Origin:       q.Origin,
		Destination:  q.Destination,
		Departure:    depart,
		Arrival:      depart,
		Airlines:     []string{"VS"},
		Cabin:        cabin,
		Fare:         fare,
		Price: award.Price{
			PointsPerPassenger: points,
			PointsTotal:        points * n,
			TaxesPerPassenger: award.Money{
				Amount: c.Price, Currency: c.Currency,
			},
			TaxesTotal: award.Money{
				Amount: c.Price * float64(n), Currency: c.Currency,
			},
		},
		Seats:      &seats,
		BookingURL: bookingURL(q),
		Notes:      notes,
		Segments:   []award.Segment{},
	}
}

// bookingURL is virginatlantic.com's reward flight search for q.
func bookingURL(q award.Query) string {
	v := url.Values{}
	v.Set("passengers", "a"+strconv.Itoa(q.Passengers)+"t0c0i0")
	v.Set("origin", q.Origin)
	v.Set("destination", q.Destination)
	v.Set("awardSearch", "true")
	v.Set("departing", q.Date.Format(time.DateOnly))
	return bookingBase + "?" + v.Encode()
}
