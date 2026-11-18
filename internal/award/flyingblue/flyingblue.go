// Package flyingblue searches Flying Blue award availability, the
// frequent-flyer programme of Air France and KLM, priced in Flying Blue
// miles.
//
// The booking site's search runs on a GraphQL endpoint, /gql/v1, guarded by
// Akamai: a plain request, whatever its headers, gets "Access Denied", and
// every query needs a hashcash proof of work and a search state the page
// itself created. The endpoint is therefore read the way the site reads it:
// the stealth browser opens the miles search results page, which creates the
// search state, solves the hashcash and asks for the available offers, and
// the answer of SearchResultAvailableOffersQuery is captured as the page
// loads it. That query is asked with upsell cabins, so one answer carries the
// economy, premium and business prices of every itinerary.
//
// Akamai blocks an address after a handful of searches in a short window, so
// the program must be paced: one search at a time, several seconds apart.
//
// The site searches miles only for a signed-in Flying Blue member: for a
// browser without a member session the results page goes to the sign-in
// page instead, and Search reports that as an error.
package flyingblue

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

// Key is the program key Flying Blue offers carry.
const Key = "flyingblue"

// resultsURL is the flight results page a traveller searches and books from.
const resultsURL = "https://wwws.airfrance.us/search/offers"

// offersOperation is the name of the GraphQL query whose answer holds the
// available offers, as it appears in the endpoint's URL.
const offersOperation = "SearchResultAvailableOffersQuery"

// gqlPath is the path of the site's GraphQL endpoint.
const gqlPath = "/gql/v1"

// loginPath is the path of the endpoint the site asks whether the browser
// is signed in.
const loginPath = "/endpoint/v1/oauth/login"

// milesCurrency is the currency the endpoint prices miles in.
const milesCurrency = "MILES"

// loadTimeout bounds the navigation to the results page.
const loadTimeout = 60 * time.Second

// settleRounds and settleDelay are how long the results page is given, after
// it loads, to ask for the offers: settleRounds waits of settleDelay each.
const (
	settleRounds = 5
	settleDelay  = 3 * time.Second
)

// Program searches the Air France booking site through a stealth browser.
type Program struct {
	browser *browser.Browser
}

// New builds a Program that loads the Air France booking site in b.
func New(b *browser.Browser) *Program {
	return &Program{browser: b}
}

// Key returns the program key, "flyingblue".
func (p *Program) Key() string {
	return Key
}

// Search returns Flying Blue's award offers for q, one per itinerary and
// cabin with seats for every passenger. It returns an error when the browser
// cannot load the results page, Akamai denies it, or the endpoint answers
// with something that cannot be read or reports a failure. A route Flying
// Blue does not sell gives no offers and no error.
func (p *Program) Search(
	ctx context.Context,
	q award.Query,
) ([]award.Offer, error) {
	booking := bookingURL(q)
	captured, err := p.browser.Collect(ctx, booking, browser.CollectOpts{
		Timeout:     loadTimeout,
		Scrolls:     settleRounds,
		ScrollDelay: settleDelay,
		Accept:      func(string, string) bool { return false },
		CaptureBody: func(u, _ string) bool {
			return strings.Contains(u, gqlPath) ||
				strings.Contains(u, loginPath)
		},
	})
	if err != nil {
		if strings.Contains(err.Error(), "ERR_HTTP2_PROTOCOL_ERROR") {
			return nil, fmt.Errorf("%w: %v", errDenied, err)
		}
		return nil, fmt.Errorf("flyingblue: %w", err)
	}
	var found *answer
	for _, b := range captured.Bodies {
		a, ok, err := parseAnswer(b)
		if err != nil {
			return nil, err
		}
		if ok {
			found = &a
		}
	}
	if found == nil {
		if denied(captured) {
			return nil, errDenied
		}
		if signedOut(captured) {
			return nil, errSignedOut
		}
		return []award.Offer{}, nil
	}
	return offers(*found, q, booking), nil
}

// errDenied is returned when Akamai denies the browser the booking site or
// its search endpoint.
var errDenied = errors.New(
	"flyingblue: access denied by the site's bot protection",
)

// errSession is returned when the endpoint refuses the search state, which
// happens when the page could not start a miles search, typically because
// the browser is not signed in to Flying Blue.
var errSession = errors.New(
	"flyingblue: the site refused the search session",
)

// errSignedOut is returned when the results page sends the browser to the
// Flying Blue sign-in page instead of searching: the site only searches
// miles for a signed-in member, and the browser has no member session.
var errSignedOut = errors.New(
	"flyingblue: miles search needs a signed-in Flying Blue session",
)

