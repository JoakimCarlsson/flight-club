// Package emirates searches Emirates Skywards Classic Rewards, priced in
// Skywards Miles plus taxes, fees and carrier charges in cash.
//
// emirates.com sits behind Akamai Bot Manager and only shows Classic
// Rewards to a signed-in Skywards member: anyone else is sent from the
// results page back to the booking page. Search therefore opens the
// site's own results page for the route and day in the stealth browser,
// carrying a saved session's cookies, and signs the member in when the
// site asks; see session.go for how the session is kept. The page's
// application then calls the site's branded-fares API itself, and that
// JSON answer is captured as the page loads it.
//
// The answer lists flight options, each with its flights and, per cabin,
// the fare brands that still have seats. Every available brand becomes
// one offer. The answer's times have no offset; the airports' zones come
// from the location taxonomy the page loads alongside it.
package emirates

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/joakimcarlsson/flight-club/internal/award"
	"github.com/joakimcarlsson/flight-club/internal/browser"
)

// Key is the program key Emirates offers carry.
const Key = "emirates"

// resultsURL is the results page a member searches and books from.
const resultsURL = "https://www.emirates.com/booking/search-results/"

// faresPath is the path of the API answering the search.
const faresPath = "/service/search-results/branded-fares"

// zonesPath is the path of the location taxonomy giving airport zones.
const zonesPath = "/service/taxonomy/locations"

// loadTimeout bounds the navigation to the results page.
const loadTimeout = 90 * time.Second

// settleRounds and settleDelay are how long the page is given, once the
// search has been answered, for the answer's body to be read.
const (
	settleRounds = 1
	settleDelay  = 3 * time.Second
)

// errBlocked is returned when emirates.com answers with Akamai's denial
// instead of the results page.
var errBlocked = errors.New(
	"emirates: access denied by the site's bot protection",
)

// Program searches emirates.com through a stealth browser, signed in as
// one Skywards member.
type Program struct {
	browser *browser.Browser
	account Account
	session *session
	zones   *zones
}

// New builds a Program that loads emirates.com in b, signed in as a,
// keeping its session in a.SessionFile.
func New(b *browser.Browser, a Account) *Program {
	return &Program{
		browser: b,
		account: a,
		session: &session{path: a.sessionPath()},
		zones:   &zones{},
	}
}

// Key returns the program key, "emirates".
func (p *Program) Key() string {
	return Key
}

// Search returns the Classic Rewards offers for q, one per available fare
// brand of every cabin of every flight option. It returns an error when
// the browser cannot load the results page, the page is denied by the
// bot protection, the member cannot be signed in without help
// (errSignInNeeded, returned at once from then on until the session file
// changes), or the answer cannot be read. A day without award space gives
// no offers and no error.
func (p *Program) Search(
	ctx context.Context,
	q award.Query,
) ([]award.Offer, error) {
	if p.session.isStuck() {
		return nil, errSignInNeeded
	}
	booking := bookingURL(q)
	captured, err := p.collect(ctx, booking, nil)
	if errors.Is(err, errSignInNeeded) || errors.Is(err, errSignInRefused) {
		p.session.markStuck()
	}
	if err != nil {
		return nil, err
	}
	var answerBody []byte
	for _, b := range captured.Bodies {
		switch {
		case strings.Contains(b.URL, zonesPath):
			p.zones.learn(b.Data)
		case strings.Contains(b.URL, faresPath):
			answerBody = b.Data
		}
	}
	if answerBody == nil {
		return nil, missingAnswer(captured)
	}
	var a answer
	if err := json.Unmarshal(answerBody, &a); err != nil {
		return nil, errors.New("emirates: unreadable fares answer")
	}
	_ = p.session.save(captured.Cookies)
	if len(a.Bounds) == 0 && a.Message != "" && !noSpace(a) {
		return nil, fmt.Errorf("emirates: search failed: %.80s", a.Message)
	}
	return offers(a, p.zones, max(q.Passengers, 1), booking), nil
}

// noSpace reports whether a is the API's answer for a day without award
// space, such as "No available solution" (SRP_102).
func noSpace(a answer) bool {
	return a.MessageCode == "SRP_102" ||
		strings.Contains(strings.ToLower(a.Message), "no available")
}

