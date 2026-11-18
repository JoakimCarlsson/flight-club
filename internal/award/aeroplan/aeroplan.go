// Package aeroplan searches Air Canada Aeroplan award availability, priced
// in Aeroplan points plus taxes, fees and carrier charges in cash.
//
// aircanada.com sits behind Akamai Bot Manager: plain requests to the site
// or to its availability API on dbaas.aircanada.com are refused with
// "Access Denied". The API is therefore read the way the site reads it: the
// stealth browser opens the redeem availability page for the route and day,
// the page's Angular application calls the air-bounds API itself, and that
// JSON answer is captured as the page loads it. Since March 2025 the site
// only shows award availability to a signed-in Aeroplan member and sends
// anyone else to its sign-in page, which Search reports as an error.
//
// The answer lists air bound groups, one per itinerary, each with its air
// bounds, one per fare family (Standard, Flex, Comfort, Latitude and so on)
// that still has seats. Every air bound becomes one offer. Flight details,
// with local times carrying their offsets, come from the answer's flight
// dictionary; a flight missing from it is read from its flight id instead.
package aeroplan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/joakimcarlsson/flight-club/internal/award"
	"github.com/joakimcarlsson/flight-club/internal/browser"
)

// Key is the program key Aeroplan offers carry.
const Key = "aeroplan"

// availabilityURL is the redeem results page a traveller searches and books
// from.
const availabilityURL = "https://www.aircanada.com/aeroplan/redeem/" +
	"availability/outbound"

// apiHost is the host of the availability API whose answers the page loads.
const apiHost = "dbaas.aircanada.com"

// noFlights is the API's error code for a day with no award flights.
const noFlights = "7959"

// homeURL is the site's home page, opened before the results page so the
// bot protection's sensor script earns its cookies on an ordinary visit.
const homeURL = "https://www.aircanada.com/ca/en/aco/home.html"

// loadTimeout bounds the navigation to the results page.
const loadTimeout = 60 * time.Second

// settleRounds and settleDelay are how long the results page is given, after
// it loads, to call the availability API: settleRounds waits of settleDelay
// each.
const (
	settleRounds = 5
	settleDelay  = 4 * time.Second
)

// localLayout is the layout of a time read from a flight id, which has no
// offset.
const localLayout = "2006-01-02T15:04"

// errBlocked is returned when aircanada.com answers with Akamai's denial
// instead of the results page.
var errBlocked = errors.New(
	"aeroplan: access denied by the site's bot protection",
)

// loginPath is the part of the sign-in page's URL the results page sends
// a visitor to when no Aeroplan member is signed in.
const loginPath = "/clogin/pages/login"

// errLoginRequired is returned when the results page sends the browser to
// the sign-in page: aircanada.com has only shown award availability to
// signed-in Aeroplan members since March 2025.
var errLoginRequired = errors.New(
	"aeroplan: aircanada.com requires a signed-in Aeroplan member to " +
		"search awards",
)

// Program searches aircanada.com through a stealth browser.
type Program struct {
	browser *browser.Browser
}

// New builds a Program that loads aircanada.com in b.
func New(b *browser.Browser) *Program {
	return &Program{browser: b}
}

// Key returns the program key, "aeroplan".
func (p *Program) Key() string {
	return Key
}

// Search returns Aeroplan's award offers for q, one per fare family of
// every itinerary. It returns an error when the browser cannot load the
// results page, the page is denied by the bot protection, or the API
// answers with something that cannot be read or reports a failure other
// than no flights. A day without award flights gives no offers and no
// error.
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
		CaptureBody: func(u, ct string) bool {
			return strings.Contains(u, apiHost) &&
				strings.Contains(strings.ToLower(ct), "json")
		},
	})
	if err != nil {
		return nil, fmt.Errorf("aeroplan: %w", err)
	}
	var bodies [][]byte
	for _, b := range captured.Bodies {
		bodies = append(bodies, b.Data)
	}
	a, found, err := pickAnswer(bodies)
	if err != nil {
		return nil, err
	}
	if !found {
		if strings.Contains(captured.URL, loginPath) {
			return nil, errLoginRequired
		}
		if strings.Contains(captured.HTML, "Access Denied") {
			return nil, errBlocked
		}
		return nil, errors.New("aeroplan: no availability answer captured")
	}
	return offers(a, q.Passengers, booking), nil
}

