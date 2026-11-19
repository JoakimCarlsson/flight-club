// Package southwest searches Southwest Airlines award availability, priced
// in Rapid Rewards points. Southwest sells every seat for points in the same
// fare families it sells for cash (Basic, Wanna Get Away, Wanna Get Away
// Plus, Anytime and Business Select), all in one economy cabin, so every
// offer is Economy with the family's name as its fare.
//
// The search needs no login: logged out, southwest.com's results page
// fetches points fares from its own shopping API. That API sits behind bot
// protection that refuses plain requests, so the search is run the way the
// site runs it: the stealth browser opens the home page, then the points
// results page for the route and day, and the shopping API's JSON answer is
// captured as the page loads it.
package southwest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/joakimcarlsson/flight-club/internal/award"
	"github.com/joakimcarlsson/flight-club/internal/browser"
)

// Key is the program key Southwest offers carry.
const Key = "southwest"

// resultsURL is the points results page a traveller searches and books
// from.
const resultsURL = "https://www.southwest.com/air/booking/select-depart.html"

// homeURL is the site's home page, opened before the results page so the
// bot protection's scripts earn their cookies on an ordinary visit.
const homeURL = "https://www.southwest.com/"

// shoppingPath identifies the shopping API's answer among the responses
// the results page loads.
const shoppingPath = "/air-booking/page/air/booking/shopping"

// loadTimeout bounds each navigation.
const loadTimeout = 60 * time.Second

// settleRounds and settleDelay are how long the results page is given,
// after it loads, to call the shopping API.
const (
	settleRounds = 4
	settleDelay  = 3 * time.Second
)

// fareNames are the traveller-facing names of the points fare families.
var fareNames = map[string]string{
	"BASRED": "Basic",
	"WGARED": "Wanna Get Away",
	"PLURED": "Wanna Get Away Plus",
	"ANYRED": "Anytime",
	"BUSRED": "Business Select",
}

// fareOrder is the order the site shows its fare families in.
var fareOrder = []string{"BASRED", "WGARED", "PLURED", "ANYRED", "BUSRED"}

// Program searches southwest.com through a stealth browser.
type Program struct {
	browser *browser.Browser
}

// New builds a Program that loads southwest.com in b.
func New(b *browser.Browser) *Program {
	return &Program{browser: b}
}

// Key returns the program key, "southwest".
func (p *Program) Key() string {
	return Key
}

// errNoAnswer is returned when the results page never called the shopping
// API, most often because the bot protection refused it.
var errNoAnswer = errors.New(
	"southwest: the results page returned no fares (blocked or timed out)",
)

