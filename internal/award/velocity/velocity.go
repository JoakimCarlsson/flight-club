// Package velocity searches Virgin Australia Velocity Frequent Flyer award
// availability, priced in Velocity Points.
//
// Virgin Australia books on Sabre's Digital Experience storefront
// (book.virginaustralia.com/dx/VADX), which searches with points logged
// out: its flight selection page, opened with awardBooking=true, calls the
// storefront's GraphQL API (/api/graphql, operation bookingAirSearch) and
// lists every flight of the day in every fare brand priced in points plus
// cash taxes. The storefront sits behind Imperva, whose script must run
// before the API answers, so the search runs the way the site runs it: the
// stealth browser opens the flight selection deep link and the GraphQL
// answer is captured as the page loads it.
//
// The answer has two kinds of fare: Velocity reward seats (the "Reward"
// brands, such as Economy Reward and Business Reward), sold from award
// inventory at a fixed points price, and the ordinary fare brands (Lite,
// Choice, Flex, Business) priced in points at their cash value. Both are
// returned; the brand name is the offer's fare, and revenue fares carry a
// note. Times come with their offsets, and objects the answer repeats are
// sent once with an "@id" and referred to after with an "@ref".
package velocity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/joakimcarlsson/flight-club/internal/award"
	"github.com/joakimcarlsson/flight-club/internal/browser"
)

// Key is the program key Velocity offers carry.
const Key = "velocity"

// searchURL is the storefront's flight selection page, opened with the
// search in its fragment.
const searchURL = "https://book.virginaustralia.com/dx/VADX/" +
	"#/flight-selection?"

// graphQLPath identifies the storefront's GraphQL API among the responses
// the page loads.
const graphQLPath = "/api/graphql"

// pointsCurrency is the currency code the answer gives points in.
const pointsCurrency = "FFCURRENCY"

// loadTimeout bounds the navigation to the flight selection page.
const loadTimeout = 60 * time.Second

// settleRounds and settleDelay are how long the page is given, after it
// loads, to run its search: settleRounds waits of settleDelay each.
const (
	settleRounds = 4
	settleDelay  = 4 * time.Second
)

// localLayout is the layout of the answer's local times.
const localLayout = "2006-01-02T15:04:05"

// Program searches book.virginaustralia.com through a stealth browser.
type Program struct {
	browser *browser.Browser
}

// New builds a Program that loads book.virginaustralia.com in b.
func New(b *browser.Browser) *Program {
	return &Program{browser: b}
}

// Key returns the program key, "velocity".
func (p *Program) Key() string {
	return Key
}

// errNoAnswer is returned when the page never received a search answer,
// as when the bot protection stops it.
var errNoAnswer = errors.New(
	"velocity: the booking page returned no search results " +
		"(possibly blocked by the site's bot protection)",
)

