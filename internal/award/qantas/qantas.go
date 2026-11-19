// Package qantas searches Qantas Frequent Flyer award availability, priced in
// Qantas Points as international Classic Flight Rewards on Qantas, Jetstar
// and partner airlines.
//
// The search is the Flight Reward finder at flightrewardfinder.qantas.com,
// which answers without a session or a login. It sits behind Cloudflare,
// which refuses Go's own TLS client with a 403 "Connection not verified"
// page and puts a direct load of its /api/search endpoint behind a
// challenge, so the finder's page is rendered in the stealth browser for
// the search, and the flights are read from the search answer the server
// renders into the page's Astro island props. The finder answers from an
// index of award space it refreshes through the day rather than from a
// live booking search, so its offers are Estimated: each flight carries
// when its space was last seen, and the space is only certain once checked
// on qantas.com. The answer is paged ten flights at a time and lists every
// cabin a flight has space in, with the points, seats and taxes of one
// passenger. Domestic Australian routes are not in the finder and give no
// offers.
package qantas

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

// Key is the program key Qantas offers carry.
const Key = "qantas"

// finderURL is the Flight Reward finder's page, which renders the search
// its query asks for.
const finderURL = "https://flightrewardfinder.qantas.com/"

// loadTimeout bounds the browser's load of one page of the answer.
const loadTimeout = 45 * time.Second

// maxPages is the most pages of ten flights one search reads.
const maxPages = 5

// maxBookAdults is the most adults the qantas.com deep link takes.
const maxBookAdults = 5

// bookURL is the qantas.com flight search a traveller books a reward from.
const bookURL = "https://www.qantas.com/en-au/book/flights"

// Program searches the Qantas Flight Reward finder through a stealth
// browser.
type Program struct {
	browser *browser.Browser
}

// New builds a Program that renders the finder's page in b.
func New(b *browser.Browser) *Program {
	return &Program{browser: b}
}

// Key returns the program key, "qantas".
func (p *Program) Key() string {
	return Key
}

// errBlocked is returned when Cloudflare answers with its "Connection not
// verified" page rather than the search.
var errBlocked = errors.New(
	"qantas: search blocked by the site's bot protection",
)

// errLimited is returned when the finder answers that too many searches
// were made.
var errLimited = errors.New(
	"qantas: the site is limiting searches; wait a minute and try again",
)

// Search returns the Classic Flight Rewards the finder knows of for q. It
// reads every page of the answer, up to maxPages. It returns an error when
// the browser cannot load the answer, the site blocks or limits the
// search, or the answer cannot be read. A route the finder does not sell
// gives no offers and no error.
func (p *Program) Search(
	ctx context.Context,
	q award.Query,
) ([]award.Offer, error) {
	out := []award.Offer{}
	booking := bookingURL(q)
	for page := 1; page <= maxPages; page++ {
		res, err := p.fetch(ctx, q, page)
		if err != nil {
			return nil, err
		}
		if res.RouteDecision.Blocked {
			return out, nil
		}
		for _, f := range res.Flights {
			out = append(out, offers(f, q, booking)...)
		}
		if len(res.Flights) == 0 || page >= res.Pagination.MaxKnownPage {
			break
		}
	}
	return out, nil
}

// fetch renders the finder's page for page of q's flights in the browser
// and reads the search answer the server rendered into it. It returns
// errBlocked or errLimited when the site refuses the search, and an error
// when the page cannot be loaded or holds no answer that can be read.
func (p *Program) fetch(
	ctx context.Context,
	q award.Query,
	page int,
) (answer, error) {
	target := finderURL + "?" + searchParams(q, page).Encode()
	captured, err := p.browser.Collect(ctx, target, browser.CollectOpts{
		Timeout: loadTimeout,
		Accept:  func(string, string) bool { return false },
	})
	if err != nil {
		return answer{}, fmt.Errorf("qantas: %w", err)
	}
	res, found, err := initialData(captured.HTML)
	if err != nil {
		return answer{}, fmt.Errorf("qantas: %w", err)
	}
	if !found {
		return answer{}, refusal(captured.HTML)
	}
	return res, nil
}

