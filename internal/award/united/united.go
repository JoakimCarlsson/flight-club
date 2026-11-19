// Package united searches United MileagePlus award availability, priced in
// MileagePlus miles plus taxes and fees in cash.
//
// united.com sits behind Akamai Bot Manager and only answers an award
// search for a signed-in MileagePlus member; anyone else gets a sign-in
// dialog over results that never load. The search is therefore read the
// way the site reads it: the stealth browser opens the award results page
// for the route and day with a saved session's cookies, the page calls the
// FetchFlights API itself, and that JSON answer is captured as the page
// loads it. When the dialog still shows, the member is signed in with the
// account's MileagePlus number or email and password; see session.go for
// how the session is kept.
//
// The answer lists flights, each with its connections and its products,
// one per fare column (economy, premium economy, business) that has award
// seats. Every priced product becomes one offer, United's own flights and
// its partners' alike.
package united

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/joakimcarlsson/bonk"
	"github.com/joakimcarlsson/flight-club/internal/award"
	"github.com/joakimcarlsson/flight-club/internal/browser"
)

// Key is the program key United offers carry.
const Key = "united"

// resultsURL is the results page a traveller searches and books from.
const resultsURL = "https://www.united.com/en/us/fsr/choose-flights"

// homeURL is the site's home page, opened before the results page so the
// bot protection's sensor script earns its cookies on an ordinary visit.
const homeURL = "https://www.united.com/en/us"

// apiPath is the path of the search API whose answer the page loads.
const apiPath = "/api/flight/fetchflights"

// loadTimeout bounds the navigation to the results page.
const loadTimeout = 60 * time.Second

// localLayout is the layout of the answer's local times.
const localLayout = "2006-01-02 15:04"

// errBlocked is returned when united.com answers with Akamai's denial
// instead of the results page.
var errBlocked = errors.New(
	"united: access denied by the site's bot protection",
)

// Program searches united.com through a stealth browser, signed in as one
// MileagePlus member.
type Program struct {
	browser *browser.Browser
	account Account
	session *session
}

// New builds a Program that loads united.com in b, signed in as account,
// keeping its session in account.SessionFile.
func New(b *browser.Browser, account Account) *Program {
	return &Program{
		browser: b,
		account: account,
		session: &session{path: account.sessionPath()},
	}
}

// Key returns the program key, "united".
func (p *Program) Key() string {
	return Key
}

// Search returns United's award offers for q, one per fare column of
// every itinerary. It returns an error when the browser cannot load the
// results page, the page is denied by the bot protection, the member
// cannot be signed in without help (errSignInNeeded, returned at once
// from then on until the session file changes), or the answer cannot be
// read. A day without award flights gives no offers and no error.
func (p *Program) Search(
	ctx context.Context,
	q award.Query,
) ([]award.Offer, error) {
	if p.session.isStuck() {
		return nil, errSignInNeeded
	}
	body, captured, err := p.collect(ctx, q, nil)
	if errors.Is(err, errSignInNeeded) {
		p.session.markStuck()
	}
	if err != nil {
		if strings.Contains(captured.HTML, "Access Denied") {
			return nil, errBlocked
		}
		return nil, short(err)
	}
	var a answer
	if err := json.Unmarshal(body, &a); err != nil {
		return nil, errors.New("united: unreadable search answer")
	}
	_ = p.session.save(captured.Cookies)
	return offers(a, max(q.Passengers, 1), bookingURL(q)), nil
}

// collect opens the results page for q with the saved session, signing in
// with code when the site asks, and returns the search API's answer and
// what the page left. It returns an error when the page cannot be loaded,
// the sign-in fails or no answer comes in time; the captured page is
// returned with a missing answer so the caller can tell why.
func (p *Program) collect(
	ctx context.Context,
	q award.Query,
	code CodeFunc,
) ([]byte, browser.Captured, error) {
	var mu sync.Mutex
	var body []byte
	keep := func(u string, data []byte) {
		if !isAnswerURL(u) || !bytes.Contains(data, []byte(`"data"`)) {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		body = data
	}
	has := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return body != nil
	}
	var waitErr error
	captured, err := p.browser.Collect(ctx, bookingURL(q), browser.CollectOpts{
		Timeout: loadTimeout,
		WarmUp:  homeURL,
		Cookies: p.session.load(),
		Accept:  func(string, string) bool { return false },
		CaptureBody: func(u, _ string) bool {
			return isAnswerURL(u)
		},
		Interact: func(ctx context.Context, page *browser.Page) error {
			unsub := page.OnResponse(func(r *bonk.Response) {
				if !isAnswerURL(r.URL) {
					return
				}
				if data, err := r.Body(); err == nil {
					keep(r.URL, data)
				}
			})
			defer unsub()
			waitErr = awaitAnswer(ctx, page, p.account, code, has)
			if errors.Is(waitErr, errSignInNeeded) {
				return waitErr
			}
			return nil
		},
	})
	if err != nil {
		return nil, captured, err
	}
	for _, b := range captured.Bodies {
		keep(b.URL, b.Data)
	}
	if body == nil {
		if waitErr == nil {
			waitErr = errors.New("united: no search answer captured")
		}
		return nil, captured, waitErr
	}
	return body, captured, nil
}