// Search returns Velocity's award offers for q. It returns an error when
// the browser cannot load the booking page, the page gets no search answer,
// or the answer cannot be read. A route Virgin Australia does not sell
// gives no offers and no error.
func (p *Program) Search(
	ctx context.Context,
	q award.Query,
) ([]award.Offer, error) {
	captured, err := p.browser.Collect(ctx, pageURL(q, 1),
		browser.CollectOpts{
			Timeout:     loadTimeout,
			Scrolls:     settleRounds,
			ScrollDelay: settleDelay,
			Accept:      func(string, string) bool { return false },
			CaptureBody: func(u, _ string) bool {
				return strings.Contains(u, graphQLPath)
			},
		})
	if err != nil {
		return nil, fmt.Errorf("velocity: %w", err)
	}
	var lastErr error
	for _, b := range captured.Bodies {
		if !strings.Contains(string(b.Data), "bookingAirSearch") {
			continue
		}
		offers, err := parse(b.Data, q)
		if err != nil {
			lastErr = err
			continue
		}
		return offers, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errNoAnswer
}

// pageURL is the flight selection page for a one-way points search for q
// with adults travellers.
func pageURL(q award.Query, adults int) string {
	v := url.Values{}
	v.Set("journeyType", "one-way")
	v.Set("locale", "en-AU")
	v.Set("awardBooking", "true")
	v.Set("searchType", "BRANDED")
	v.Set("cabinClass", "Economy")
	v.Set("ADT", strconv.Itoa(adults))
	v.Set("CHD", "0")
	v.Set("INF", "0")
	v.Set("origin", q.Origin)
	v.Set("destination", q.Destination)
	v.Set("date", q.Date.Format("01-02-2006"))
	return searchURL + v.Encode()
}

// answer is the GraphQL response of the bookingAirSearch operation.
type answer struct {
	Data struct {
		BookingAirSearch *struct {
			OriginalResponse json.RawMessage `json:"originalResponse"`
		} `json:"bookingAirSearch"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// response is the part of the storefront's search answer read here.
type response struct {
	FareFamilies []struct {
		BrandID    string `json:"brandId"`
		BrandLabel []struct {
			LanguageID    string `json:"languageId"`
			MarketingText string `json:"marketingText"`
		} `json:"brandLabel"`
	} `json:"fareFamilies"`
	BrandedResults struct {
		ItineraryPartBrands [][]partBrands `json:"itineraryPartBrands"`
	} `json:"brandedResults"`
}

// partBrands is one itinerary with its offers in every fare brand.
type partBrands struct {
	ItineraryPart ref          `json:"itineraryPart"`
	BrandOffers   []brandOffer `json:"brandOffers"`
}

// ref points at an object sent elsewhere in the answer under its "@id".
type ref struct {
	Ref string `json:"@ref"`
	ID  string `json:"@id"`
}

// brandOffer is an itinerary's price in one fare brand.
type brandOffer struct {
	BrandID        string `json:"brandId"`
	Soldout        bool   `json:"soldout"`
	CabinClass     string `json:"cabinClass"`
	SeatsRemaining *struct {
		Count int `json:"count"`
	} `json:"seatsRemaining"`
	Fare  alternatives `json:"fare"`
	Taxes alternatives `json:"taxes"`
}

// alternatives are the ways a price can be paid; each is a list of amounts
// paid together.
type alternatives struct {
	Alternatives [][]amount `json:"alternatives"`
}

// amount is a sum in one currency, FFCURRENCY for points.
type amount struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

// itineraryPart is one one-way itinerary.
type itineraryPart struct {
	Segments []json.RawMessage `json:"segments"`
}

// segment is one flight of an itinerary.
type segment struct {
	Duration   int    `json:"duration"`
	CabinClass string `json:"cabinClass"`
	Equipment  string `json:"equipment"`
	Flight     struct {
		FlightNumber         int    `json:"flightNumber"`
		AirlineCode          string `json:"airlineCode"`
		OperatingAirlineCode string `json:"operatingAirlineCode"`
	} `json:"flight"`
	Origin             string `json:"origin"`
	Destination        string `json:"destination"`
	Departure          string `json:"departure"`
	Arrival            string `json:"arrival"`
	DepartureGMTOffset string `json:"departureGMTOffset"`
	ArrivalGMTOffset   string `json:"arrivalGMTOffset"`
}

// parse turns a bookingAirSearch answer into the offers for q. It returns
// an error when the answer cannot be decoded or reports an error without
// results.
func parse(body []byte, q award.Query) ([]award.Offer, error) {
	var a answer
	if err := json.Unmarshal(body, &a); err != nil {
		return nil, fmt.Errorf("velocity: decode answer: %w", err)
	}
	search := a.Data.BookingAirSearch
	if search == nil || len(search.OriginalResponse) == 0 ||
		string(search.OriginalResponse) == "null" {
		if len(a.Errors) > 0 {
			return nil, fmt.Errorf("velocity: search failed: %s",
				a.Errors[0].Message)
		}
		return []award.Offer{}, nil
	}
	var raw any
	if err := json.Unmarshal(search.OriginalResponse, &raw); err != nil {
		return nil, fmt.Errorf("velocity: decode results: %w", err)
	}
	objects := map[string]any{}
	index(raw, objects)
	var r response
	if err := json.Unmarshal(search.OriginalResponse, &r); err != nil {
		return nil, fmt.Errorf("velocity: decode results: %w", err)
	}
	brands := brandNames(r)
	out := []award.Offer{}
	for _, leg := range r.BrandedResults.ItineraryPartBrands {
		for _, pb := range leg {
			segs, err := segments(pb.ItineraryPart, objects)
			if err != nil {
				return nil, err
			}
			if len(segs) == 0 {
				continue
			}
			for _, bo := range pb.BrandOffers {
				o, ok := offer(bo, segs, brands, q)
				if ok {
					out = append(out, o)
				}
			}
		}
	}
	return out, nil
}

// index records in objects every object under v that carries an "@id",
// keyed by it.
func index(v any, objects map[string]any) {
	switch t := v.(type) {
	case map[string]any:
		if id, ok := t["@id"].(string); ok {
			objects[id] = t
		}
		for _, c := range t {
			index(c, objects)
		}
	case []any:
		for _, c := range t {
			index(c, objects)
		}
	}
}

// resolve decodes into dst the object raw is, or the one it refers to with
// an "@ref". It returns an error when the reference is unknown or the
// object cannot be decoded.
func resolve(raw json.RawMessage, objects map[string]any, dst any) error {
	var r ref
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("velocity: decode reference: %w", err)
	}
	if r.Ref != "" {
		obj, ok := objects[r.Ref]
		if !ok {
			return fmt.Errorf("velocity: unknown reference %q", r.Ref)
		}
		b, err := json.Marshal(obj)
		if err != nil {
			return err
		}
		raw = b
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("velocity: decode object: %w", err)
	}
	return nil
}

// segments returns the flights of the itinerary part p points at. It
// returns an error when a reference cannot be resolved.
func segments(p ref, objects map[string]any) ([]segment, error) {
	key := p.Ref
	if key == "" {
		key = p.ID
	}
	obj, ok := objects[key]
	if !ok {
		return nil, fmt.Errorf("velocity: unknown itinerary %q", key)
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	var part itineraryPart
	if err := json.Unmarshal(b, &part); err != nil {
		return nil, fmt.Errorf("velocity: decode itinerary: %w", err)
	}
	out := make([]segment, 0, len(part.Segments))
	for _, raw := range part.Segments {
		var s segment
		if err := resolve(raw, objects, &s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// brandNames maps each fare brand's code to its English name, such as
// "RE" to "Economy Reward".
func brandNames(r response) map[string]string {
	out := make(map[string]string, len(r.FareFamilies))
	for _, f := range r.FareFamilies {
		for _, l := range f.BrandLabel {
			if l.LanguageID == "en_GB" || out[f.BrandID] == "" {
				out[f.BrandID] = l.MarketingText
			}
		}
	}
	return out
}

// offer turns one brand's price for the itinerary of segs into an offer
// for q. It reports false when the brand is sold out or has no points
// price.
func offer(
	bo brandOffer,
	segs []segment,
	brands map[string]string,
	q award.Query,
) (award.Offer, bool) {
	if bo.Soldout {
		return award.Offer{}, false
	}
	points, ok := pointsOf(bo.Fare)
	if !ok || points <= 0 {
		return award.Offer{}, false
	}
	taxes, currency := cashOf(bo.Taxes)
	pax := max(q.Passengers, 1)
	cabin := cabinOf(bo.CabinClass)
	out := award.Offer{
		Program:      Key,
		Availability: award.Confirmed,
		Origin:       segs[0].Origin,
		Destination:  segs[len(segs)-1].Destination,
		Stops:        len(segs) - 1,
		Cabin:        cabin,
		Fare:         fareName(bo.BrandID, brands),
		Price: award.Price{
			PointsPerPassenger: points,
			PointsTotal:        points * pax,
			TaxesPerPassenger:  award.Money{Amount: taxes, Currency: currency},
			TaxesTotal: award.Money{
				Amount:   math.Round(taxes*float64(pax)*100) / 100,
				Currency: currency,
			},
		},
		BookingURL: pageURL(q, pax),
	}
	if bo.SeatsRemaining != nil && bo.SeatsRemaining.Count > 0 {
		n := bo.SeatsRemaining.Count
		out.Seats = &n
	}
	if !strings.Contains(strings.ToLower(out.Fare), "reward") {
		out.Notes = append(out.Notes,
			"Revenue fare priced in points (not a reward seat)")
	}
	seen := map[string]bool{}
	for _, s := range segs {
		seg := toSegment(s, cabin)
		if seg.Cabin != cabin {
			out.MixedCabin = true
		}
		if n := len(out.Segments); n > 0 {
			prev := &out.Segments[n-1]
			gap := seg.Departure.Sub(prev.Arrival)
			prev.LayoverMinutes = int(gap.Minutes())
		}
		out.Segments = append(out.Segments, seg)
		if !seen[seg.OperatingCarrier] {
			seen[seg.OperatingCarrier] = true
			out.Airlines = append(out.Airlines, seg.OperatingCarrier)
		}
	}
	out.Departure = out.Segments[0].Departure
	out.Arrival = out.Segments[len(out.Segments)-1].Arrival
	if !out.Departure.IsZero() && !out.Arrival.IsZero() {
		out.DurationMinutes = int(out.Arrival.Sub(out.Departure).Minutes())
	}
	return out, true
}

// fareName is the English name of brand, or its code when the answer does
// not name it.
func fareName(brand string, brands map[string]string) string {
	if n := brands[brand]; n != "" {
		return n
	}
	return brand
}

// pointsOf returns the points price of a, the alternative paid in points
// alone. It reports false when there is none.
func pointsOf(a alternatives) (int, bool) {
	for _, alt := range a.Alternatives {
		if len(alt) == 1 && alt[0].Currency == pointsCurrency {
			return int(math.Round(alt[0].Amount)), true
		}
	}
	return 0, false
}

// cashOf returns the cash price of a and its currency, the alternative
// paid in money alone, or zero AUD when there is none.
func cashOf(a alternatives) (float64, string) {
	for _, alt := range a.Alternatives {
		if len(alt) == 1 && alt[0].Currency != pointsCurrency {
			return alt[0].Amount, alt[0].Currency
		}
	}
	return 0, "AUD"
}

// cabinOf maps the answer's cabin class to a cabin.
func cabinOf(class string) award.Cabin {
	c := strings.ToLower(class)
	switch {
	case strings.Contains(c, "premium"):
		return award.Premium
	case strings.Contains(c, "business"):
		return award.Business
	case strings.Contains(c, "first"):
		return award.First
	}
	return award.Economy
}

// toSegment turns a flight of the answer into a segment booked in cabin.
// The itinerary's segments are shared by every brand, so the cabin is the
// brand's, and the booking class, which differs by brand, is left out.
func toSegment(s segment, cabin award.Cabin) award.Segment {
	operating := s.Flight.OperatingAirlineCode
	if operating == "" {
		operating = s.Flight.AirlineCode
	}
	return award.Segment{
		Flight: fmt.Sprintf("%s%d", s.Flight.AirlineCode,
			s.Flight.FlightNumber),
		MarketingCarrier: s.Flight.AirlineCode,
		OperatingCarrier: operating,
		Origin:           s.Origin,
		Destination:      s.Destination,
		Departure:        localTime(s.Departure, s.DepartureGMTOffset),
		Arrival:          localTime(s.Arrival, s.ArrivalGMTOffset),
		DurationMinutes:  s.Duration,
		Aircraft:         s.Equipment,
		Cabin:            cabin,
	}
}

// localTime parses a local time with its offset, such as
// "2026-12-10T06:30:00" and "+11:00". It returns the zero time when either
// cannot be read.
func localTime(local, offset string) time.Time {
	t, err := time.Parse(localLayout+"-07:00", local+offset)
	if err != nil {
		return time.Time{}
	}
	return t
}