// collect opens page in the browser with the saved session, signing in
// when the site asks with code answering any emailed code, and captures
// the fares answer and, until the zones are known, the location taxonomy.
// It returns what the browser's Collect returns.
func (p *Program) collect(
	ctx context.Context,
	page string,
	code CodeFunc,
) (browser.Captured, error) {
	wantZones := !p.zones.known()
	var answered atomic.Bool
	w := watch{
		account:  p.account,
		code:     code,
		results:  page,
		answered: answered.Load,
	}
	return p.browser.Collect(ctx, page, browser.CollectOpts{
		Timeout:     loadTimeout,
		Cookies:     p.session.load(),
		Scrolls:     settleRounds,
		ScrollDelay: settleDelay,
		Accept:      func(string, string) bool { return false },
		CaptureBody: func(u, ct string) bool {
			if !strings.Contains(strings.ToLower(ct), "json") {
				return false
			}
			if strings.Contains(u, faresPath) {
				answered.Store(true)
				return true
			}
			return wantZones && strings.Contains(u, zonesPath)
		},
		Interact: func(ctx context.Context, pg *browser.Page) error {
			return signInIfAsked(ctx, pg, w)
		},
	})
}

// missingAnswer explains why captured holds no fares answer: the member
// is not signed in, the bot protection denied the page, or the search
// simply never answered.
func missingAnswer(captured browser.Captured) error {
	switch {
	case signedOutURL(captured.URL) || onAccountsURL(captured.URL):
		return errSignInNeeded
	case strings.Contains(captured.HTML, "Access Denied"):
		return errBlocked
	}
	return errors.New("emirates: no fares answer captured")
}

// searchRequest is the search the results page reads from its URL.
type searchRequest struct {
	JourneyType string          `json:"journeyType"`
	BookingType string          `json:"bookingType"`
	Passengers  []passengerType `json:"passengers"`
	Segments    []searchLeg     `json:"segments"`
	Referrer    string          `json:"referrer"`
}

// passengerType is how many passengers of one type travel.
type passengerType struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

// searchLeg is one leg of a search.
type searchLeg struct {
	Departure  string `json:"departure"`
	Arrival    string `json:"arrival"`
	TravelDate string `json:"travelDate"`
	CabinClass string `json:"cabinClass"`
}

// bookingURL is the results page of a one-way Classic Rewards search for
// q, whose answer covers every cabin.
func bookingURL(q award.Query) string {
	req := searchRequest{
		JourneyType: "ONEWAY",
		BookingType: "REDEMPTION",
		Passengers: []passengerType{
			{Type: "ADT", Count: max(q.Passengers, 1)},
		},
		Segments: []searchLeg{{
			Departure:  q.Origin,
			Arrival:    q.Destination,
			TravelDate: q.Date.Format(time.DateOnly),
			CabinClass: "Y",
		}},
		Referrer: "/book",
	}
	data, _ := json.Marshal(req)
	v := url.Values{}
	v.Set("searchRequest", base64.StdEncoding.EncodeToString(data))
	return resultsURL + "?" + v.Encode()
}

// answer is the part of a branded-fares answer that is read.
type answer struct {
	// Message says why the search failed, when it did.
	Message string `json:"message"`
	// MessageCode is the site's code for Message, e.g. "SRP_102".
	MessageCode string `json:"messageCode"`
	// Currency is the currency the cash amounts are in.
	Currency struct {
		// Priced is the currency the fares are priced in.
		Priced struct {
			// Code is the ISO 4217 code.
			Code string `json:"code"`
		} `json:"priced"`
	} `json:"currency"`
	// Bounds are the legs searched, the first the outbound.
	Bounds []struct {
		// Options are the flight options of the leg.
		Options []option `json:"options"`
	} `json:"bounds"`
}

// option is one itinerary and its fares.
type option struct {
	// ID identifies it, e.g. "DXB_LHR_EK007_1750644336".
	ID string `json:"id"`
	// OndDuration is the door-to-door time, e.g. "8H0M".
	OndDuration string `json:"ondDuration"`
	// AirSegments are its flights, in order.
	AirSegments []airSegment `json:"airSegments"`
	// Cabins are its fares per cabin.
	Cabins []cabinFares `json:"cabins"`
}