// isAnswerURL reports whether u is the search API's address.
func isAnswerURL(u string) bool {
	return strings.Contains(strings.ToLower(u), apiPath)
}

// bookingURL is the award results page for a one-way search for q.
func bookingURL(q award.Query) string {
	v := url.Values{}
	v.Set("f", q.Origin)
	v.Set("t", q.Destination)
	v.Set("d", q.Date.Format(time.DateOnly))
	v.Set("tt", "1")
	v.Set("at", "1")
	v.Set("sc", "7")
	v.Set("px", strconv.Itoa(max(q.Passengers, 1)))
	v.Set("taxng", "1")
	v.Set("newHP", "True")
	v.Set("clm", "7")
	v.Set("st", "bestmatches")
	v.Set("tqp", "A")
	return resultsURL + "?" + v.Encode()
}

// answer is the part of a FetchFlights answer that is read.
type answer struct {
	// Data holds the results.
	Data struct {
		// Trips are the searched slices; a one-way search has one.
		Trips []struct {
			// Flights are the itineraries, each led by its first flight.
			Flights []flight `json:"Flights"`
		} `json:"Trips"`
	} `json:"data"`
}

// flight is one flight of an itinerary. The first flight of an itinerary
// carries the rest as Connections.
type flight struct {
	// MarketingCarrier is the IATA code of the airline selling it.
	MarketingCarrier string `json:"MarketingCarrier"`
	// FlightNumber is the number it is sold under.
	FlightNumber string `json:"FlightNumber"`
	// OperatingCarrier is the IATA code of the airline flying it.
	OperatingCarrier string `json:"OperatingCarrier"`
	// Origin is the IATA code of the airport it leaves from.
	Origin string `json:"Origin"`
	// Destination is the IATA code of the airport it lands at.
	Destination string `json:"Destination"`
	// DepartDateTime is the local departure, e.g. "2026-12-29 13:50".
	DepartDateTime string `json:"DepartDateTime"`
	// DestinationDateTime is the local arrival.
	DestinationDateTime string `json:"DestinationDateTime"`
	// OrgTimezoneOffset is the origin's UTC offset in hours.
	OrgTimezoneOffset float64 `json:"OrgTimezoneOffset"`
	// DestTimezoneOffset is the destination's UTC offset in hours.
	DestTimezoneOffset float64 `json:"DestTimezoneOffset"`
	// TravelMinutes is the time in the air.
	TravelMinutes int `json:"TravelMinutes"`
	// TravelMinutesTotal is the door-to-door time of the itinerary.
	TravelMinutesTotal int `json:"TravelMinutesTotal"`
	// ConnectTimeMinutes is the connection before this flight.
	ConnectTimeMinutes int `json:"ConnectTimeMinutes"`
	// BookingClassAvailability lists seats per booking class, e.g. "X9|I0".
	BookingClassAvailability string `json:"BookingClassAvailability"`
	// EquipmentDisclosures describe the aircraft.
	EquipmentDisclosures struct {
		// EquipmentDescription names the aircraft type.
		EquipmentDescription string `json:"EquipmentDescription"`
	} `json:"EquipmentDisclosures"`
	// Connections are the itinerary's later flights, in order.
	Connections []flight `json:"Connections"`
	// Products are the flight's fares, one per fare column.
	Products []product `json:"Products"`
}

// product is a flight's fare in one fare column.
type product struct {
	// ProductType names the fare column, e.g. "MIN-BUSINESS-SURP-OR-DISP".
	ProductType string `json:"ProductType"`
	// CabinType is the cabin flown: Coach, PREMIUMPLUS, Business or First.
	CabinType string `json:"CabinType"`
	// BookingCode is the booking class, e.g. "X".
	BookingCode string `json:"BookingCode"`
	// Description names the fare, e.g. "United Polaris business".
	Description string `json:"Description"`
	// AwardType is "Saver" or "Standard".
	AwardType string `json:"AwardType"`
	// Prices are the miles and the taxes, per passenger.
	Prices []productPrice `json:"Prices"`
}

