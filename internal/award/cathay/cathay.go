// Package cathay searches Cathay Pacific award availability, priced in
// Asia Miles.
//
// Cathay's own award search, the booking flow on book.cathaypacific.com,
// answers only a signed-in member: the entry point
// api.cathaypacific.com/redibe/IBEFacade?ACTION=RED_AWARD_SEARCH redirects
// a visitor without a session to the sign-in page, and the milesInfo API
// that prices its itineraries rejects a request without one. That is a
// login wall, not bot protection, so a stealth browser gets no further.
//
// What answers without a session is the award calendar behind the site's
// "flight award availability" widget:
// api.cathaypacific.com/afr/search/availability/... gives, for a nonstop
// Cathay route, a cabin and a number of passengers, whether each day has
// high, low or no Standard award space. It names no flights, so each day
// with space becomes one estimated offer per cabin for the nonstop route,
// priced from the Asia Miles Standard award chart for Cathay-operated
// flights by the great-circle distance of the route, with the carrier
// surcharge Cathay adds per sector as the taxes.
package cathay

import (
	"context"
	"encoding/json"
	"errors"
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

// Key is the program key Cathay offers carry.
const Key = "cathay"

// calendarURL is the award calendar API, followed by
// "<lang>.<origin>.<destination>.<cabin>.<brand>.<adults>.<from>.<to>.json".
const calendarURL = "https://api.cathaypacific.com/afr/search/availability/"

// awardPage is the page a traveller starts an award booking from.
const awardPage = "https://www.cathaypacific.com/cx/en_HK/book-a-trip/" +
	"redeem-flights/redeem-flight-awards.html"

// bookingBase is the booking flow's entry point, which signs a traveller in
// and opens the award search for a route and date.
const bookingBase = "https://api.cathaypacific.com/redibe/IBEFacade"

// userAgent is a current desktop Chrome, which the API answers like a
// browser.
const userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36"

// maxBody is the most of an answer that is read, far above the few hundred
// bytes a day's calendar is.
const maxBody = 1 << 20

// dateLayout is the calendar's date layout.
const dateLayout = "20060102"

// surchargeCurrency is the currency Cathay quotes its carrier surcharge in.
const surchargeCurrency = "HKD"

// Program searches Cathay Pacific's award calendar.
type Program struct {
	client *http.Client
}

// New builds a Program that makes its requests with client.
func New(client *http.Client) *Program {
	return &Program{client: client}
}

// Key returns the program key, "cathay".
func (p *Program) Key() string {
	return Key
}

// cabinCode pairs a cabin with the calendar's code for it and the fare
// name offers carry.
type cabinCode struct {
	cabin award.Cabin
	code  string
	fare  string
}

// cabins are the cabins the calendar is asked about, with Cathay's names
// for their Standard awards.
var cabins = []cabinCode{
	{award.Economy, "eco", "Standard Economy"},
	{award.Premium, "pey", "Standard Premium Economy"},
	{award.Business, "bus", "Standard Business"},
	{award.First, "fir", "Standard First"},
}

// Search returns Cathay's estimated award offers for q, one per cabin with
// Standard award space on the nonstop route, searching every airport of a
// city code. It returns an error when the calendar cannot be reached or
// answers with a status other than 200 or with something it cannot read.
// A route Cathay does not fly nonstop gives no offers and no error.
func (p *Program) Search(
	ctx context.Context,
	q award.Query,
) ([]award.Offer, error) {
	var out []award.Offer
	for _, from := range cityAirportsOf(q.Origin) {
		for _, to := range cityAirportsOf(q.Destination) {
			if from == to {
				continue
			}
			sub := q
			sub.Origin, sub.Destination = from, to
			found, err := p.searchRoute(ctx, sub)
			if err != nil {
				return nil, err
			}
			out = append(out, found...)
		}
	}
	return out, nil
}

// searchRoute asks the calendar about every cabin of q, whose origin and
// destination are airport codes, at once. It returns the first error any
// cabin's request returns.
func (p *Program) searchRoute(
	ctx context.Context,
	q award.Query,
) ([]award.Offer, error) {
	levels := make([]string, len(cabins))
	errs := make([]error, len(cabins))
	var wg sync.WaitGroup
	for i, c := range cabins {
		wg.Go(func() {
			levels[i], errs[i] = p.day(ctx, q, c.code)
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	var out []award.Offer
	for i, c := range cabins {
		if o, ok := offer(q, c, levels[i]); ok {
			out = append(out, o)
		}
	}
	return out, nil
}

// calendar is the award calendar's answer.
type calendar struct {
	// Availabilities holds the days.
	Availabilities struct {
		// Std are the days of Standard awards, empty for a route the
		// calendar does not cover.
		Std []struct {
			// Date is the day, YYYYMMDD.
			Date string `json:"date"`
			// Availability is "H" for high, "L" for low and "NA" for no
			// award space.
			Availability string `json:"availability"`
		} `json:"std"`
	} `json:"availabilities"`
}

// day returns the calendar's availability level for q's date in the cabin
// with calendar code cabin: "H", "L", "NA", or empty when the route is not
// covered. It returns an error when the calendar cannot be reached or
// answers with a status other than 200 or with something it cannot read.
func (p *Program) day(
	ctx context.Context,
	q award.Query,
	cabin string,
) (string, error) {
	req, err := http.NewRequestWithContext(
		ctx, http.MethodGet, dayURL(q, cabin), nil,
	)
	if err != nil {
		return "", fmt.Errorf("cathay: building request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Referer", awardPage)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("cathay: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("cathay: calendar answered %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return "", fmt.Errorf("cathay: reading answer: %w", err)
	}
	return parseDay(body, q.Date)
}

// parseDay reads date's availability level out of a calendar answer. It
// returns an empty level when the answer has no entry for date, and an
// error when body is not the calendar's JSON.
func parseDay(body []byte, date time.Time) (string, error) {
	var c calendar
	if err := json.Unmarshal(body, &c); err != nil {
		return "", fmt.Errorf("cathay: reading calendar: %w", err)
	}
	want := date.Format(dateLayout)
	for _, d := range c.Availabilities.Std {
		if d.Date == want {
			return d.Availability, nil
		}
	}
	return "", nil
}

// dayURL is the calendar URL for q's single date in the cabin with calendar
// code cabin.
func dayURL(q award.Query, cabin string) string {
	d := q.Date.Format(dateLayout)
	return calendarURL + strings.Join([]string{
		"en", q.Origin, q.Destination, cabin, "CX",
		strconv.Itoa(max(q.Passengers, 1)), d, d, "json",
	}, ".")
}

// bookingURL opens Cathay's award search for q in cabin, after the
// traveller signs in.
func bookingURL(q award.Query, cabin award.Cabin) string {
	v := url.Values{}
	v.Set("ACTION", "RED_AWARD_SEARCH")
	v.Set("ENTRYPOINT", awardPage)
	v.Set("ENTRYLANGUAGE", "en")
	v.Set("ENTRYCOUNTRY", "HK")
	v.Set("RETURNURL", awardPage)
	v.Set("ERRORURL", strings.TrimSuffix(awardPage, ".html")+
		".handler.html")
	v.Set("LOGINURL", "https://www.cathaypacific.com/cx/en_HK/sign-in.html")
	v.Set("CABINCLASS", cabinClass[cabin])
	v.Set("ADULT", strconv.Itoa(max(q.Passengers, 1)))
	v.Set("CHILD", "0")
	v.Set("FLEXIBLEDATE", "false")
	v.Set("BRAND", "CX")
	v.Set("ORIGIN[1]", q.Origin)
	v.Set("DESTINATION[1]", q.Destination)
	v.Set("DEPARTUREDATE[1]", q.Date.Format(dateLayout))
	return bookingBase + "?" + v.Encode()
}

// cabinClass is the booking flow's cabin code for each cabin.
var cabinClass = map[award.Cabin]string{
	award.Economy:  "Y",
	award.Premium:  "W",
	award.Business: "C",
	award.First:    "F",
}

// bookingCode is the booking class Cathay sells its own Standard awards
// in, per cabin.
var bookingCode = map[award.Cabin]string{
	award.Economy:  "X",
	award.Premium:  "T",
	award.Business: "U",
	award.First:    "Z",
}

// offer builds the estimated offer for q in cabin c, whose calendar level
// is level. It returns false when the day has no award space.
func offer(q award.Query, c cabinCode, level string) (award.Offer, bool) {
	if level != "H" && level != "L" {
		return award.Offer{}, false
	}
	from, okFrom := airports[q.Origin]
	to, okTo := airports[q.Destination]
	pax := max(q.Passengers, 1)
	notes := []string{
		"Day-level estimate from Cathay's award calendar; flight times " +
			"and exact miles need a signed-in search",
		levelNote(level),
	}
	var miles int
	var tax float64
	if okFrom && okTo {
		miles = chartMiles(from, to, c.cabin)
		tax = surcharge(q.Origin, q.Destination, from, to)
		notes = append(notes, "Miles from the Standard award chart")
	} else {
		notes = append(notes, "Miles unknown: airport not in the chart")
	}
	if okFrom && okTo && miles == 0 {
		return award.Offer{}, false
	}
	var dep time.Time
	if okFrom {
		dep = time.Date(q.Date.Year(), q.Date.Month(), q.Date.Day(),
			0, 0, 0, 0, from.location())
	} else {
		dep = time.Date(q.Date.Year(), q.Date.Month(), q.Date.Day(),
			0, 0, 0, 0, time.UTC)
	}
	return award.Offer{
		Program:      Key,
		Availability: award.Estimated,
		Origin:       q.Origin,
		Destination:  q.Destination,
		Departure:    dep,
		Arrival:      dep,
		Airlines:     []string{"CX"},
		Cabin:        c.cabin,
		Fare:         c.fare,
		Price: award.Price{
			PointsPerPassenger: miles,
			PointsTotal:        miles * pax,
			TaxesPerPassenger: award.Money{
				Amount: tax, Currency: surchargeCurrency,
			},
			TaxesTotal: award.Money{
				Amount: tax * float64(pax), Currency: surchargeCurrency,
			},
		},
		BookingURL: bookingURL(q, c.cabin),
		Notes:      notes,
		Segments: []award.Segment{{
			Flight:           "CX",
			MarketingCarrier: "CX",
			OperatingCarrier: "CX",
			Origin:           q.Origin,
			Destination:      q.Destination,
			Departure:        dep,
			Arrival:          dep,
			Cabin:            c.cabin,
			BookingCode:      bookingCode[c.cabin],
		}},
	}, true
}

// levelNote says what a calendar level means for the seats left.
func levelNote(level string) string {
	if level == "L" {
		return "Low award availability"
	}
	return "High award availability"
}
