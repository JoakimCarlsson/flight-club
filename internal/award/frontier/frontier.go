// Package frontier searches Frontier Airlines award availability, priced in
// FRONTIER Miles. Frontier flies one cabin, but sells each seat bare (the
// base miles fare) or in bundles that add bags and a better seat: the
// Economy bundle stays Economy, the Premium bundle adds an UpFront seat and
// is mapped to Premium, and the Business bundle adds an UpFront Plus seat
// with the middle blocked and is mapped to Business.
//
// The search needs no login: logged out, booking.flyfrontier.com's results
// page prices every flight in miles when asked for miles, and embeds the
// flights it found as JSON in the page. The site sits behind bot
// protection, so the page is loaded through the stealth browser after a
// visit to the home page, and the JSON is read from the rendered markup.
package frontier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/joakimcarlsson/flight-club/internal/award"
	"github.com/joakimcarlsson/flight-club/internal/browser"
)

// Key is the program key Frontier offers carry.
const Key = "frontier"

// selectURL is the results page the site's own search form opens.
const selectURL = "https://booking.flyfrontier.com/Flight/InternalSelect"

// homeURL is the site's home page, opened before the results page so the
// bot protection's scripts earn their cookies on an ordinary visit.
const homeURL = "https://www.flyfrontier.com/"

// loadTimeout bounds each navigation.
const loadTimeout = 60 * time.Second

// dataStart and dataEnd delimit the flights JSON the results page embeds.
const (
	dataStart = "FlightData = '"
	dataEnd   = "';"
)

// localLayout is how the page writes local and UTC times.
const localLayout = "2006-01-02T15:04:05"

// cityAirports expands IATA city codes to the airports the site searches.
var cityAirports = map[string][]string{
	"NYC": {"LGA", "EWR", "JFK"},
	"WAS": {"DCA", "IAD", "BWI"},
	"CHI": {"ORD", "MDW"},
	"DTT": {"DTW"},
	"QDF": {"DFW", "DAL"},
	"HOU": {"IAH", "HOU"},
	"QLA": {"LAX", "ONT", "SNA", "BUR", "LGB"},
	"SFO": {"SFO", "OAK", "SJC"},
	"MIA": {"MIA", "FLL", "PBI"},
	"ORL": {"MCO"},
	"YTO": {"YYZ"},
}

// Program searches flyfrontier.com through a stealth browser.
type Program struct {
	browser *browser.Browser
}

// New builds a Program that loads flyfrontier.com in b.
func New(b *browser.Browser) *Program {
	return &Program{browser: b}
}

// Key returns the program key, "frontier".
func (p *Program) Key() string {
	return Key
}

// errNoAnswer is returned when the results page carried no flights data,
// most often because the bot protection refused it.
var errNoAnswer = errors.New(
	"frontier: results page had no flight data (blocked or timed out)",
)

// Search returns Frontier's miles offers for q, expanding city codes to
// their airports. It returns an error when the browser cannot load the
// results page or the page carries no flights data it can read. A route
// Frontier does not fly gives no offers and no error.
func (p *Program) Search(
	ctx context.Context,
	q award.Query,
) ([]award.Offer, error) {
	var out []award.Offer
	for _, o := range airports(q.Origin) {
		for _, d := range airports(q.Destination) {
			if o == d {
				continue
			}
			leg := q
			leg.Origin, leg.Destination = o, d
			found, err := p.searchPair(ctx, leg)
			if err != nil {
				return nil, err
			}
			out = append(out, found...)
		}
	}
	if out == nil {
		out = []award.Offer{}
	}
	return out, nil
}

// airports expands an IATA city code to its airports, passing an airport
// code through.
func airports(code string) []string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if list, ok := cityAirports[code]; ok {
		return list
	}
	return []string{code}
}

// searchPair loads the results page for one airport pair and reads its
// offers. It fails as Search does.
func (p *Program) searchPair(
	ctx context.Context,
	q award.Query,
) ([]award.Offer, error) {
	booking := bookingURL(q)
	captured, err := p.browser.Collect(ctx, booking, browser.CollectOpts{
		Timeout: loadTimeout,
		WarmUp:  homeURL,
		Accept:  func(string, string) bool { return false },
	})
	if err != nil {
		return nil, fmt.Errorf("frontier: %w", err)
	}
	return parse(captured.HTML, q, booking)
}