// bookingURL is the redeem results page for a one-way search for q.
func bookingURL(q award.Query) string {
	v := url.Values{}
	v.Set("tripType", "O")
	v.Set("org0", q.Origin)
	v.Set("dest0", q.Destination)
	v.Set("departureDate0", q.Date.Format(time.DateOnly))
	v.Set("ADT", strconv.Itoa(max(q.Passengers, 1)))
	v.Set("YTH", "0")
	v.Set("CHD", "0")
	v.Set("INF", "0")
	v.Set("INS", "0")
	return availabilityURL + "?" + v.Encode()
}

// answer is the part of an air-bounds API answer that is read.
type answer struct {
	// Data holds the results.
	Data struct {
		// AirBoundGroups are the itineraries with their fares.
		AirBoundGroups []boundGroup `json:"airBoundGroups"`
	} `json:"data"`
	// Dictionaries describe the flights, aircraft and currencies the
	// results refer to.
	Dictionaries dictionaries `json:"dictionaries"`
	// Errors are what the API reports went wrong.
	Errors []apiError `json:"errors"`
}

// apiError is one error the API reports.
type apiError struct {
	// Code is the API's error code, e.g. "7959" for no flights.
	Code string `json:"code"`
	// Title says what went wrong.
	Title string `json:"title"`
	// Detail says more about it.
	Detail string `json:"detail"`
}

// dictionaries are the lookup tables of an answer.
type dictionaries struct {
	// Flight describes each flight by its flight id.
	Flight map[string]flightInfo `json:"flight"`
	// Aircraft names each aircraft type by its code.
	Aircraft map[string]string `json:"aircraft"`
	// Currency describes each currency by its ISO code.
	Currency map[string]struct {
		// DecimalPlaces is how many minor-unit digits amounts carry.
		DecimalPlaces *int `json:"decimalPlaces"`
	} `json:"currency"`
}

// flightInfo is one flight as the flight dictionary describes it.
type flightInfo struct {
	// MarketingAirlineCode is the IATA code of the airline selling it.
	MarketingAirlineCode string `json:"marketingAirlineCode"`
	// MarketingFlightNumber is the number it is sold under.
	MarketingFlightNumber string `json:"marketingFlightNumber"`
	// OperatingAirlineCode is the IATA code of the airline flying it.
	OperatingAirlineCode string `json:"operatingAirlineCode"`
	// OperatingAirlineName names the airline flying it when it has no
	// code.
	OperatingAirlineName string `json:"operatingAirlineName"`
	// AircraftCode is the aircraft type code, e.g. "789".
	AircraftCode string `json:"aircraftCode"`
	// Departure is where and when it leaves.
	Departure endpoint `json:"departure"`
	// Arrival is where and when it lands.
	Arrival endpoint `json:"arrival"`
	// Duration is the time in the air in seconds.
	Duration int `json:"duration"`
}

// endpoint is an airport and a local time with its offset.
type endpoint struct {
	// LocationCode is the IATA airport code.
	LocationCode string `json:"locationCode"`
	// DateTime is the local time, e.g. "2026-10-19T12:35:00.000-07:00".
	DateTime string `json:"dateTime"`
}

// boundGroup is one itinerary and its fares.
type boundGroup struct {
	// BoundDetails describes the itinerary.
	BoundDetails struct {
		// Duration is the door-to-door time in seconds.
		Duration int `json:"duration"`
		// Segments are its flights, in order.
		Segments []boundSegment `json:"segments"`
	} `json:"boundDetails"`
	// AirBounds are its fares, one per fare family.
	AirBounds []airBound `json:"airBounds"`
}

// boundSegment is one flight of an itinerary.
type boundSegment struct {
	// FlightID identifies the flight, e.g. "SEG-AC3-YVRNRT-2026-10-19-1235".
	FlightID string `json:"flightId"`
	// ConnectionTime is the connection time after it in seconds.
	ConnectionTime int `json:"connectionTime"`
}

// airBound is an itinerary's fare in one fare family.
type airBound struct {
	// FareFamilyCode names the fare family, e.g. "STANDARD".
	FareFamilyCode string `json:"fareFamilyCode"`
	// AvailabilityDetails say how each segment is booked, in order.
	AvailabilityDetails []availability `json:"availabilityDetails"`
	// Prices are the fare's price for every passenger.
	Prices prices `json:"prices"`
	// IsCheapestOffer is set for the cheapest fare of the itinerary.
	IsCheapestOffer bool `json:"isCheapestOffer"`
}