// signedOut reports whether c shows the site saying the browser is not
// signed in, or sending it to the sign-in page.
func signedOut(c browser.Captured) bool {
	for _, b := range c.Bodies {
		if strings.Contains(b.URL, "identity.airfranceklm.com") ||
			strings.Contains(string(b.Data), `"isLoggedIn":false`) {
			return true
		}
	}
	return false
}

// denied reports whether c shows Akamai's "Access Denied" page or a denied
// endpoint answer.
func denied(c browser.Captured) bool {
	if strings.Contains(c.HTML, "Access Denied") {
		return true
	}
	for _, b := range c.Bodies {
		if strings.Contains(string(b.Data), "Access Denied") {
			return true
		}
	}
	return false
}

// bookingURL is the miles results page for a one-way search for q.
func bookingURL(q award.Query) string {
	pax := strconv.Itoa(max(q.Passengers, 1)) + ":0:0:0:0:0:0:0"
	conn := fmt.Sprintf("%s:%s-%s:%s@%s",
		q.Origin, locationType(q.Origin),
		q.Destination, locationType(q.Destination),
		q.Date.Format("20060102"))
	v := url.Values{}
	v.Set("pax", pax)
	v.Set("cabinClass", "ECONOMY")
	v.Set("activeConnection", "0")
	v.Set("connections", conn)
	v.Set("bookingFlow", "REWARD")
	return resultsURL + "?" + v.Encode()
}

// cityCodes are the city codes the site searches as cities rather than as
// airports.
var cityCodes = map[string]bool{
	"PAR": true, "NYC": true, "LON": true, "WAS": true, "CHI": true,
	"TYO": true, "MIL": true, "ROM": true, "STO": true, "YTO": true,
	"YMQ": true, "BUE": true, "SAO": true, "RIO": true, "OSA": true,
	"SEL": true, "BJS": true, "SHA": true, "MOW": true, "BER": true,
}

// locationType is the site's letter for the kind of location code is: "C"
// for a city, "A" for an airport.
func locationType(code string) string {
	if cityCodes[code] {
		return "C"
	}
	return "A"
}

// answer is the available offers part of an endpoint answer.
type answer struct {
	// TypeName is the GraphQL type, "DataSourceError" when it failed.
	TypeName string `json:"__typename"`
	// OfferItineraries are the itineraries with their prices per cabin.
	OfferItineraries []offerItinerary `json:"offerItineraries"`
}

// envelope is a GraphQL answer of the endpoint.
type envelope struct {
	// Data holds the query's result.
	Data *struct {
		// AvailableOffers holds the offers, nil for other queries.
		AvailableOffers *answer `json:"availableOffers"`
	} `json:"data"`
	// Errors are the GraphQL errors, if any.
	Errors []struct {
		// Message says what went wrong.
		Message string `json:"message"`
	} `json:"errors"`
}

// offerItinerary is one itinerary and its prices.
type offerItinerary struct {
	// ActiveConnection is the itinerary's flights.
	ActiveConnection *connection `json:"activeConnection"`
	// UpsellCabinProducts are its prices, one product per cabin.
	UpsellCabinProducts []struct {
		// Connections price each connection of the product.
		Connections []cabinPrice `json:"connections"`
	} `json:"upsellCabinProducts"`
}

// connection is the flights of one itinerary.
type connection struct {
	// Duration is the door-to-door time in minutes.
	Duration int `json:"duration"`
	// Segments are its flights, in order.
	Segments []segment `json:"segments"`
}

// segment is one flight.
type segment struct {
	// Origin is where it leaves from.
	Origin place `json:"origin"`
	// Destination is where it lands.
	Destination place `json:"destination"`
	// DepartureDateTime is the local departure time.
	DepartureDateTime string `json:"departureDateTime"`
	// ArrivalDateTime is the local arrival time.
	ArrivalDateTime string `json:"arrivalDateTime"`
	// Duration is the time in the air in minutes.
	Duration int `json:"duration"`
	// MarketingFlight names the flight it is sold under and who flies it.
	MarketingFlight struct {
		// Number is the flight number, possibly zero-padded.
		Number string `json:"number"`
		// Carrier is the airline selling it.
		Carrier carrier `json:"carrier"`
		// OperatingFlight is the flight as the operating airline runs it.
		OperatingFlight *struct {
			// Carrier is the airline flying it.
			Carrier carrier `json:"carrier"`
		} `json:"operatingFlight"`
	} `json:"marketingFlight"`
	// EquipmentName is the aircraft type.
	EquipmentName string `json:"equipmentName"`
	// EquipmentType is the aircraft type, when given as an object.
	EquipmentType *struct {
		// Name is the aircraft type's name.
		Name string `json:"name"`
	} `json:"equipmentType"`
}