// bookingURL is the one-way miles results page for q, as the site's own
// search form builds it, for every passenger of q.
func bookingURL(q award.Query) string {
	pax := max(q.Passengers, 1)
	v := url.Values{}
	v.Set("o1", q.Origin)
	v.Set("d1", q.Destination)
	v.Set("dd1", q.Date.Format("Jan 2, 2006"))
	v.Set("ADT", strconv.Itoa(pax))
	v.Set("loy", "true")
	v.Set("promo", "")
	v.Set("ftype", "Miles")
	return selectURL + "?" + v.Encode()
}

// flightData is the part of the page's embedded JSON the search reads.
type flightData struct {
	Journeys []struct {
		Flights []flight `json:"flights"`
	} `json:"journeys"`
}

// flight is one itinerary of the results page, priced in miles.
type flight struct {
	StandardFare        float64 `json:"standardFare"`
	MilesFare           float64 `json:"milesFare"`
	MilesBundleFareEcob float64 `json:"milesBundleFareEcob"`
	MilesBundleFarePrem float64 `json:"milesBundleFarePrem"`
	MilesBundleFareBusi float64 `json:"milesBundleFareBusi"`
	MilesBundleClass    string  `json:"milesBundleFareClass"`
	MilesFareKey        string  `json:"milesFareKey"`
	IsBusinessAvailable bool    `json:"isBusinessAvailable"`
	StopCount           int     `json:"stopCount"`
	Legs                []leg   `json:"legs"`
}

// leg is one flight of an itinerary.
type leg struct {
	DepartureStation string `json:"departureStation"`
	ArrivalStation   string `json:"arrivalStation"`
	DepartureDate    string `json:"departureDate"`
	ArrivalDate      string `json:"arrivalDate"`
	DepartureUTC     string `json:"departureDateUtc"`
	ArrivalUTC       string `json:"arrivalDateUtc"`
	FlightNumber     int    `json:"flightNumber"`
	CarrierCode      string `json:"carrierCode"`
	FirstSeats       int    `json:"legFirstSeatAvailabiltyCount"`
}

// fare is one way a flight is sold for miles.
type fare struct {
	name  string
	cabin award.Cabin
	extra float64
	ok    bool
}

// parse reads the offers out of the results page markup. It returns an
// error when the page carries no flights data or data it cannot read.
func parse(
	page string,
	q award.Query,
	booking string,
) ([]award.Offer, error) {
	i := strings.Index(page, dataStart)
	if i < 0 {
		return nil, errNoAnswer
	}
	rest := page[i+len(dataStart):]
	j := strings.Index(rest, dataEnd)
	if j < 0 {
		return nil, errNoAnswer
	}
	var data flightData
	raw := html.UnescapeString(rest[:j])
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return nil, fmt.Errorf("frontier: read flight data: %w", err)
	}
	pax := max(q.Passengers, 1)
	out := []award.Offer{}
	for _, j := range data.Journeys {
		for _, f := range j.Flights {
			found, err := offers(f, pax, booking)
			if err != nil {
				return nil, err
			}
			out = append(out, found...)
		}
	}
	return out, nil
}