// availability is how one segment of a fare is booked.
type availability struct {
	// FlightID is the segment's flight id.
	FlightID string `json:"flightId"`
	// Cabin is the cabin: eco, ecoPremium, premium, business or first.
	Cabin string `json:"cabin"`
	// BookingClass is the fare class letter.
	BookingClass string `json:"bookingClass"`
	// Quota is how many seats are left, nil when not said.
	Quota *int `json:"quota"`
	// MileagePercentage is the share of the distance flown in Cabin.
	MileagePercentage *int `json:"mileagePercentage"`
}

// prices are a fare's points and cash prices.
type prices struct {
	// MilesConversion is the price for every passenger.
	MilesConversion milesConversion `json:"milesConversion"`
	// ConvertedMiles is the points price for every passenger, as some
	// answers put it.
	ConvertedMiles *miles `json:"convertedMiles"`
	// UnitPrices are the prices per passenger type.
	UnitPrices []struct {
		// TravelerIDs are the passengers this price is for.
		TravelerIDs []string `json:"travelerIds"`
		// MilesConversion is the price for one such passenger.
		MilesConversion milesConversion `json:"milesConversion"`
	} `json:"unitPrices"`
}

// milesConversion is a price split into points and the cash left over.
type milesConversion struct {
	// ConvertedMiles is the points.
	ConvertedMiles *miles `json:"convertedMiles"`
	// RemainingNonConverted is the cash, in minor units.
	RemainingNonConverted *struct {
		// Total is the cash in the currency's minor unit.
		Total int64 `json:"total"`
		// CurrencyCode is the ISO 4217 code.
		CurrencyCode string `json:"currencyCode"`
	} `json:"remainingNonConverted"`
}

// miles is a points amount.
type miles struct {
	// Base is the points price.
	Base int `json:"base"`
	// Total is the points price including any points surcharge.
	Total int `json:"total"`
}

// pickAnswer finds the air-bounds answer among the API bodies the page
// loaded, preferring the last. It returns false when none of them is one.
// An answer reporting no flights is found with no groups. It returns an
// error when the API reports any other failure and no answer has results.
func pickAnswer(bodies [][]byte) (answer, bool, error) {
	var failure error
	empty := false
	for i := len(bodies) - 1; i >= 0; i-- {
		var a answer
		if json.Unmarshal(bodies[i], &a) != nil {
			continue
		}
		if len(a.Data.AirBoundGroups) > 0 {
			return a, true, nil
		}
		for _, e := range a.Errors {
			if e.Code == noFlights {
				empty = true
			} else if failure == nil && isSearchError(bodies[i]) {
				failure = fmt.Errorf("aeroplan: API error %s: %s",
					e.Code, strings.TrimSpace(e.Title+" "+e.Detail))
			}
		}
		if isSearchAnswer(bodies[i]) {
			empty = true
		}
	}
	switch {
	case empty:
		return answer{}, true, nil
	case failure != nil:
		return answer{}, false, failure
	}
	return answer{}, false, nil
}

// isSearchAnswer reports whether body is an air-bounds answer, empty or
// not, by its carrying an airBoundGroups list.
func isSearchAnswer(body []byte) bool {
	var probe struct {
		Data struct {
			AirBoundGroups *json.RawMessage `json:"airBoundGroups"`
		} `json:"data"`
	}
	return json.Unmarshal(body, &probe) == nil &&
		probe.Data.AirBoundGroups != nil
}