// Search returns Southwest's points offers for q. It returns an error when
// the browser cannot load the results page, the page never fetches fares,
// or the shopping API answers with something that cannot be read or with
// an error. A route Southwest does not fly gives no offers and no error.
func (p *Program) Search(
	ctx context.Context,
	q award.Query,
) ([]award.Offer, error) {
	booking := bookingURL(q)
	captured, err := p.browser.Collect(ctx, booking, browser.CollectOpts{
		Timeout:     loadTimeout,
		WarmUp:      homeURL,
		Scrolls:     settleRounds,
		ScrollDelay: settleDelay,
		Accept:      func(string, string) bool { return false },
		CaptureBody: func(u, _ string) bool {
			return strings.Contains(u, shoppingPath)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("southwest: %w", err)
	}
	var lastErr error = errNoAnswer
	for _, b := range captured.Bodies {
		out, err := parse(b.Data, q, booking)
		if err == nil {
			return out, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// bookingURL is the one-way points results page for q, as the site's own
// search form builds it, for every passenger of q.
func bookingURL(q award.Query) string {
	n := strconv.Itoa(max(q.Passengers, 1))
	v := url.Values{}
	v.Set("adultPassengersCount", n)
	v.Set("adultsCount", n)
	v.Set("departureDate", q.Date.Format(time.DateOnly))
	v.Set("departureTimeOfDay", "ALL_DAY")
	v.Set("destinationAirportCode", q.Destination)
	v.Set("fareType", "POINTS")
	v.Set("int", "HOMEQBOMAIR")
	v.Set("originationAirportCode", q.Origin)
	v.Set("passengerType", "ADULT")
	v.Set("returnDate", "")
	v.Set("returnTimeOfDay", "ALL_DAY")
	v.Set("tripType", "oneway")
	return resultsURL + "?" + v.Encode()
}

// answer is the part of the shopping API's answer that is read.
type answer struct {
	Success *bool `json:"success"`
	Data    *struct {
		SearchResults *struct {
			AirProducts []struct {
				Details []detail `json:"details"`
			} `json:"airProducts"`
		} `json:"searchResults"`
	} `json:"data"`
	Notifications json.RawMessage `json:"notifications"`
}

// detail is one itinerary with its fare families.
type detail struct {
	Origin        string                        `json:"originationAirportCode"`
	Destination   string                        `json:"destinationAirportCode"`
	Departure     string                        `json:"departureDateTime"`
	Arrival       string                        `json:"arrivalDateTime"`
	TotalDuration int                           `json:"totalDuration"`
	FareProducts  map[string]map[string]product `json:"fareProducts"`
	Segments      []segment                     `json:"segments"`
}

// segment is one flight of an itinerary.
type segment struct {
	Operating   string `json:"operatingCarrierCode"`
	Marketing   string `json:"marketingCarrierCode"`
	FlightNo    string `json:"flightNumber"`
	Origin      string `json:"originationAirportCode"`
	Destination string `json:"destinationAirportCode"`
	Departure   string `json:"departureDateTime"`
	Arrival     string `json:"arrivalDateTime"`
	Aircraft    string `json:"aircraftEquipmentType"`
}

// product is one fare family's price for an itinerary.
type product struct {
	ProductID    string `json:"productId"`
	Availability string `json:"availabilityStatus"`
	Fare         *struct {
		TotalFare amount `json:"totalFare"`
		Taxes     amount `json:"totalTaxesAndFees"`
		SeatsLeft *int   `json:"seatsLeft"`
	} `json:"fare"`
}

// amount is a value in a currency, or in "POINTS".
type amount struct {
	Currency string `json:"currencyCode"`
	Value    string `json:"value"`
}

// parse turns a shopping API answer into the offers for q, booked at
// booking. It returns an error when body is not the API's JSON or the API
// says the search failed without a reason that means no flights.
func parse(
	body []byte,
	q award.Query,
	booking string,
) ([]award.Offer, error) {
	var a answer
	if err := json.Unmarshal(body, &a); err != nil {
		return nil, fmt.Errorf("southwest: read answer: %w", err)
	}
	out := []award.Offer{}
	if a.Data == nil || a.Data.SearchResults == nil {
		if a.Success != nil && !*a.Success && !noFlights(body) {
			return nil, fmt.Errorf("southwest: search failed: %s",
				compact(a.Notifications))
		}
		return out, nil
	}
	n := max(q.Passengers, 1)
	for _, ap := range a.Data.SearchResults.AirProducts {
		for _, d := range ap.Details {
			out = append(out, offers(d, n, booking)...)
		}
	}
	return out, nil
}

// noFlights reports whether a failed answer says the route or day simply
// has no flights to sell.
func noFlights(body []byte) bool {
	s := strings.ToUpper(string(body))
	for _, m := range []string{
		"NO_FLIGHTS", "NOT_SERVED", "NO_ROUTES", "INVALID_MARKET",
		"NO FLIGHTS", "DOES NOT SERVE", "NOT AVAILABLE",
	} {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// compact shortens raw JSON to a line fit for an error message.
func compact(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	if s == "" {
		return "no reason given"
	}
	return s
}

// offers turns one itinerary into an offer per available fare family, for
// n passengers, booked at booking. An itinerary whose times cannot be read
// gives no offers.
func offers(d detail, n int, booking string) []award.Offer {
	dep, err1 := time.Parse(time.RFC3339, d.Departure)
	arr, err2 := time.Parse(time.RFC3339, d.Arrival)
	if err1 != nil || err2 != nil || len(d.Segments) == 0 {
		return nil
	}
	segs, airlines, ok := segments(d.Segments)
	if !ok {
		return nil
	}
	duration := d.TotalDuration
	if duration <= 0 {
		duration = int(arr.Sub(dep).Minutes())
	}
	families := d.FareProducts["ADULT"]
	var out []award.Offer
	for _, code := range familyCodes(families) {
		p := families[code]
		if p.Availability != "AVAILABLE" || p.Fare == nil {
			continue
		}
		points, err := strconv.Atoi(p.Fare.TotalFare.Value)
		if err != nil || p.Fare.TotalFare.Currency != "POINTS" {
			continue
		}
		tax, _ := strconv.ParseFloat(p.Fare.Taxes.Value, 64)
		currency := p.Fare.Taxes.Currency
		if currency == "" {
			currency = "USD"
		}
		withCodes := bookingCodes(segs, p.ProductID)
		out = append(out, award.Offer{
			Program:         Key,
			Availability:    award.Confirmed,
			Origin:          d.Origin,
			Destination:     d.Destination,
			Departure:       dep,
			Arrival:         arr,
			DurationMinutes: duration,
			Stops:           len(segs) - 1,
			Airlines:        airlines,
			Cabin:           award.Economy,
			Fare:            fareName(code),
			Price: award.Price{
				PointsPerPassenger: points,
				PointsTotal:        points * n,
				TaxesPerPassenger: award.Money{
					Amount: tax, Currency: currency,
				},
				TaxesTotal: award.Money{
					Amount:   roundCents(tax * float64(n)),
					Currency: currency,
				},
			},
			Seats:      p.Fare.SeatsLeft,
			BookingURL: booking,
			Segments:   withCodes,
		})
	}
	return out
}

// familyCodes returns the fare family codes of families in the order the
// site shows them, unknown ones last in name order.
func familyCodes(families map[string]product) []string {
	var out, rest []string
	for _, c := range fareOrder {
		if _, ok := families[c]; ok {
			out = append(out, c)
		}
	}
	for c := range families {
		if !slices.Contains(fareOrder, c) {
			rest = append(rest, c)
		}
	}
	slices.Sort(rest)
	return append(out, rest...)
}

// fareName is the traveller-facing name of the fare family code, or the
// code itself when it is not known.
func fareName(code string) string {
	if name, ok := fareNames[code]; ok {
		return name
	}
	return code
}

// segments turns the flights of an itinerary into offer segments, with
// connection times, and lists the operating carriers in order of first
// appearance. It returns false when a time cannot be read.
func segments(in []segment) ([]award.Segment, []string, bool) {
	out := make([]award.Segment, 0, len(in))
	var airlines []string
	for _, s := range in {
		dep, err1 := time.Parse(time.RFC3339, s.Departure)
		arr, err2 := time.Parse(time.RFC3339, s.Arrival)
		if err1 != nil || err2 != nil {
			return nil, nil, false
		}
		marketing := carrier(s.Marketing)
		operating := carrier(s.Operating)
		if s.Operating == "" {
			operating = marketing
		}
		if !slices.Contains(airlines, operating) {
			airlines = append(airlines, operating)
		}
		out = append(out, award.Segment{
			Flight:           marketing + s.FlightNo,
			MarketingCarrier: marketing,
			OperatingCarrier: operating,
			Origin:           s.Origin,
			Destination:      s.Destination,
			Departure:        dep,
			Arrival:          arr,
			DurationMinutes:  int(arr.Sub(dep).Minutes()),
			Aircraft:         s.Aircraft,
			Cabin:            award.Economy,
		})
	}
	for i := 0; i+1 < len(out); i++ {
		out[i].LayoverMinutes = int(
			out[i+1].Departure.Sub(out[i].Arrival).Minutes(),
		)
	}
	return out, airlines, true
}

// carrier returns code, or "WN" for Southwest when it is empty.
func carrier(code string) string {
	if code == "" {
		return "WN"
	}
	return code
}

// bookingCodes returns a copy of segs with the fare class letter of each
// segment read from productID, which lists the family and then, per
// segment, "fareBasis,class,origin,...". Segments it cannot match keep an
// empty code.
func bookingCodes(segs []award.Segment, productID string) []award.Segment {
	out := slices.Clone(segs)
	parts := strings.Split(productID, "|")
	if len(parts) != len(segs)+1 {
		return out
	}
	for i, part := range parts[1:] {
		fields := strings.Split(part, ",")
		if len(fields) > 1 {
			out[i].BookingCode = fields[1]
		}
	}
	return out
}

// roundCents rounds v to whole cents.
func roundCents(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}