// offers turns one flight into an offer per fare it is sold in, none when
// it has no miles price. It fails when a time cannot be read.
func offers(f flight, pax int, booking string) ([]award.Offer, error) {
	if f.StandardFare <= 0 || len(f.Legs) == 0 {
		return nil, nil
	}
	segs, err := segments(f)
	if err != nil {
		return nil, err
	}
	fares := []fare{
		{"BASIC", award.Economy, 0, true},
		{"ECONOMY_BUNDLE", award.Economy, f.MilesBundleFareEcob,
			f.MilesBundleFareEcob > 0},
		{"PREMIUM_BUNDLE", award.Premium, f.MilesBundleFarePrem,
			f.MilesBundleFarePrem > 0},
		{"BUSINESS_BUNDLE", award.Business, f.MilesBundleFareBusi,
			f.MilesBundleFareBusi > 0 && f.IsBusinessAvailable},
	}
	first, last := segs[0], segs[len(segs)-1]
	var out []award.Offer
	for _, fr := range fares {
		if !fr.ok {
			continue
		}
		points := int(math.Round(f.StandardFare + fr.extra))
		taxes := math.Round(f.MilesFare*100) / 100
		o := award.Offer{
			Program:      Key,
			Availability: award.Confirmed,
			Origin:       first.Origin,
			Destination:  last.Destination,
			Departure:    first.Departure,
			Arrival:      last.Arrival,
			DurationMinutes: int(
				last.Arrival.Sub(first.Departure).Minutes(),
			),
			Stops:    len(segs) - 1,
			Airlines: airlines(segs),
			Cabin:    fr.cabin,
			Fare:     fr.name,
			Price: award.Price{
				PointsPerPassenger: points,
				PointsTotal:        points * pax,
				TaxesPerPassenger: award.Money{
					Amount:   taxes,
					Currency: "USD",
				},
				TaxesTotal: award.Money{
					Amount:   math.Round(taxes*float64(pax)*100) / 100,
					Currency: "USD",
				},
			},
			BookingURL: booking,
			Segments:   withCabin(segs, fr.cabin, f.MilesBundleClass),
		}
		if fr.cabin == award.Business {
			o.Seats = upFrontSeats(f.Legs)
			if o.Seats != nil && *o.Seats == 0 {
				continue
			}
			o.Notes = []string{"UpFront Plus seat, bags included"}
		}
		if fr.cabin == award.Premium {
			o.Notes = []string{"UpFront seat, bags included"}
		}
		out = append(out, o)
	}
	return out, nil
}

// segments builds the segments of f, with each time in its airport's
// offset and the layover before the next flight. It fails when a time
// cannot be read.
func segments(f flight) ([]award.Segment, error) {
	segs := make([]award.Segment, 0, len(f.Legs))
	for _, l := range f.Legs {
		dep, err := zoned(l.DepartureDate, l.DepartureUTC)
		if err != nil {
			return nil, err
		}
		arr, err := zoned(l.ArrivalDate, l.ArrivalUTC)
		if err != nil {
			return nil, err
		}
		carrier := l.CarrierCode
		if carrier == "" {
			carrier = "F9"
		}
		segs = append(segs, award.Segment{
			Flight:           carrier + strconv.Itoa(l.FlightNumber),
			MarketingCarrier: carrier,
			OperatingCarrier: carrier,
			Origin:           l.DepartureStation,
			Destination:      l.ArrivalStation,
			Departure:        dep,
			Arrival:          arr,
			DurationMinutes:  int(arr.Sub(dep).Minutes()),
		})
	}
	for i := 0; i+1 < len(segs); i++ {
		gap := segs[i+1].Departure.Sub(segs[i].Arrival)
		segs[i].LayoverMinutes = int(gap.Minutes())
	}
	return segs, nil
}

// zoned reads a local time and the same instant in UTC, returning the local
// time in its own fixed offset. It fails when either cannot be read.
func zoned(local, utc string) (time.Time, error) {
	lt, err := time.Parse(localLayout, local)
	if err != nil {
		return time.Time{}, fmt.Errorf("frontier: time %q: %w", local, err)
	}
	ut, err := time.Parse(time.RFC3339, utc)
	if err != nil {
		ut, err = time.Parse(localLayout, strings.TrimSuffix(utc, "Z"))
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("frontier: time %q: %w", utc, err)
	}
	offset := int(lt.Sub(ut).Seconds())
	zone := time.FixedZone("", offset)
	return time.Date(
		lt.Year(), lt.Month(), lt.Day(),
		lt.Hour(), lt.Minute(), lt.Second(), 0, zone,
	), nil
}

// withCabin returns a copy of segs booked in cabin and fare class code.
func withCabin(
	segs []award.Segment,
	cabin award.Cabin,
	code string,
) []award.Segment {
	out := make([]award.Segment, len(segs))
	for i, s := range segs {
		s.Cabin = cabin
		s.BookingCode = code
		out[i] = s
	}
	return out
}

// airlines returns the operating carriers of segs in order of first
// appearance.
func airlines(segs []award.Segment) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range segs {
		if !seen[s.OperatingCarrier] {
			seen[s.OperatingCarrier] = true
			out = append(out, s.OperatingCarrier)
		}
	}
	return out
}

// upFrontSeats is the fewest UpFront seats left on any leg, nil when the
// page does not say.
func upFrontSeats(legs []leg) *int {
	n := -1
	for _, l := range legs {
		if n < 0 || l.FirstSeats < n {
			n = l.FirstSeats
		}
	}
	if n < 0 {
		return nil
	}
	return &n
}