// isSearchError reports whether body is an error answer of the search
// rather than of one of the page's other API calls, by its naming no
// session token and carrying errors.
func isSearchError(body []byte) bool {
	var probe struct {
		Data struct {
			SessionToken string `json:"sessionToken"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	return json.Unmarshal(body, &probe) == nil &&
		probe.Data.SessionToken == "" && len(probe.Errors) > 0
}

// offers turns every fare of every itinerary in a into an offer for
// passengers, booked at booking.
func offers(a answer, passengers int, booking string) []award.Offer {
	out := []award.Offer{}
	passengers = max(passengers, 1)
	for _, g := range a.Data.AirBoundGroups {
		segs, ok := segments(g, a.Dictionaries)
		if !ok {
			continue
		}
		for _, b := range g.AirBounds {
			o, ok := offer(g, segs, b, a.Dictionaries, passengers, booking)
			if ok {
				out = append(out, o)
			}
		}
	}
	return out
}

// segments builds the flights of itinerary g from the dictionaries d, with
// no cabin or booking class yet. It returns false when a flight can be
// read neither from the dictionary nor from its flight id.
func segments(g boundGroup, d dictionaries) ([]award.Segment, bool) {
	defs := g.BoundDetails.Segments
	if len(defs) == 0 {
		return nil, false
	}
	out := make([]award.Segment, len(defs))
	for i, s := range defs {
		seg, ok := dictionarySegment(s.FlightID, d)
		if !ok {
			seg, ok = idSegment(s.FlightID, g.BoundDetails.Duration, len(defs))
		}
		if !ok {
			return nil, false
		}
		if i < len(defs)-1 {
			seg.LayoverMinutes = s.ConnectionTime / 60
		}
		out[i] = seg
	}
	return out, true
}

// dictionarySegment builds the flight id from the flight dictionary of d.
// It returns false when the flight is missing or its times cannot be read.
func dictionarySegment(id string, d dictionaries) (award.Segment, bool) {
	f, ok := d.Flight[id]
	if !ok {
		return award.Segment{}, false
	}
	dep, err1 := parseTime(f.Departure.DateTime)
	arr, err2 := parseTime(f.Arrival.DateTime)
	if err1 != nil || err2 != nil {
		return award.Segment{}, false
	}
	op := f.OperatingAirlineCode
	if op == "" {
		op = f.MarketingAirlineCode
	}
	aircraft := f.AircraftCode
	if name, ok := d.Aircraft[aircraft]; ok && name != "" {
		aircraft = name
	}
	minutes := f.Duration / 60
	if minutes == 0 {
		minutes = int(arr.Sub(dep).Minutes())
	}
	return award.Segment{
		Flight:           f.MarketingAirlineCode + f.MarketingFlightNumber,
		MarketingCarrier: f.MarketingAirlineCode,
		OperatingCarrier: op,
		Origin:           f.Departure.LocationCode,
		Destination:      f.Arrival.LocationCode,
		Departure:        dep,
		Arrival:          arr,
		DurationMinutes:  minutes,
		Aircraft:         aircraft,
	}, true
}

// parseTime reads a dictionary time such as "2026-10-19T12:35:00.000-07:00".
// It returns an error when s is not such a time.
func parseTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339, s)
}

// flightIDPattern matches a flight id such as
// "SEG-AC3-YVRNRT-2026-10-19-1235".
var flightIDPattern = regexp.MustCompile(
	`^SEG-([A-Z0-9]{2})(\d+[A-Z]?)-([A-Z]{3})([A-Z]{3})-` +
		`(\d{4}-\d{2}-\d{2})-(\d{2})(\d{2})$`,
)

// idSegment builds a flight from its flight id alone, for a flight missing
// from the dictionary. The id has the local departure without an offset, so
// the departure is given in UTC, and the arrival is estimated as an equal
// share of total, the itinerary's duration in seconds, over count flights.
// It returns false when id is not a flight id.
func idSegment(id string, total, count int) (award.Segment, bool) {
	m := flightIDPattern.FindStringSubmatch(id)
	if m == nil {
		return award.Segment{}, false
	}
	dep, err := time.Parse(localLayout, m[5]+"T"+m[6]+":"+m[7])
	if err != nil {
		return award.Segment{}, false
	}
	share := total / max(count, 1)
	return award.Segment{
		Flight:           m[1] + m[2],
		MarketingCarrier: m[1],
		OperatingCarrier: m[1],
		Origin:           m[3],
		Destination:      m[4],
		Departure:        dep,
		Arrival:          dep.Add(time.Duration(share) * time.Second),
		DurationMinutes:  share / 60,
	}, true
}

// cabins maps the API's cabin names to cabins.
var cabins = map[string]award.Cabin{
	"eco":        award.Economy,
	"economy":    award.Economy,
	"ecoPremium": award.Premium,
	"premium":    award.Premium,
	"business":   award.Business,
	"first":      award.First,
}

// cabinOf maps the API's cabin name to a cabin, economy when unknown.
func cabinOf(name string) award.Cabin {
	if c, ok := cabins[name]; ok {
		return c
	}
	if c, ok := award.ParseCabin(name); ok {
		return c
	}
	return award.Economy
}

// rank orders cabins from economy up.
var rank = map[award.Cabin]int{
	award.Economy:  0,
	award.Premium:  1,
	award.Business: 2,
	award.First:    3,
}

// offer builds the offer for fare b of itinerary g, whose flights are segs,
// for passengers and booked at booking, using the dictionaries d. The
// offer's cabin is the highest cabin any segment is booked in, as Aeroplan
// files a mixed-cabin fare. It returns false when b has no points price.
func offer(
	g boundGroup,
	segs []award.Segment,
	b airBound,
	d dictionaries,
	passengers int,
	booking string,
) (award.Offer, bool) {
	points, taxes, ok := farePrice(b.Prices, d, passengers)
	if !ok {
		return award.Offer{}, false
	}
	booked := slices.Clone(segs)
	byID := map[string]availability{}
	for _, a := range b.AvailabilityDetails {
		byID[a.FlightID] = a
	}
	var seats *int
	cabin := award.Economy
	for i, s := range g.BoundDetails.Segments {
		a, ok := byID[s.FlightID]
		if !ok && i < len(b.AvailabilityDetails) {
			a = b.AvailabilityDetails[i]
		}
		booked[i].Cabin = cabinOf(a.Cabin)
		booked[i].BookingCode = a.BookingClass
		if rank[booked[i].Cabin] > rank[cabin] {
			cabin = booked[i].Cabin
		}
		if a.Quota != nil && (seats == nil || *a.Quota < *seats) {
			n := *a.Quota
			seats = &n
		}
	}
	mixed := false
	var airlines []string
	for _, s := range booked {
		if s.Cabin != cabin {
			mixed = true
		}
		if !slices.Contains(airlines, s.OperatingCarrier) {
			airlines = append(airlines, s.OperatingCarrier)
		}
	}
	last := len(booked) - 1
	duration := g.BoundDetails.Duration / 60
	if duration == 0 {
		duration = int(booked[last].Arrival.Sub(booked[0].Departure).Minutes())
	}
	return award.Offer{
		Program:         Key,
		Availability:    award.Confirmed,
		Origin:          booked[0].Origin,
		Destination:     booked[last].Destination,
		Departure:       booked[0].Departure,
		Arrival:         booked[last].Arrival,
		DurationMinutes: duration,
		Stops:           last,
		Airlines:        airlines,
		Cabin:           cabin,
		MixedCabin:      mixed,
		Fare:            b.FareFamilyCode,
		Price: award.Price{
			PointsPerPassenger: points,
			PointsTotal:        points * passengers,
			TaxesPerPassenger:  taxes,
			TaxesTotal: award.Money{
				Amount:   roundCents(taxes.Amount * float64(passengers)),
				Currency: taxes.Currency,
			},
		},
		Seats:      seats,
		BookingURL: booking,
		Notes:      notes(booked, b, cabin),
		Segments:   booked,
	}, true
}

// farePrice reads the points and taxes per passenger of p, preferring the
// price of one passenger and else dividing the price of all passengers.
// Cash amounts are converted from minor units with the currency's decimal
// places in d. It returns false when p has no points price.
func farePrice(
	p prices,
	d dictionaries,
	passengers int,
) (int, award.Money, bool) {
	conv := p.MilesConversion
	divide := passengers
	if len(p.UnitPrices) > 0 &&
		p.UnitPrices[0].MilesConversion.ConvertedMiles != nil {
		conv = p.UnitPrices[0].MilesConversion
		divide = 1
	}
	m := conv.ConvertedMiles
	if m == nil {
		m = p.ConvertedMiles
		divide = passengers
	}
	if m == nil {
		return 0, award.Money{}, false
	}
	points := m.Base
	if m.Total > points {
		points = m.Total
	}
	if points <= 0 {
		return 0, award.Money{}, false
	}
	points /= divide
	taxes := award.Money{Currency: "CAD"}
	if r := conv.RemainingNonConverted; r != nil {
		code := r.CurrencyCode
		if code == "" {
			code = "CAD"
		}
		places := 2
		if c, ok := d.Currency[code]; ok && c.DecimalPlaces != nil {
			places = *c.DecimalPlaces
		}
		amount := float64(r.Total) / math.Pow10(places) / float64(divide)
		taxes = award.Money{Amount: roundCents(amount), Currency: code}
	}
	return points, taxes, true
}

// roundCents rounds v to two decimals.
func roundCents(v float64) float64 {
	return math.Round(v*100) / 100
}

// notes are what the answer says about a fare beyond its price: partner
// flights, the share of a mixed-cabin fare flown in its cabin and whether
// it is the itinerary's cheapest fare.
func notes(segs []award.Segment, b airBound, cabin award.Cabin) []string {
	var out []string
	for _, s := range segs {
		if s.OperatingCarrier != s.MarketingCarrier {
			out = append(out, s.Flight+" operated by "+s.OperatingCarrier)
		}
	}
	for _, a := range b.AvailabilityDetails {
		if a.MileagePercentage != nil && *a.MileagePercentage < 100 &&
			cabinOf(a.Cabin) == cabin {
			out = append(out, fmt.Sprintf("%d%% of the distance in %s",
				*a.MileagePercentage, cabin))
			break
		}
	}
	if b.IsCheapestOffer {
		out = append(out, "Cheapest fare of this itinerary")
	}
	return out
}