// refusal is the error for a page that held no answer: errBlocked for
// Cloudflare's block page, errLimited for a rate limit, else a generic
// error.
func refusal(markup string) error {
	lower := strings.ToLower(markup)
	switch {
	case strings.Contains(lower, "connection not verified"),
		strings.Contains(lower, "attention required"),
		strings.Contains(lower, "just a moment"):
		return errBlocked
	case strings.Contains(lower, "too many requests"),
		strings.Contains(lower, "rate limit"):
		return errLimited
	}
	return errors.New("qantas: the search answer could not be read")
}

// searchParams are the finder's query parameters for page of q: origin,
// destination, a one-day date range, passengers and the program, with city
// codes widened to their airports.
func searchParams(q award.Query, page int) url.Values {
	day := q.Date.Format(time.DateOnly)
	v := url.Values{}
	v.Set("o", airportList(q.Origin))
	v.Set("d", airportList(q.Destination))
	v.Set("dr", day+"I"+day)
	v.Set("p", strconv.Itoa(max(q.Passengers, 1)))
	v.Set("pr", "QF")
	if page > 1 {
		v.Set("pg", strconv.Itoa(page))
	}
	return v
}

// airportList is code as the finder takes it: the airports of a city code
// joined by commas, or the airport code itself.
func airportList(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if list, ok := cityAirports[code]; ok {
		return strings.Join(list, ",")
	}
	return code
}

// cityAirports are the airports of the city codes travellers search by.
var cityAirports = map[string][]string{
	"NYC": {"JFK", "EWR", "LGA"},
	"LON": {"LHR", "LGW", "LCY", "STN"},
	"PAR": {"CDG", "ORY"},
	"TYO": {"HND", "NRT"},
	"CHI": {"ORD", "MDW"},
	"WAS": {"IAD", "DCA", "BWI"},
	"SEL": {"ICN", "GMP"},
	"OSA": {"KIX", "ITM"},
	"MIL": {"MXP", "LIN"},
	"ROM": {"FCO"},
	"STO": {"ARN"},
	"BJS": {"PEK", "PKX"},
	"SHA": {"PVG"},
	"YTO": {"YYZ"},
	"YMQ": {"YUL"},
	"SAO": {"GRU", "GIG"},
}

// bookingURL is the qantas.com points search for q's route and day.
func bookingURL(q award.Query) string {
	v := url.Values{}
	v.Set("departureAirportCode", strings.ToUpper(q.Origin))
	v.Set("arrivalAirportCode", strings.ToUpper(q.Destination))
	v.Set("departureDate", q.Date.Format(time.DateOnly))
	v.Set("usePoints", "true")
	v.Set("tripType", "O")
	v.Set("flexibleWithDates", "true")
	v.Set("travelClass", "ALL")
	v.Set("adults", strconv.Itoa(min(max(q.Passengers, 1), maxBookAdults)))
	return bookURL + "?" + v.Encode()
}

// answer is the finder's answer to one search page.
type answer struct {
	Flights    []flight `json:"flights"`
	Pagination struct {
		Page         int `json:"page"`
		PageSize     int `json:"pageSize"`
		MaxKnownPage int `json:"maxKnownPage"`
	} `json:"pagination"`
	RouteDecision struct {
		Blocked bool   `json:"blocked"`
		Reason  string `json:"reason"`
	} `json:"routeDecision"`
}

// flight is one itinerary of the answer with its space in every cabin.
type flight struct {
	ID              int64            `json:"id"`
	IsSelfTransfer  bool             `json:"isSelfTransfer"`
	Program         string           `json:"program"`
	Origin          place            `json:"origin"`
	Destination     place            `json:"destination"`
	DepartsAt       string           `json:"departsAt"`
	ArrivesAt       string           `json:"arrivesAt"`
	DurationMinutes int              `json:"durationMinutes"`
	Stopovers       int              `json:"stopovers"`
	Cabins          map[string]*fare `json:"cabins"`
	Legs            []leg            `json:"legs"`
	LastSeenAt      string           `json:"lastSeenAt"`
}