// place is an airport.
type place struct {
	// Code is the IATA code.
	Code string `json:"code"`
}

// carrier is an airline.
type carrier struct {
	// Code is the IATA code.
	Code string `json:"code"`
	// Name is the airline's name.
	Name string `json:"name"`
}

// cabinPrice is a connection's price and seats in one cabin.
type cabinPrice struct {
	// CabinClass is the cabin: ECONOMY, PREMIUM, BUSINESS or FIRST.
	CabinClass string `json:"cabinClass"`
	// NumberOfSeatsAvailable is how many seats are left at this price.
	NumberOfSeatsAvailable int `json:"numberOfSeatsAvailable"`
	// Price is the miles price per passenger.
	Price *amount `json:"price"`
	// Tax is the taxes and surcharges per passenger.
	Tax *amount `json:"tax"`
	// Taxes is the taxes and surcharges per passenger, as some answers
	// name them.
	Taxes *amount `json:"taxes"`
	// FareBasis is the fare basis code, when given.
	FareBasis string `json:"fareBasis"`
	// FareFamily names the fare, when given.
	FareFamily *struct {
		// Code is the fare family's code.
		Code string `json:"code"`
	} `json:"fareFamily"`
	// Segments say how each segment is booked, when given.
	Segments []struct {
		// CabinClass is the segment's cabin.
		CabinClass string `json:"cabinClass"`
		// SellingClass is the booking class letter.
		SellingClass *struct {
			// Code is the letter.
			Code string `json:"code"`
		} `json:"sellingClass"`
	} `json:"segments"`
}

// amount is an amount in a currency.
type amount struct {
	// Amount is in the currency's major unit, or miles.
	Amount float64 `json:"amount"`
	// Currency is an ISO 4217 code or milesCurrency.
	Currency string `json:"currency"`
}

// parseAnswer reads the available offers out of a captured endpoint answer.
// It returns false for an answer of another query. It returns errDenied for
// Akamai's denial, errSession for a refused search state, and an error for
// an offers answer that cannot be read or carries GraphQL errors.
func parseAnswer(b browser.Body) (answer, bool, error) {
	if !strings.Contains(b.URL, offersOperation) {
		return answer{}, false, nil
	}
	if strings.Contains(string(b.Data), "Access Denied") {
		return answer{}, false, errDenied
	}
	var env envelope
	if err := json.Unmarshal(b.Data, &env); err != nil {
		return answer{}, false, fmt.Errorf("flyingblue: read offers: %w", err)
	}
	if env.Data == nil || env.Data.AvailableOffers == nil {
		if len(env.Errors) > 0 {
			return answer{}, false, fmt.Errorf("flyingblue: search: %s",
				env.Errors[0].Message)
		}
		return answer{}, false, nil
	}
	a := *env.Data.AvailableOffers
	if a.TypeName == "DataSourceError" {
		return answer{}, false, errSession
	}
	return a, true, nil
}

// offers turns every cabin with enough seats of every itinerary in a into
// an offer for q, booked at booking.
func offers(a answer, q award.Query, booking string) []award.Offer {
	out := []award.Offer{}
	for _, it := range a.OfferItineraries {
		if it.ActiveConnection == nil ||
			len(it.ActiveConnection.Segments) == 0 {
			continue
		}
		times, ok := itineraryTimes(it.ActiveConnection.Segments)
		if !ok {
			continue
		}
		for _, up := range it.UpsellCabinProducts {
			if len(up.Connections) == 0 {
				continue
			}
			o, ok := offer(*it.ActiveConnection, times, up.Connections[0],
				q.Passengers, booking)
			if ok {
				out = append(out, o)
			}
		}
	}
	return out
}

