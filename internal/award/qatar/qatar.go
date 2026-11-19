// Package qatar searches Qatar Airways Privilege Club award availability,
// priced in Avios.
//
// qatarairways.com sits behind Akamai Bot Manager and only shows award
// flights to a signed-in Privilege Club member. The stealth browser
// therefore opens the site's own award results page for the route and
// day, carrying a saved session's cookies, and captures the JSON answer
// of the award-flight-offers API the page's Angular application calls.
// When the page asks to log in, Search signs in on the site's sign-in
// page, which emails the member a one-time pin; without a person to give
// it, the session must be refreshed with SignIn. See session.go for how
// the session is kept.
//
// The answer lists flight offers, one per itinerary, each with its fare
// offers, one per cabin that still has seats. Every fare offer becomes
// one offer. Times in the answer are local without offsets; they are
// placed with the airports' time zones, see zones.go. The results page
// shows no taxes; they are only priced once a fare is put in the cart,
// which needs the Avios to pay for it, so offers carry no taxes.
package qatar

import (
	"bytes"
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

// Key is the program key Qatar Airways offers carry.
const Key = "qatar"

// resultsURL is the award results page a member searches and books from.
const resultsURL = "https://www.qatarairways.com/app/booking/redemption"

// homeURL is the site's home page, opened before the results page so the
// bot protection's sensor script earns its cookies on an ordinary visit.
const homeURL = "https://www.qatarairways.com/en/homepage.html"

// loginURL is the Privilege Club sign-in page.
const loginURL = "https://www.qatarairways.com/en/Privilege-Club/" +
	"loginpage.html"

// loginPath is the part of the sign-in page's URL that marks it.
const loginPath = "/loginpage.html"

// offersPath is the part of the award search API's URL that marks it.
const offersPath = "/flight-search/award-flight-offers"

// loadTimeout bounds the navigation to the results page.
const loadTimeout = 60 * time.Second

// settleRounds and settleDelay are how long the results page is given,
// once it has called the award search API, to receive its answer.
const (
	settleRounds = 3
	settleDelay  = 3 * time.Second
)

// taxCurrency is the currency the site prices award taxes in for a
// search from its hub, given with the zero taxes offers carry.
const taxCurrency = "QAR"

// errBlocked is returned when qatarairways.com answers with Akamai's
// denial instead of the results page.
var errBlocked = errors.New("qatar: access denied by the site's bot " +
	"protection")

// Program searches qatarairways.com through a stealth browser, signed in
// as one Privilege Club member.
type Program struct {
	browser *browser.Browser
	account Account
	session *session
}

// New builds a Program that loads qatarairways.com in b, signed in as
// account, keeping its session in account.SessionFile.
func New(b *browser.Browser, account Account) *Program {
	return &Program{
		browser: b,
		account: account,
		session: &session{path: account.sessionPath()},
	}
}

// Key returns the program key, "qatar".
func (p *Program) Key() string {
	return Key
}

// Search returns Qatar Airways' award offers for q, one per cabin of
// every itinerary. It returns an error when the browser cannot load the
// results page, the page is denied by the bot protection, the member
// cannot be signed in without help (errSignInNeeded, returned at once
// from then on until the session file changes), or the API answers with
// an error. A day or route without award seats gives no offers and no
// error.
func (p *Program) Search(
	ctx context.Context,
	q award.Query,
) ([]award.Offer, error) {
	if p.session.isStuck() {
		return nil, errSignInNeeded
	}
	booking := bookingURL(q)
	captured, err := p.browser.Collect(ctx, booking, browser.CollectOpts{
		Timeout:     loadTimeout,
		WarmUp:      homeURL,
		Cookies:     p.session.load(),
		Scrolls:     settleRounds,
		ScrollDelay: settleDelay,
		Accept:      func(string, string) bool { return false },
		CaptureBody: func(u, ct string) bool {
			return strings.Contains(u, offersPath)
		},
		Interact: func(ctx context.Context, page *browser.Page) error {
			return signInIfAsked(ctx, page, p.account, booking, nil)
		},
	})
	if err != nil {
		if !errors.Is(err, context.Canceled) &&
			!errors.Is(err, context.DeadlineExceeded) &&
			!strings.HasPrefix(err.Error(), "browser:") {
			p.session.markStuck()
		}
		return nil, err
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
		if strings.Contains(captured.HTML, "Access Denied") {
			return nil, errBlocked
		}
		if noFlightsShown(captured.HTML) {
			_ = p.session.save(captured.Cookies)
			return []award.Offer{}, nil
		}
		return nil, errors.New("qatar: no award answer captured")
	}
	_ = p.session.save(captured.Cookies)
	return offers(a, q.Passengers, booking), nil
}

// bookingURL is the award results page for a one-way search for q.
func bookingURL(q award.Query) string {
	v := url.Values{}
	v.Set("widget", "QR")
	v.Set("searchType", "F")
	v.Set("addTaxToFare", "Y")
	v.Set("minPurTime", "0")
	v.Set("selLang", "en")
	v.Set("tripType", "O")
	v.Set("fromStation", q.Origin)
	v.Set("toStation", q.Destination)
	v.Set("departing", q.Date.Format(time.DateOnly))
	v.Set("bookingClass", "E")
	v.Set("adults", strconv.Itoa(max(q.Passengers, 1)))
	v.Set("children", "0")
	v.Set("infants", "0")
	v.Set("ofw", "0")
	v.Set("teenager", "0")
	v.Set("flexibleDate", "off")
	v.Set("qmilesFlow", "true")
	v.Set("allowRedemption", "Y")
	return resultsURL + "?" + v.Encode()
}

// noFlightsShown reports whether the results page html says there are
// no flights for the search.
func noFlightsShown(html string) bool {
	h := strings.ToLower(html)
	return strings.Contains(h, "no flights") ||
		strings.Contains(h, "no results") ||
		strings.Contains(h, "0 results")
}

// answer is the part of an award-flight-offers answer that is read.
type answer struct {
	// FlightOffers are the itineraries with their fares; nil when the
	// answer has none at all.
	FlightOffers []flightOffer `json:"flightOffers"`
	// Errors are what the API reports went wrong.
	Errors []apiError `json:"errors"`
}

// apiError is one error the API reports.
type apiError struct {
	// Code is the API's error code.
	Code string `json:"code"`
	// Title says what went wrong.
	Title string `json:"title"`
	// Detail says more about it.
	Detail string `json:"detail"`
}

// flightOffer is one itinerary and its fares.
type flightOffer struct {
	// Duration is the door-to-door time in seconds.
	Duration int `json:"duration"`
	// NumberOfStops is how many connections it makes.
	NumberOfStops int `json:"numberOfStops"`
	// Segments are its flights, in order.
	Segments []segment `json:"segments"`
	// FareOffers are its fares, one per cabin.
	FareOffers []fareOffer `json:"fareOffers"`
}

// station names an airport.
type station struct {
	// IataCode is the IATA airport code.
	IataCode string `json:"iataCode"`
}

// segment is one flight of an itinerary.
type segment struct {
	// FlightID identifies the flight within the answer.
	FlightID string `json:"flightId"`
	// FlightNumber is the marketing carrier and number, e.g. "QR703".
	FlightNumber string `json:"flightNumber"`
	// Vehicle is the aircraft.
	Vehicle struct {
		// Code is the aircraft type code, e.g. "77W".
		Code string `json:"code"`
		// Name is the aircraft's name, e.g. "Boeing 777-300er".
		Name string `json:"name"`
	} `json:"vehicle"`
	// Departure is where and when it leaves.
	Departure struct {
		// Origin is the airport.
		Origin station `json:"origin"`
		// DateTime is the local time without offset.
		DateTime string `json:"dateTime"`
	} `json:"departure"`
	// Arrival is where and when it lands.
	Arrival struct {
		// Destination is the airport.
		Destination station `json:"destination"`
		// DateTime is the local time without offset.
		DateTime string `json:"dateTime"`
	} `json:"arrival"`
	// Duration is the time in the air in seconds.
	Duration int `json:"duration"`
	// OperatorLogo names the airline flying it.
	OperatorLogo struct {
		// OperatorCode is the operating airline's IATA code.
		OperatorCode string `json:"operatorCode"`
	} `json:"operatorLogo"`
	// OperatingAirlineName is the operating airline's name.
	OperatingAirlineName string `json:"operatingAirlineName"`
}

// fareOffer is an itinerary's fare in one cabin.
type fareOffer struct {
	// AvailableSeats is how many seats are left, nil when not said.
	AvailableSeats *int `json:"availableSeats"`
	// Price is the fare's Avios price for every passenger.
	Price struct {
		// Base is the Avios.
		Base float64 `json:"base"`
		// CurrencyCode is "AVIOS".
		CurrencyCode string `json:"currencyCode"`
	} `json:"price"`
	// CabinType is the cabin: ECONOMY, PREMIUM_ECONOMY, BUSINESS or
	// FIRST.
	CabinType string `json:"cabinType"`
	// CabinOfferType says whether every flight is in CabinType, "SINGLE",
	// or not.
	CabinOfferType string `json:"cabinOfferType"`
	// FareInformation says how each flight is booked.
	FareInformation []fareInfo `json:"fareInformation"`
	// IsFlexiFare is set for a Flexi award rather than a Classic one.
	IsFlexiFare bool `json:"isFlexiFare"`
	// PrivilegePickIndicator is set for a discounted Privilege Pick.
	PrivilegePickIndicator bool `json:"privilegePickIndicator"`
	// OffPeakIndicator is set for an off-peak price.
	OffPeakIndicator bool `json:"offPeakIndicator"`
}

// fareInfo is how one flight of a fare is booked.
type fareInfo struct {
	// FlightID is the flight's id.
	FlightID string `json:"flightId"`
	// CabinType is the flight's cabin when the fare mixes cabins.
	CabinType string `json:"cabinType"`
	// BookingClass is the fare class letter when given.
	BookingClass string `json:"bookingClass"`
}

// pickAnswer finds the award answer among the bodies the page loaded,
// preferring the last. It returns false when none of them is one. It
// returns an error when the API reports a failure and no answer has
// offers; an empty body, which the API sends for a day without award
// seats, and an error saying there are no flights are empty answers.
func pickAnswer(bodies [][]byte) (answer, bool, error) {
	var failure error
	for i := len(bodies) - 1; i >= 0; i-- {
		if len(bytes.TrimSpace(bodies[i])) == 0 {
			return answer{}, true, nil
		}
		var a answer
		if json.Unmarshal(bodies[i], &a) != nil {
			continue
		}
		if a.FlightOffers != nil || isOffersAnswer(bodies[i]) {
			return a, true, nil
		}
		for _, e := range a.Errors {
			if noFlights(e) {
				return answer{}, true, nil
			}
			if failure == nil {
				msg := strings.TrimSpace(e.Title + " " + e.Detail)
				failure = fmt.Errorf("qatar: API error %s: %s",
					e.Code, clip(msg, 90))
			}
		}
	}
	return answer{}, false, failure
}

// isOffersAnswer reports whether body carries a flightOffers list, even
// an empty or null one.
func isOffersAnswer(body []byte) bool {
	var probe map[string]json.RawMessage
	if json.Unmarshal(body, &probe) != nil {
		return false
	}
	_, ok := probe["flightOffers"]
	return ok
}

// noFlights reports whether e says there are no flights or seats.
func noFlights(e apiError) bool {
	m := strings.ToLower(e.Title + " " + e.Detail)
	for _, s := range []string{
		"no flight", "no availab", "not available", "no seat",
		"no result", "no award",
	} {
		if strings.Contains(m, s) {
			return true
		}
	}
	return false
}

// offers turns every fare of every itinerary in a into an offer for
// passengers, booked at booking.
func offers(a answer, passengers int, booking string) []award.Offer {
	out := []award.Offer{}
	passengers = max(passengers, 1)
	for _, f := range a.FlightOffers {
		segs, ok := segments(f.Segments)
		if !ok {
			continue
		}
		for _, fare := range f.FareOffers {
			o, ok := offer(f, segs, fare, passengers, booking)
			if ok {
				out = append(out, o)
			}
		}
	}
	return out
}

// segments builds the flights of an itinerary from defs, with no cabin
// yet. It returns false when there are none or a time cannot be read.
func segments(defs []segment) ([]award.Segment, bool) {
	if len(defs) == 0 {
		return nil, false
	}
	out := make([]award.Segment, len(defs))
	for i, s := range defs {
		org := s.Departure.Origin.IataCode
		dst := s.Arrival.Destination.IataCode
		dep, arr, ok := placeFlight(s.Departure.DateTime,
			s.Arrival.DateTime, org, dst, s.Duration)
		if !ok {
			return nil, false
		}
		marketing := carrierOf(s.FlightNumber)
		op := s.OperatorLogo.OperatorCode
		if op == "" {
			op = marketing
		}
		aircraft := s.Vehicle.Name
		if aircraft == "" {
			aircraft = s.Vehicle.Code
		}
		minutes := s.Duration / 60
		if minutes == 0 {
			minutes = int(arr.Sub(dep).Minutes())
		}
		out[i] = award.Segment{
			Flight:           s.FlightNumber,
			MarketingCarrier: marketing,
			OperatingCarrier: op,
			Origin:           org,
			Destination:      dst,
			Departure:        dep,
			Arrival:          arr,
			DurationMinutes:  minutes,
			Aircraft:         aircraft,
		}
	}
	for i := range len(out) - 1 {
		gap := out[i+1].Departure.Sub(out[i].Arrival)
		out[i].LayoverMinutes = max(int(gap.Minutes()), 0)
	}
	return out, true
}

// carrierOf is the airline code of a flight number such as "QR703".
func carrierOf(flight string) string {
	if len(flight) < 2 {
		return flight
	}
	return flight[:2]
}

// cabins maps the API's cabin names to cabins.
var cabins = map[string]award.Cabin{
	"ECONOMY":         award.Economy,
	"PREMIUM_ECONOMY": award.Premium,
	"PREMIUM":         award.Premium,
	"BUSINESS":        award.Business,
	"FIRST":           award.First,
}

// cabinOf maps the API's cabin name to a cabin, and false when it is not
// known.
func cabinOf(name string) (award.Cabin, bool) {
	if c, ok := cabins[strings.ToUpper(name)]; ok {
		return c, true
	}
	return award.ParseCabin(name)
}

// offer builds the offer for fare of itinerary f, whose flights are
// segs, for passengers and booked at booking. The Avios price the answer
// gives is for every passenger. It returns false when the fare has no
// Avios price or an unknown cabin.
func offer(
	f flightOffer,
	segs []award.Segment,
	fare fareOffer,
	passengers int,
	booking string,
) (award.Offer, bool) {
	cabin, ok := cabinOf(fare.CabinType)
	if !ok || fare.Price.Base <= 0 {
		return award.Offer{}, false
	}
	total := int(fare.Price.Base)
	points := total / passengers
	booked := slices.Clone(segs)
	byID := map[string]fareInfo{}
	for _, fi := range fare.FareInformation {
		byID[fi.FlightID] = fi
	}
	mixed := false
	var airlines []string
	for i := range booked {
		booked[i].Cabin = cabin
		fi := byID[f.Segments[i].FlightID]
		if c, ok := cabinOf(fi.CabinType); ok {
			booked[i].Cabin = c
		}
		booked[i].BookingCode = fi.BookingClass
		if booked[i].Cabin != cabin {
			mixed = true
		}
		if !slices.Contains(airlines, booked[i].OperatingCarrier) {
			airlines = append(airlines, booked[i].OperatingCarrier)
		}
	}
	if fare.CabinOfferType != "" && fare.CabinOfferType != "SINGLE" {
		mixed = true
	}
	last := len(booked) - 1
	duration := f.Duration / 60
	if duration == 0 {
		duration = int(booked[last].Arrival.Sub(booked[0].Departure).
			Minutes())
	}
	var seats *int
	if fare.AvailableSeats != nil {
		n := *fare.AvailableSeats
		seats = &n
	}
	noTax := award.Money{Currency: taxCurrency}
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
		Fare:            fareName(fare),
		Price: award.Price{
			PointsPerPassenger: points,
			PointsTotal:        total,
			TaxesPerPassenger:  noTax,
			TaxesTotal:         noTax,
		},
		Seats:      seats,
		BookingURL: booking,
		Notes:      notes(booked, f.Segments, fare),
		Segments:   booked,
	}, true
}

// fareName is the award type of fare: "FLEXI" or "CLASSIC".
func fareName(fare fareOffer) string {
	if fare.IsFlexiFare {
		return "FLEXI"
	}
	return "CLASSIC"
}

// notes are what the answer says about a fare beyond its price: partner
// flights, Privilege Picks and off-peak prices, and that taxes are only
// priced at checkout.
func notes(segs []award.Segment, defs []segment, fare fareOffer) []string {
	var out []string
	for i, s := range segs {
		if s.OperatingCarrier != s.MarketingCarrier {
			name := defs[i].OperatingAirlineName
			if name == "" {
				name = s.OperatingCarrier
			}
			out = append(out, s.Flight+" operated by "+name)
		}
	}
	if fare.PrivilegePickIndicator {
		out = append(out, "Privilege Pick")
	}
	if fare.OffPeakIndicator {
		out = append(out, "Off-peak price")
	}
	return append(out, "Taxes and surcharges are priced at checkout")
}