// airSegment is one flight of an option.
type airSegment struct {
	ID                   string `json:"id"`
	FlightNumber         string `json:"flightNumber"`
	CarrierCode          string `json:"carrierCode"`
	MarketingCarrierCode string `json:"marketingCarrierCode"`
	OperatingCarrierCode string `json:"operatingCarrierCode"`
	Departure            string `json:"departure"`
	Arrival              string `json:"arrival"`
	DepartureDateTime    string `json:"departureDateTime"`
	ArrivalDateTime      string `json:"arrivalDateTime"`
	AircraftType         string `json:"aircraftType"`
	Duration             string `json:"duration"`
	Stops                int    `json:"stops"`
}

// cabinFares are an option's fare brands in one cabin.
type cabinFares struct {
	// CabinClass is Y, W, J or F.
	CabinClass string `json:"cabinClass"`
	// Status is AVAILABLE or SOLD_OUT.
	Status string `json:"status"`
	// BrandInformation are the fare brands on sale.
	BrandInformation []brand `json:"brandInformation"`
}

// brand is a fare brand of a cabin.
type brand struct {
	Status           string        `json:"status"`
	FareBrand        string        `json:"fareBrand"`
	CabinClass       string        `json:"cabinClass"`
	SeatsAvailable   *int          `json:"seatsAvailable"`
	ReservationClass []reservation `json:"reservationClass"`
	PriceDetails     struct {
		Summary struct {
			Total    []amount `json:"total"`
			TaxTotal []amount `json:"taxTotal"`
		} `json:"summary"`
	} `json:"priceDetails"`
}

// reservation is the booking class of one flight of a brand.
type reservation struct {
	RBD        string `json:"rbd"`
	AirSegment string `json:"airSegment"`
	CabinClass string `json:"cabinClass"`
}

// amount is a price in miles or in cash.
type amount struct {
	Amount   float64 `json:"amount"`
	Type     string  `json:"type"`
	Currency string  `json:"currency"`
}

// offers turns every available brand of every option of a's first leg into
// an offer for passengers, booked at booking, timing flights with z.
func offers(
	a answer,
	z *zones,
	passengers int,
	booking string,
) []award.Offer {
	out := []award.Offer{}
	if len(a.Bounds) == 0 {
		return out
	}
	currency := a.Currency.Priced.Code
	for _, o := range a.Bounds[0].Options {
		segs, ok := segments(o, z)
		if !ok {
			continue
		}
		for _, c := range o.Cabins {
			if c.Status != "AVAILABLE" {
				continue
			}
			for _, b := range c.BrandInformation {
				off, ok := offer(o, segs, c, b, currency, passengers, booking)
				if ok {
					out = append(out, off)
				}
			}
		}
	}
	return out
}

// segments builds the flights of option o, with no cabin or booking class
// yet. It returns false when o has no flights or a time cannot be read.
func segments(o option, z *zones) ([]award.Segment, bool) {
	if len(o.AirSegments) == 0 {
		return nil, false
	}
	out := make([]award.Segment, len(o.AirSegments))
	for i, s := range o.AirSegments {
		minutes := parseDuration(s.Duration)
		dep, arr, ok := z.flightTimes(s.Departure, s.Arrival,
			s.DepartureDateTime, s.ArrivalDateTime, minutes)
		if !ok {
			return nil, false
		}
		if minutes == 0 {
			minutes = int(arr.Sub(dep).Minutes())
		}
		marketing := firstOf(s.MarketingCarrierCode, s.CarrierCode)
		out[i] = award.Segment{
			Flight:           marketing + s.FlightNumber,
			MarketingCarrier: marketing,
			OperatingCarrier: firstOf(s.OperatingCarrierCode, marketing),
			Origin:           s.Departure,
			Destination:      s.Arrival,
			Departure:        dep,
			Arrival:          arr,
			DurationMinutes:  minutes,
			Aircraft:         s.AircraftType,
		}
	}
	for i := range len(out) - 1 {
		out[i].LayoverMinutes = int(
			out[i+1].Departure.Sub(out[i].Arrival).Minutes())
	}
	return out, true
}