// place is an airport of the answer.
type place struct {
	Code string `json:"code"`
	Name string `json:"name"`
	City string `json:"city"`
}

// fare is a flight's space in one cabin, priced for one passenger.
type fare struct {
	CabinClass        string          `json:"cabinClass"`
	Points            int             `json:"points"`
	Seats             *int            `json:"seats"`
	Tax               float64         `json:"tax"`
	Currency          string          `json:"currency"`
	RBD               string          `json:"rbd"`
	IsDynamicPricing  bool            `json:"isDynamicPricing"`
	DynamicPoints     *int            `json:"dynamicPoints"`
	MixedCabinClasses json.RawMessage `json:"mixedCabinClasses"`
}

// leg is one flight of an itinerary, with local times without offsets.
type leg struct {
	DepartsAt       string `json:"departsAt"`
	ArrivesAt       string `json:"arrivesAt"`
	Duration        string `json:"duration"`
	LayoverDuration string `json:"layoverDuration"`
	FlightNumber    string `json:"flightNumber"`
	OperatedBy      string `json:"operatedBy"`
	AirlineCode     string `json:"airlineCode"`
	Equipment       string `json:"equipment"`
	Origin          place  `json:"origin"`
	Destination     place  `json:"destination"`
}

// cabinOrder are the finder's cabin names, in the order offers are given.
var cabinOrder = []string{"Economy", "PremiumEconomy", "Business", "First"}

// cabinOf maps a finder cabin name to the award cabin.
func cabinOf(name string) (award.Cabin, bool) {
	switch strings.ToLower(strings.ReplaceAll(name, " ", "")) {
	case "economy":
		return award.Economy, true
	case "premiumeconomy", "premium":
		return award.Premium, true
	case "business":
		return award.Business, true
	case "first":
		return award.First, true
	}
	return "", false
}

// offers are the offers of f, one per cabin it has space in, for q and
// booked at booking. A flight whose times cannot be placed gives none.
func offers(f flight, q award.Query, booking string) []award.Offer {
	if len(f.Legs) == 0 {
		return nil
	}
	times, ok := legTimes(f.Legs)
	if !ok {
		return nil
	}
	var out []award.Offer
	for _, name := range cabinOrder {
		fr := f.Cabins[name]
		if fr == nil || fr.Points <= 0 {
			continue
		}
		cabin, ok := cabinOf(name)
		if !ok {
			continue
		}
		out = append(out, offer(f, fr, cabin, times, q, booking))
	}
	return out
}

// offer is f's offer in cabin, priced by fr, with its legs placed at times.
func offer(
	f flight,
	fr *fare,
	cabin award.Cabin,
	times []legTime,
	q award.Query,
	booking string,
) award.Offer {
	pax := max(q.Passengers, 1)
	segCabins := legCabins(fr.MixedCabinClasses, len(f.Legs), cabin)
	codes := strings.Split(fr.RBD, ",")
	segs := make([]award.Segment, len(f.Legs))
	var airlines []string
	mixed := false
	for i, l := range f.Legs {
		code := ""
		if i < len(codes) {
			code = strings.TrimSpace(codes[i])
		}
		carrier := carrierOf(l)
		if !slices.Contains(airlines, carrier) {
			airlines = append(airlines, carrier)
		}
		if segCabins[i] != cabin {
			mixed = true
		}
		segs[i] = award.Segment{
			Flight:           strings.ReplaceAll(l.FlightNumber, " ", ""),
			MarketingCarrier: carrier,
			OperatingCarrier: carrier,
			Origin:           l.Origin.Code,
			Destination:      l.Destination.Code,
			Departure:        times[i].departure,
			Arrival:          times[i].arrival,
			DurationMinutes: int(
				times[i].arrival.Sub(times[i].departure).Minutes(),
			),
			LayoverMinutes: layoverMinutes(times, i),
			Aircraft:       l.Equipment,
			Cabin:          segCabins[i],
			BookingCode:    code,
		}
	}
	first, last := segs[0], segs[len(segs)-1]
	currency := currencyCode(fr.Currency)
	return award.Offer{
		Program:      Key,
		Availability: award.Estimated,
		Origin:       first.Origin,
		Destination:  last.Destination,
		Departure:    first.Departure,
		Arrival:      last.Arrival,
		DurationMinutes: int(
			last.Arrival.Sub(first.Departure).Minutes(),
		),
		Stops:      len(segs) - 1,
		Airlines:   airlines,
		Cabin:      cabin,
		MixedCabin: mixed,
		Fare:       "Classic Flight Reward " + cabinLabel(cabin),
		Price: award.Price{
			PointsPerPassenger: fr.Points,
			PointsTotal:        fr.Points * pax,
			TaxesPerPassenger: award.Money{
				Amount: fr.Tax, Currency: currency,
			},
			TaxesTotal: award.Money{
				Amount: fr.Tax * float64(pax), Currency: currency,
			},
		},
		Seats:      fr.Seats,
		BookingURL: booking,
		Notes:      notes(f, fr),
		Segments:   segs,
	}
}