// offer builds the offer for connection c, whose segments leave and land at
// times, priced in cabin price cp for passengers and booked at booking. It
// returns false when the cabin is unknown, has no miles price or too few
// seats.
func offer(
	c connection,
	times []segmentTimes,
	cp cabinPrice,
	passengers int,
	booking string,
) (award.Offer, bool) {
	cabin, ok := cabinOf(cp.CabinClass)
	if !ok || cp.Price == nil || cp.Price.Amount <= 0 {
		return award.Offer{}, false
	}
	if cp.Price.Currency != "" && cp.Price.Currency != milesCurrency {
		return award.Offer{}, false
	}
	pax := max(passengers, 1)
	if cp.NumberOfSeatsAvailable < pax {
		return award.Offer{}, false
	}
	seats := cp.NumberOfSeatsAvailable
	miles := int(math.Round(cp.Price.Amount))
	tax := taxOf(cp)

	segs := make([]award.Segment, len(c.Segments))
	var airlines []string
	mixed := false
	for i, s := range c.Segments {
		seg := buildSegment(s, times[i], cabin)
		if i < len(cp.Segments) {
			if sc, ok := cabinOf(cp.Segments[i].CabinClass); ok {
				seg.Cabin = sc
			}
			if sel := cp.Segments[i].SellingClass; sel != nil {
				seg.BookingCode = sel.Code
			}
		}
		if seg.Cabin != cabin {
			mixed = true
		}
		if i+1 < len(c.Segments) {
			seg.LayoverMinutes = int(
				times[i+1].departure.Sub(times[i].arrival).Minutes())
		}
		if !contains(airlines, seg.OperatingCarrier) {
			airlines = append(airlines, seg.OperatingCarrier)
		}
		segs[i] = seg
	}
	first, last := times[0], times[len(times)-1]
	duration := c.Duration
	if duration <= 0 {
		duration = int(last.arrival.Sub(first.departure).Minutes())
	}
	return award.Offer{
		Program:         Key,
		Availability:    award.Confirmed,
		Origin:          segs[0].Origin,
		Destination:     segs[len(segs)-1].Destination,
		Departure:       first.departure,
		Arrival:         last.arrival,
		DurationMinutes: duration,
		Stops:           len(segs) - 1,
		Airlines:        airlines,
		Cabin:           cabin,
		MixedCabin:      mixed,
		Fare:            fareName(cp),
		Price: award.Price{
			PointsPerPassenger: miles,
			PointsTotal:        miles * pax,
			TaxesPerPassenger:  tax,
			TaxesTotal: award.Money{
				Amount:   roundCents(tax.Amount * float64(pax)),
				Currency: tax.Currency,
			},
		},
		Seats:      &seats,
		BookingURL: booking,
		Notes:      notes(segs),
		Segments:   segs,
	}, true
}

// buildSegment builds the segment for s, leaving and landing at t, booked in
// cabin.
func buildSegment(s segment, t segmentTimes, cabin award.Cabin) award.Segment {
	mf := s.MarketingFlight
	marketing := mf.Carrier.Code
	operating := marketing
	if mf.OperatingFlight != nil && mf.OperatingFlight.Carrier.Code != "" {
		operating = mf.OperatingFlight.Carrier.Code
	}
	aircraft := s.EquipmentName
	if aircraft == "" && s.EquipmentType != nil {
		aircraft = s.EquipmentType.Name
	}
	duration := s.Duration
	if duration <= 0 {
		duration = int(t.arrival.Sub(t.departure).Minutes())
	}
	return award.Segment{
		Flight:           marketing + strings.TrimLeft(mf.Number, "0"),
		MarketingCarrier: marketing,
		OperatingCarrier: operating,
		Origin:           s.Origin.Code,
		Destination:      s.Destination.Code,
		Departure:        t.departure,
		Arrival:          t.arrival,
		DurationMinutes:  duration,
		Aircraft:         aircraft,
		Cabin:            cabin,
	}
}

// contains reports whether list holds s.
func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// taxOf returns cp's taxes per passenger, zero in euros when not given.
func taxOf(cp cabinPrice) award.Money {
	t := cp.Tax
	if t == nil {
		t = cp.Taxes
	}
	if t == nil {
		return award.Money{Currency: "EUR"}
	}
	return award.Money{Amount: roundCents(t.Amount), Currency: t.Currency}
}

// fareName is the site's name for cp's fare: its fare family, its fare basis
// or its cabin class.
func fareName(cp cabinPrice) string {
	if cp.FareFamily != nil && cp.FareFamily.Code != "" {
		return cp.FareFamily.Code
	}
	if cp.FareBasis != "" {
		return cp.FareBasis
	}
	return cp.CabinClass
}

// notes names the partner airlines that fly any of segs.
func notes(segs []award.Segment) []string {
	var out []string
	for _, s := range segs {
		if s.OperatingCarrier == "AF" || s.OperatingCarrier == "KL" {
			continue
		}
		n := s.Flight + " operated by " + s.OperatingCarrier
		if !contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// roundCents rounds v to two decimals.
func roundCents(v float64) float64 {
	return math.Round(v*100) / 100
}

// cabinOf maps the endpoint's cabin class to a cabin, and false for one it
// does not know.
func cabinOf(name string) (award.Cabin, bool) {
	switch strings.ToUpper(name) {
	case "ECONOMY":
		return award.Economy, true
	case "PREMIUM", "PREMIUM_ECONOMY":
		return award.Premium, true
	case "BUSINESS":
		return award.Business, true
	case "FIRST", "LA_PREMIERE":
		return award.First, true
	}
	return "", false
}