// productPrice is one price of a product.
type productPrice struct {
	// Currency is "MILES" or an ISO 4217 code.
	Currency string `json:"Currency"`
	// Amount is the price per passenger.
	Amount float64 `json:"Amount"`
	// PricingType is "Award" for the miles and "Tax" for the cash.
	PricingType string `json:"PricingType"`
}

// offers turns every priced fare of every itinerary in a into an offer for
// passengers, booked at booking, leaving out fares identical to one
// already kept.
func offers(a answer, passengers int, booking string) []award.Offer {
	out := []award.Offer{}
	seen := map[string]bool{}
	for _, t := range a.Data.Trips {
		for _, f := range t.Flights {
			legs := append([]flight{f}, f.Connections...)
			for i := range f.Products {
				o, ok := offer(legs, i, passengers, booking)
				if !ok {
					continue
				}
				k := offerKey(o)
				if !seen[k] {
					seen[k] = true
					out = append(out, o)
				}
			}
		}
	}
	return out
}

// offerKey identifies an offer by its flights, booking classes and price.
func offerKey(o award.Offer) string {
	var b strings.Builder
	for _, s := range o.Segments {
		b.WriteString(s.Flight + s.Departure.String() + s.BookingCode + "/")
	}
	b.WriteString(string(o.Cabin) + strconv.Itoa(o.Price.PointsPerPassenger))
	return b.String()
}

// offer builds the offer for the fare at index i of the first flight of
// legs, for passengers and booked at booking. It returns false when the
// fare has no miles price or a flight cannot be read.
func offer(
	legs []flight,
	i, passengers int,
	booking string,
) (award.Offer, bool) {
	lead := legs[0].Products[i]
	points, taxes, ok := farePrice(lead.Prices)
	if !ok {
		return award.Offer{}, false
	}
	cabin := columnCabin(lead.ProductType)
	segs := make([]award.Segment, len(legs))
	var seats *int
	var airlines []string
	mixed := false
	for n, l := range legs {
		p, found := productOf(l, i, lead.ProductType)
		if !found || p.BookingCode == "" {
			return award.Offer{}, false
		}
		s, ok := segment(l, p, cabin)
		if !ok {
			return award.Offer{}, false
		}
		if n+1 < len(legs) {
			s.LayoverMinutes = legs[n+1].ConnectTimeMinutes
		}
		segs[n] = s
		if s.Cabin != cabin {
			mixed = true
		}
		if !slices.Contains(airlines, s.OperatingCarrier) {
			airlines = append(airlines, s.OperatingCarrier)
		}
		if c, ok := seatsIn(l.BookingClassAvailability, p.BookingCode); ok &&
			(seats == nil || c < *seats) {
			seats = &c
		}
	}
	last := len(segs) - 1
	duration := legs[0].TravelMinutesTotal
	if duration == 0 {
		duration = int(segs[last].Arrival.Sub(segs[0].Departure).Minutes())
	}
	return award.Offer{
		Program:         Key,
		Availability:    award.Confirmed,
		Origin:          segs[0].Origin,
		Destination:     segs[last].Destination,
		Departure:       segs[0].Departure,
		Arrival:         segs[last].Arrival,
		DurationMinutes: duration,
		Stops:           last,
		Airlines:        airlines,
		Cabin:           cabin,
		MixedCabin:      mixed,
		Fare:            fareName(lead, cabin),
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
		Notes:      notes(segs),
		Segments:   segs,
	}, true
}

// productOf finds the fare of flight l in the column typ, preferring the
// one at index i, where the answer lists it. It returns false when l has
// no fare in that column.
func productOf(l flight, i int, typ string) (product, bool) {
	if i < len(l.Products) && l.Products[i].ProductType == typ {
		return l.Products[i], true
	}
	for _, p := range l.Products {
		if p.ProductType == typ {
			return p, true
		}
	}
	return product{}, false
}