// cabinLabel is the display name of cabin in a fare name.
func cabinLabel(c award.Cabin) string {
	switch c {
	case award.Premium:
		return "Premium Economy"
	case award.Business:
		return "Business"
	case award.First:
		return "First"
	}
	return "Economy"
}

// carrierOf is the IATA code of the airline flying l, read from its flight
// number when the answer leaves the airline code out.
func carrierOf(l leg) string {
	if l.AirlineCode != "" {
		return strings.ToUpper(l.AirlineCode)
	}
	if len(l.FlightNumber) >= 2 {
		return strings.ToUpper(l.FlightNumber[:2])
	}
	return ""
}

// layoverMinutes is the time between leg i's arrival and the next leg's
// departure, 0 for the last leg.
func layoverMinutes(times []legTime, i int) int {
	if i+1 >= len(times) {
		return 0
	}
	return int(times[i+1].departure.Sub(times[i].arrival).Minutes())
}

// legCabins are the cabins of n legs flown in a fare of cabin base. raw is
// the fare's mixedCabinClasses: null when every leg flies in base, else
// one finder cabin name per leg. Legs it does not name fly in base.
func legCabins(raw json.RawMessage, n int, base award.Cabin) []award.Cabin {
	out := make([]award.Cabin, n)
	for i := range out {
		out[i] = base
	}
	var names []string
	if len(raw) == 0 || json.Unmarshal(raw, &names) != nil {
		return out
	}
	for i, name := range names {
		if i >= n {
			break
		}
		if c, ok := cabinOf(name); ok {
			out[i] = c
		}
	}
	return out
}

// notes are what else the finder says about f in fare fr: when its space
// was last seen, dynamic pricing and a self-transfer.
func notes(f flight, fr *fare) []string {
	var out []string
	if f.LastSeenAt != "" {
		out = append(out, "Space last seen "+f.LastSeenAt+
			"; confirm on qantas.com before transferring points")
	}
	if fr.IsDynamicPricing && fr.DynamicPoints != nil {
		out = append(out, fmt.Sprintf(
			"Dynamically priced; %d points", *fr.DynamicPoints,
		))
	}
	if f.IsSelfTransfer {
		out = append(out, "Self-transfer between separate bookings")
	}
	return out
}

// currencySymbols map the finder's currency symbols to ISO codes.
var currencySymbols = map[string]string{
	"AU$": "AUD", "A$": "AUD", "NZ$": "NZD", "US$": "USD", "$": "USD",
	"CA$": "CAD", "C$": "CAD", "HK$": "HKD", "S$": "SGD", "€": "EUR",
	"£": "GBP", "¥": "JPY", "JP¥": "JPY", "CN¥": "CNY", "₹": "INR",
	"₩": "KRW", "R": "ZAR", "FJ$": "FJD", "NT$": "TWD",
}

// currencyCode is the ISO code of the finder's currency s, which is either
// a symbol such as "AU$" or already a code such as "DKK".
func currencyCode(s string) string {
	s = strings.TrimSpace(s)
	if code, ok := currencySymbols[s]; ok {
		return code
	}
	return strings.ToUpper(s)
}