// firstOf returns the first of values that is not empty.
func firstOf(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// durationPattern matches a duration such as "18H55M".
var durationPattern = regexp.MustCompile(`^(?:(\d+)H)?(?:(\d+)M)?$`)

// parseDuration reads a duration such as "8H0M" in minutes, 0 when it
// cannot be read.
func parseDuration(s string) int {
	m := durationPattern.FindStringSubmatch(strings.ToUpper(s))
	if m == nil {
		return 0
	}
	h, _ := strconv.Atoi(m[1])
	mins, _ := strconv.Atoi(m[2])
	return h*60 + mins
}

// cabins maps the answer's cabin codes to cabins.
var cabins = map[string]award.Cabin{
	"Y": award.Economy,
	"W": award.Premium,
	"J": award.Business,
	"C": award.Business,
	"F": award.First,
}

// cabinOf maps a cabin code to a cabin, fallback when unknown.
func cabinOf(code string, fallback award.Cabin) award.Cabin {
	if c, ok := cabins[code]; ok {
		return c
	}
	return fallback
}

// offer builds the offer for brand b of cabin c of option o, whose flights
// are segs, for passengers and booked at booking, with taxes in currency
// unless the amounts name their own. It returns false when b is not on
// sale or has no miles price.
func offer(
	o option,
	segs []award.Segment,
	c cabinFares,
	b brand,
	currency string,
	passengers int,
	booking string,
) (award.Offer, bool) {
	if b.Status != "" && b.Status != "AVAILABLE" {
		return award.Offer{}, false
	}
	miles, cash, code := price(b)
	if miles <= 0 {
		return award.Offer{}, false
	}
	code = firstOf(code, currency, "AED")
	cabin := cabinOf(firstOf(b.CabinClass, c.CabinClass), award.Economy)
	booked := slices.Clone(segs)
	byID := map[string]reservation{}
	for _, r := range b.ReservationClass {
		byID[r.AirSegment] = r
	}
	mixed := false
	var airlines []string
	for i, s := range o.AirSegments {
		r, ok := byID[s.ID]
		if !ok && i < len(b.ReservationClass) {
			r = b.ReservationClass[i]
		}
		booked[i].Cabin = cabinOf(r.CabinClass, cabin)
		booked[i].BookingCode = r.RBD
		if booked[i].Cabin != cabin {
			mixed = true
		}
		op := booked[i].OperatingCarrier
		if !slices.Contains(airlines, op) {
			airlines = append(airlines, op)
		}
	}
	last := len(booked) - 1
	duration := parseDuration(o.OndDuration)
	if duration == 0 {
		duration = int(booked[last].Arrival.Sub(booked[0].Departure).Minutes())
	}
	perPax := int(math.Round(miles / float64(passengers)))
	taxes := roundCents(cash / float64(passengers))
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
		Fare:            b.FareBrand,
		Price: award.Price{
			PointsPerPassenger: perPax,
			PointsTotal:        perPax * passengers,
			TaxesPerPassenger:  award.Money{Amount: taxes, Currency: code},
			TaxesTotal: award.Money{
				Amount:   roundCents(cash),
				Currency: code,
			},
		},
		Seats:      b.SeatsAvailable,
		BookingURL: booking,
		Notes:      notes(booked, o.AirSegments),
		Segments:   booked,
	}, true
}

// price reads the miles and cash for every passenger of brand b, and the
// cash's currency when the answer names it.
func price(b brand) (float64, float64, string) {
	var miles, cash float64
	code := ""
	sum := b.PriceDetails.Summary
	for _, a := range sum.Total {
		if a.Type == "MILES" {
			miles += a.Amount
		}
	}
	taxes := sum.TaxTotal
	if len(taxes) == 0 {
		taxes = sum.Total
	}
	for _, a := range taxes {
		if a.Type == "CASH" {
			cash += a.Amount
			code = firstOf(code, a.Currency)
		}
	}
	return miles, cash, code
}

// roundCents rounds v to two decimals.
func roundCents(v float64) float64 {
	return math.Round(v*100) / 100
}

// notes are what the answer says about an itinerary beyond its price:
// partner flights and stops a flight makes on the way.
func notes(segs []award.Segment, raw []airSegment) []string {
	var out []string
	for i, s := range segs {
		if s.OperatingCarrier != s.MarketingCarrier {
			out = append(out, s.Flight+" operated by "+s.OperatingCarrier)
		}
		if raw[i].Stops > 0 {
			out = append(out, fmt.Sprintf("%s makes %d stop(s) en route",
				s.Flight, raw[i].Stops))
		}
	}
	return out
}