// segment builds flight l booked as fare p in a column of cabin. It
// returns false when its times cannot be read.
func segment(l flight, p product, cabin award.Cabin) (award.Segment, bool) {
	dep, err1 := localTime(l.DepartDateTime, l.OrgTimezoneOffset)
	arr, err2 := localTime(l.DestinationDateTime, l.DestTimezoneOffset)
	if err1 != nil || err2 != nil {
		return award.Segment{}, false
	}
	op := l.OperatingCarrier
	if op == "" {
		op = l.MarketingCarrier
	}
	minutes := l.TravelMinutes
	if minutes == 0 {
		minutes = int(arr.Sub(dep).Minutes())
	}
	return award.Segment{
		Flight:           l.MarketingCarrier + l.FlightNumber,
		MarketingCarrier: l.MarketingCarrier,
		OperatingCarrier: op,
		Origin:           l.Origin,
		Destination:      l.Destination,
		Departure:        dep,
		Arrival:          arr,
		DurationMinutes:  minutes,
		Aircraft:         l.EquipmentDisclosures.EquipmentDescription,
		Cabin:            segmentCabin(p, cabin),
		BookingCode:      p.BookingCode,
	}, true
}

// localTime reads a local time such as "2026-12-29 13:50" at a UTC offset
// of hours. It returns an error when s is not such a time.
func localTime(s string, hours float64) (time.Time, error) {
	secs := int(math.Round(hours * 3600))
	return time.ParseInLocation(localLayout, s, time.FixedZone("", secs))
}

// columnCabin maps a fare column such as "MIN-BUSINESS-SURP-OR-DISP" to
// the cabin it sells.
func columnCabin(typ string) award.Cabin {
	t := strings.ToUpper(typ)
	switch {
	case strings.Contains(t, "FIRST"):
		return award.First
	case strings.Contains(t, "BUSINESS"):
		return award.Business
	case strings.Contains(t, "PREMIUM"):
		return award.Premium
	}
	return award.Economy
}

// segmentCabin maps the cabin fare p books a flight in to a cabin, for a
// fare sold in a column of cabin column. The economy award classes X and
// Y are economy whatever cabin the answer names, as it names the column's
// cabin for a flight that has none such, and United's domestic first
// class is sold in the business column, so First there is business.
func segmentCabin(p product, column award.Cabin) award.Cabin {
	if strings.HasPrefix(p.BookingCode, "X") ||
		strings.HasPrefix(p.BookingCode, "Y") {
		return award.Economy
	}
	name := p.CabinType
	switch strings.ToUpper(name) {
	case "COACH", "ECONOMY":
		return award.Economy
	case "PREMIUMPLUS", "PREMIUM":
		return award.Premium
	case "BUSINESS":
		return award.Business
	case "FIRST":
		if column == award.First {
			return award.First
		}
		return award.Business
	}
	if c, ok := award.ParseCabin(name); ok {
		return c
	}
	return column
}

// seatsIn reads the seats left in booking class code from avail, such as
// "J9|JN9|X4". It returns false when avail does not list code.
func seatsIn(avail, code string) (int, bool) {
	for _, part := range strings.Split(avail, "|") {
		n, ok := strings.CutPrefix(part, code)
		if !ok || n == "" {
			continue
		}
		if c, err := strconv.Atoi(n); err == nil {
			return c, true
		}
	}
	return 0, false
}

// farePrice reads the miles and taxes per passenger from prices. It
// returns false when there is no miles price.
func farePrice(prices []productPrice) (int, award.Money, bool) {
	points := 0
	taxes := award.Money{Currency: "USD"}
	for _, p := range prices {
		switch {
		case strings.EqualFold(p.Currency, "MILES"):
			points = int(math.Round(p.Amount))
		case strings.EqualFold(p.PricingType, "Tax"):
			taxes = award.Money{
				Amount:   roundCents(p.Amount),
				Currency: p.Currency,
			}
		}
	}
	return points, taxes, points > 0
}

// roundCents rounds v to two decimals.
func roundCents(v float64) float64 {
	return math.Round(v*100) / 100
}

// notes name the flights of segs another airline operates.
func notes(segs []award.Segment) []string {
	var out []string
	for _, s := range segs {
		if s.OperatingCarrier != s.MarketingCarrier {
			out = append(out, s.Flight+" operated by "+s.OperatingCarrier)
		}
	}
	return out
}

// fareName names fare p sold in a column of cabin, e.g. "Saver business".
func fareName(p product, cabin award.Cabin) string {
	return strings.TrimSpace(p.AwardType + " " + string(cabin))
}

// short keeps err as it is when its message is short, and else replaces
// it with a short error saying the search failed, keeping err wrapped.
func short(err error) error {
	if len(err.Error()) < 120 {
		return err
	}
	return &shortError{err: err}
}

// shortError is an error whose message is too long to show, such as one
// naming the whole results page address.
type shortError struct {
	err error
}

// Error says the results page could not be loaded.
func (e *shortError) Error() string {
	return "united: results page could not be loaded"
}

// Unwrap returns the long error.
func (e *shortError) Unwrap() error {
	return e.err
}
