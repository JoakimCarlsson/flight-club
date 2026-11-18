// Package jetblue searches JetBlue award availability, priced in TrueBlue
// points. TrueBlue awards are revenue-based: every seat JetBlue sells can be
// bought with points, in every fare brand, so each flight is offered once per
// brand (Main Base, Main, Main Flex, EvenMore Base, EvenMore, EvenMore Flex,
// Mint Base, Mint, Mint Flex) that still has seats.
//
// The booking pages of jetblue.com sit behind Fastly's bot defence: a plain
// request to /booking/flights gets a "Client Challenge" page, and a plain POST
// to the search API gets an empty 406. The site's own search API,
// /api/ecom/cb-flight-search/v1/search/NGB, is therefore read the way the
// site reads it: the stealth browser opens the points search results page
// and the API's JSON answer is captured as the page loads it. The answer
// gives local times without offsets, so offsets come from a table of the
// airports JetBlue flies to, carried along a connection by each segment's
// flying time where an airport is missing from it.
//
// JetBlue does not search city codes, so a city code such as NYC is searched
// as each of its airports in turn.
package jetblue

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

// Key is the program key JetBlue offers carry.
const Key = "jetblue"

// resultsURL is the flight results page a traveller searches and books from.
const resultsURL = "https://www.jetblue.com/booking/flights"

// searchPath is the part of the search API's URL that identifies its answer
// among the responses the results page loads.
const searchPath = "/cb-flight-search/v1/search/"

// pointsCurrency is the currency the search API prices points in.
const pointsCurrency = "FFCURRENCY"

// localLayout is the layout of the search API's local, offset-less times.
const localLayout = "2006-01-02T15:04:05"

// loadTimeout bounds the navigation to the results page.
const loadTimeout = 60 * time.Second

// settleRounds and settleDelay are how long the results page is given, after
// it loads, to call the search API: settleRounds waits of settleDelay each.
const (
	settleRounds = 4
	settleDelay  = 4 * time.Second
)

// Program searches jetblue.com through a stealth browser.
type Program struct {
	browser *browser.Browser
}

// New builds a Program that loads jetblue.com in b.
func New(b *browser.Browser) *Program {
	return &Program{browser: b}
}

// Key returns the program key, "jetblue".
func (p *Program) Key() string {
	return Key
}

// Search returns JetBlue's award offers for q, searching every airport of a
// city code. It returns an error when the browser cannot load the results
// page, the page is stopped by the bot challenge, or the search API answers
// with something that cannot be read or reports a failure. A route JetBlue
// does not sell gives no offers and no error.
func (p *Program) Search(
	ctx context.Context,
	q award.Query,
) ([]award.Offer, error) {
	out := []award.Offer{}
	for _, from := range airports(q.Origin) {
		for _, to := range airports(q.Destination) {
			if from == to || !serves(from) || !serves(to) {
				continue
			}
			leg := q
			leg.Origin, leg.Destination = from, to
			found, err := p.searchAirports(ctx, leg)
			if err != nil {
				return nil, err
			}
			out = append(out, found...)
		}
	}
	return out, nil
}

// serves reports whether JetBlue flies to airport, which is whether the
// airport is in its network's time zone table. A search for an airport
// outside it is skipped rather than costing a browser page load.
func serves(airport string) bool {
	_, ok := zones[airport]
	return ok
}

// searchAirports runs one search for q, whose origin and destination are
// airport codes. It returns the same errors as Search.
func (p *Program) searchAirports(
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
			return strings.Contains(u, searchPath)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("jetblue: %w", err)
	}
	if len(captured.Bodies) == 0 {
		if strings.Contains(captured.HTML, "Client Challenge") {
			return nil, errChallenged
		}
		return []award.Offer{}, nil
	}
	res, err := parseAnswer(captured.Bodies[len(captured.Bodies)-1].Data)
	if err != nil {
		return nil, err
	}
	return offers(res, q.Passengers, booking), nil
}

// bookingURL is the points results page for a one-way search for q.
func bookingURL(q award.Query) string {
	v := url.Values{}
	v.Set("from", q.Origin)
	v.Set("to", q.Destination)
	v.Set("depart", q.Date.Format(time.DateOnly))
	v.Set("isMultiCity", "false")
	v.Set("noOfRoute", "1")
	v.Set("adults", strconv.Itoa(max(q.Passengers, 1)))
	v.Set("children", "0")
	v.Set("infants", "0")
	v.Set("sharedMarket", "false")
	v.Set("roundTripFaresFlag", "false")
	v.Set("usePoints", "true")
	return resultsURL + "?" + v.Encode()
}

// cityAirports are the airports of the city codes JetBlue serves more than
// one airport of, or serves under an airport code only.
var cityAirports = map[string][]string{
	"NYC": {"JFK", "LGA", "EWR"},
	"WAS": {"DCA", "IAD", "BWI"},
	"CHI": {"ORD"},
	"LON": {"LHR", "LGW"},
	"PAR": {"CDG"},
	"DUB": {"DUB"},
	"AMS": {"AMS"},
	"MIL": {"MXP"},
	"ROM": {"FCO"},
	"STO": {"ARN"},
	"YTO": {"YYZ"},
	"YMQ": {"YUL"},
	"MEX": {"MEX"},
	"BUE": {"EZE"},
	"SAO": {"GRU"},
	"TYO": {"NRT", "HND"},
}

// airports returns the airports to search for code: those of a city code it
// knows, or code itself.
func airports(code string) []string {
	if a, ok := cityAirports[code]; ok {
		return a
	}
	return []string{code}
}

// answer is the search API's answer.
type answer struct {
	// Status says whether the search ran.
	Status struct {
		// TransactionStatus is "success" for a search that ran.
		TransactionStatus string `json:"transactionStatus"`
	} `json:"status"`
	// Data holds the results.
	Data struct {
		// SearchResults has one entry per searched leg.
		SearchResults []searchResult `json:"searchResults"`
		// BrandsOffered names the fare brands of the results.
		BrandsOffered []struct {
			// BrandID is the two-letter brand code, e.g. "DN".
			BrandID string `json:"brandId"`
		} `json:"brandsOffered"`
	} `json:"data"`
}

// searchResult is the result of one leg.
type searchResult struct {
	// ProductOffers are the itineraries with their offers.
	ProductOffers []productOffer `json:"productOffers"`
}

// productOffer is one itinerary and its price in every brand with seats.
type productOffer struct {
	// OriginAndDestination holds the itinerary, one entry for a one-way.
	OriginAndDestination []itinerary `json:"originAndDestination"`
	// Offers are its prices, one per fare brand.
	Offers []fareOffer `json:"offers"`
}

// itinerary is one itinerary.
type itinerary struct {
	// Departure is where and when it leaves.
	Departure point `json:"departure"`
	// Arrival is where and when it lands.
	Arrival point `json:"arrival"`
	// Stops is how many connections it makes.
	Stops int `json:"stops"`
	// TotalDuration is the door-to-door time in minutes.
	TotalDuration int `json:"totalDuration"`
	// FlightSegments are its flights, in order.
	FlightSegments []flightSegment `json:"flightSegments"`
	// AwardBooking is set when it can be booked with points.
	AwardBooking bool `json:"awardBooking"`
}

// point is an airport and a local time.
type point struct {
	// Date is the local time, e.g. "2026-12-16T06:00:00".
	Date string `json:"date"`
	// Airport is the IATA airport code.
	Airport string `json:"airport"`
	// Terminal is the terminal, empty when unknown.
	Terminal string `json:"terminal"`
}

// flightSegment is one flight of an itinerary.
type flightSegment struct {
	// Departure is where and when it leaves.
	Departure point `json:"departure"`
	// Arrival is where and when it lands.
	Arrival point `json:"arrival"`
	// Duration is the time in the air in minutes.
	Duration int `json:"duration"`
	// LayoverDuration is the connection time after it in minutes.
	LayoverDuration int `json:"layoverDuration"`
	// FlightInfo names the carriers and flight numbers.
	FlightInfo struct {
		// MarketingFlightNumber is the flight number it is sold under.
		MarketingFlightNumber int `json:"marketingFlightNumber"`
		// OperatingFlightNumber is the operating carrier's flight number.
		OperatingFlightNumber int `json:"operatingFlightNumber"`
		// OperatingAirlineCode is the IATA code of the airline flying it.
		OperatingAirlineCode string `json:"operatingAirlineCode"`
		// MarketingAirlineCode is the IATA code of the airline selling it.
		MarketingAirlineCode string `json:"marketingAirlineCode"`
	} `json:"flightInfo"`
	// Aircraft is JetBlue's aircraft type code, e.g. "32Q".
	Aircraft string `json:"aircraft"`
}

// fareOffer is an itinerary's price in one fare brand.
type fareOffer struct {
	// Brand names the fare brand.
	Brand struct {
		// BrandID is the two-letter brand code, e.g. "DN" for Main Base.
		BrandID string `json:"brandId"`
	} `json:"brand"`
	// CabinClass is the cabin the API files the offer under.
	CabinClass string `json:"cabinClass"`
	// SoldOut is set when no seat is left at this price.
	SoldOut bool `json:"soldOut"`
	// SeatsRemaining says how many seats are left, nil when not said.
	SeatsRemaining *struct {
		// Count is how many seats are left.
		Count int `json:"count"`
		// LowAvailability is set when few seats are left.
		LowAvailability bool `json:"lowAvailability"`
	} `json:"seatsRemaining"`
	// Price is the points price and the taxes, both per passenger: one
	// entry in pointsCurrency and one in a cash currency.
	Price []struct {
		// Amount is the points, or the cash in the major unit.
		Amount float64 `json:"amount"`
		// Currency is pointsCurrency or an ISO 4217 code.
		Currency string `json:"currency"`
	} `json:"price"`
	// OfferSegmentInfo is how each segment is booked, in segment order.
	OfferSegmentInfo []struct {
		// CabinClass is the segment's cabin: Economy, Business or First.
		CabinClass string `json:"cabinClass"`
		// BookingClass is the fare class letter.
		BookingClass string `json:"bookingClass"`
		// FareBasis is the fare basis code.
		FareBasis string `json:"fareBasis"`
	} `json:"offerSegmentInfo"`
}

// errChallenged is returned when jetblue.com answers with its bot challenge
// instead of the results page.
var errChallenged = errors.New(
	"jetblue: stopped by the site's bot challenge",
)

// parseAnswer decodes a search API answer. It returns an error when body is
// not the answer's JSON or the answer says the search did not succeed.
func parseAnswer(body []byte) (answer, error) {
	var a answer
	if err := json.Unmarshal(body, &a); err != nil {
		return answer{}, fmt.Errorf("jetblue: decode answer: %w", err)
	}
	if s := a.Status.TransactionStatus; s != "success" {
		return answer{}, fmt.Errorf("jetblue: search status %q", s)
	}
	return a, nil
}

// offers turns every unsold-out brand of every itinerary in a into an offer
// for passengers, booked at booking.
func offers(a answer, passengers int, booking string) []award.Offer {
	out := []award.Offer{}
	passengers = max(passengers, 1)
	for _, res := range a.Data.SearchResults {
		for _, po := range res.ProductOffers {
			if len(po.OriginAndDestination) == 0 {
				continue
			}
			it := po.OriginAndDestination[0]
			times, ok := itineraryTimes(it)
			if !ok {
				continue
			}
			for _, fo := range po.Offers {
				o, ok := offer(it, times, fo, passengers, booking)
				if ok {
					out = append(out, o)
				}
			}
		}
	}
	return out
}

// segmentTimes are a segment's departure and arrival with their offsets.
type segmentTimes struct {
	departure time.Time
	arrival   time.Time
}

// offer builds the offer for itinerary it, whose segments leave and land at
// times, priced in fare offer fo for passengers and booked at booking. It
// returns false when fo is sold out or has no points price.
func offer(
	it itinerary,
	times []segmentTimes,
	fo fareOffer,
	passengers int,
	booking string,
) (award.Offer, bool) {
	if fo.SoldOut || len(it.FlightSegments) == 0 {
		return award.Offer{}, false
	}
	points, taxes, ok := prices(fo)
	if !ok {
		return award.Offer{}, false
	}
	b := brandOf(fo.Brand.BrandID)
	segs := make([]award.Segment, len(it.FlightSegments))
	var airlines []string
	mixed := false
	for i, s := range it.FlightSegments {
		fi := s.FlightInfo
		seg := award.Segment{
			Flight: fi.MarketingAirlineCode +
				strconv.Itoa(fi.MarketingFlightNumber),
			MarketingCarrier: fi.MarketingAirlineCode,
			OperatingCarrier: fi.OperatingAirlineCode,
			Origin:           s.Departure.Airport,
			Destination:      s.Arrival.Airport,
			Departure:        times[i].departure,
			Arrival:          times[i].arrival,
			DurationMinutes:  s.Duration,
			LayoverMinutes:   s.LayoverDuration,
			Aircraft:         s.Aircraft,
			Cabin:            b.cabin,
		}
		if i < len(fo.OfferSegmentInfo) {
			info := fo.OfferSegmentInfo[i]
			seg.Cabin = segmentCabin(info.CabinClass, b.cabin)
			seg.BookingCode = info.BookingClass
		}
		if seg.Cabin != b.cabin {
			mixed = true
		}
		if seg.OperatingCarrier == "" {
			seg.OperatingCarrier = seg.MarketingCarrier
		}
		if !slices.Contains(airlines, seg.OperatingCarrier) {
			airlines = append(airlines, seg.OperatingCarrier)
		}
		segs[i] = seg
	}
	var seats *int
	if sr := fo.SeatsRemaining; sr != nil && sr.Count > 0 {
		n := sr.Count
		seats = &n
	}
	last := len(segs) - 1
	return award.Offer{
		Program:         Key,
		Availability:    award.Confirmed,
		Origin:          segs[0].Origin,
		Destination:     segs[last].Destination,
		Departure:       segs[0].Departure,
		Arrival:         segs[last].Arrival,
		DurationMinutes: it.TotalDuration,
		Stops:           it.Stops,
		Airlines:        airlines,
		Cabin:           b.cabin,
		MixedCabin:      mixed,
		Fare:            b.name,
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
		Notes:      notes(it, fo, b),
		Segments:   segs,
	}, true
}

// prices reads fo's points price and taxes per passenger. It returns false
// when fo has no points price.
func prices(fo fareOffer) (int, award.Money, bool) {
	points := 0
	taxes := award.Money{Currency: "USD"}
	for _, p := range fo.Price {
		if p.Currency == pointsCurrency {
			points = int(p.Amount)
			continue
		}
		taxes = award.Money{Amount: p.Amount, Currency: p.Currency}
	}
	return points, taxes, points > 0
}

// roundCents rounds v to two decimals.
func roundCents(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}

// notes are what the site says about an offer beyond its price: its brand
// code and fare basis, refundability, few seats left, partner flights and
// whether the itinerary is bookable with points.
func notes(it itinerary, fo fareOffer, b brand) []string {
	var out []string
	code := "brand " + fo.Brand.BrandID
	var bases []string
	for _, s := range fo.OfferSegmentInfo {
		if s.FareBasis != "" && !slices.Contains(bases, s.FareBasis) {
			bases = append(bases, s.FareBasis)
		}
	}
	if len(bases) > 0 {
		code += ", fare basis " + strings.Join(bases, "/")
	}
	out = append(out, code)
	if b.refundable {
		out = append(out, "Refundable fare")
	}
	if fo.SeatsRemaining != nil && fo.SeatsRemaining.LowAvailability {
		out = append(out, "Few seats left at this price")
	}
	for _, s := range it.FlightSegments {
		if op := s.FlightInfo.OperatingAirlineCode; op != "" &&
			op != s.FlightInfo.MarketingAirlineCode {
			out = append(out, fmt.Sprintf("%s%d operated by %s",
				s.FlightInfo.MarketingAirlineCode,
				s.FlightInfo.MarketingFlightNumber, op))
		}
	}
	if !it.AwardBooking {
		out = append(out, "Itinerary not flagged as bookable with points")
	}
	return out
}

// brand is what a fare brand code means.
type brand struct {
	name       string
	cabin      award.Cabin
	refundable bool
}

// brands are JetBlue's fare brands by code, named as jetblue.com names them.
var brands = map[string]brand{
	"DN": {"Main Base", award.Economy, false},
	"AN": {"Main", award.Economy, false},
	"AR": {"Main", award.Economy, true},
	"GR": {"Main Flex", award.Economy, true},
	"DE": {"EvenMore Base", award.Economy, false},
	"EN": {"EvenMore", award.Economy, false},
	"ER": {"EvenMore Flex", award.Economy, true},
	"DM": {"Mint Base", award.Business, false},
	"MN": {"Mint", award.Business, false},
	"MR": {"Mint Flex", award.Business, true},
}

// brandOf returns the brand for code, or an economy brand named by the code
// itself when the code is unknown.
func brandOf(code string) brand {
	if b, ok := brands[code]; ok {
		return b
	}
	return brand{name: code, cabin: award.Economy}
}

// segmentCabin maps the API's per-segment cabin name to a cabin, returning
// fallback for a name it does not know. JetBlue's Mint, filed as First or
// Business, is a lie-flat business cabin.
func segmentCabin(name string, fallback award.Cabin) award.Cabin {
	switch strings.ToLower(name) {
	case "economy", "coach":
		return award.Economy
	case "premium", "premium economy":
		return award.Premium
	case "business", "first":
		return award.Business
	}
	return fallback
}

// localTimes are a segment's offset-less departure and arrival.
type localTimes struct {
	dep time.Time
	arr time.Time
}

// itineraryTimes gives every segment of it its departure and arrival with
// the airports' offsets. An airport missing from zones gets its offset from
// a neighbouring segment's offset, local times and flying time. It returns
// false when a time cannot be parsed or no offset can be found.
func itineraryTimes(it itinerary) ([]segmentTimes, bool) {
	locals := make([]localTimes, len(it.FlightSegments))
	offsets := map[string]int{}
	for i, s := range it.FlightSegments {
		dep, err1 := time.Parse(localLayout, s.Departure.Date)
		arr, err2 := time.Parse(localLayout, s.Arrival.Date)
		if err1 != nil || err2 != nil {
			return nil, false
		}
		locals[i] = localTimes{dep, arr}
		for _, ap := range []string{s.Departure.Airport, s.Arrival.Airport} {
			if off, ok := zoneOffset(ap, dep); ok {
				offsets[ap] = off
			}
		}
	}
	for range it.FlightSegments {
		for i, s := range it.FlightSegments {
			shift := int(locals[i].arr.Sub(locals[i].dep).Seconds()) -
				s.Duration*60
			from, to := s.Departure.Airport, s.Arrival.Airport
			fo, fok := offsets[from]
			tof, tok := offsets[to]
			switch {
			case fok && !tok:
				offsets[to] = fo + shift
			case tok && !fok:
				offsets[from] = tof - shift
			}
		}
	}
	out := make([]segmentTimes, len(it.FlightSegments))
	for i, s := range it.FlightSegments {
		fo, fok := offsets[s.Departure.Airport]
		tof, tok := offsets[s.Arrival.Airport]
		if !fok || !tok {
			return nil, false
		}
		out[i] = segmentTimes{
			departure: inOffset(locals[i].dep, s.Departure.Airport, fo),
			arrival:   inOffset(locals[i].arr, s.Arrival.Airport, tof),
		}
	}
	return out, true
}

// inOffset reads the offset-less local time t as a time at airport, whose
// offset is offset seconds east of UTC.
func inOffset(t time.Time, airport string, offset int) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(),
		t.Second(), 0, time.FixedZone(airport, offset))
}

// zoneOffset returns the UTC offset in seconds of airport around local time
// t, and false when the airport's zone is not known or cannot be loaded.
func zoneOffset(airport string, t time.Time) (int, bool) {
	name, ok := zones[airport]
	if !ok {
		return 0, false
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return 0, false
	}
	_, off := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(),
		0, 0, loc).Zone()
	return off, true
}

// zones are the IANA time zones of the airports JetBlue flies to.
var zones = map[string]string{
	"JFK": "America/New_York", "LGA": "America/New_York",
	"EWR": "America/New_York", "BOS": "America/New_York",
	"BDL": "America/New_York", "PVD": "America/New_York",
	"HPN": "America/New_York", "SWF": "America/New_York",
	"ALB": "America/New_York", "BUF": "America/New_York",
	"ROC": "America/New_York", "SYR": "America/New_York",
	"BTV": "America/New_York", "PWM": "America/New_York",
	"BGR": "America/New_York", "ACK": "America/New_York",
	"MVY": "America/New_York", "HYA": "America/New_York",
	"PHL": "America/New_York", "PIT": "America/New_York",
	"BWI": "America/New_York", "DCA": "America/New_York",
	"IAD": "America/New_York", "RIC": "America/New_York",
	"ORF": "America/New_York", "RDU": "America/New_York",
	"CLT": "America/New_York", "CHS": "America/New_York",
	"SAV": "America/New_York", "ATL": "America/New_York",
	"JAX": "America/New_York", "MCO": "America/New_York",
	"TPA": "America/New_York", "RSW": "America/New_York",
	"SRQ": "America/New_York", "PBI": "America/New_York",
	"FLL": "America/New_York", "MIA": "America/New_York",
	"DTW": "America/Detroit", "CLE": "America/New_York",
	"CMH": "America/New_York", "SJU": "America/Puerto_Rico",
	"BQN": "America/Puerto_Rico", "PSE": "America/Puerto_Rico",
	"STT": "America/St_Thomas", "STX": "America/St_Thomas",
	"ORD": "America/Chicago", "MSY": "America/Chicago",
	"AUS": "America/Chicago", "HOU": "America/Chicago",
	"IAH": "America/Chicago", "DFW": "America/Chicago",
	"MSP": "America/Chicago", "NAS": "America/Nassau",
	"DEN": "America/Denver", "PHX": "America/Phoenix",
	"LAS": "America/Los_Angeles", "LAX": "America/Los_Angeles",
	"LGB": "America/Los_Angeles", "BUR": "America/Los_Angeles",
	"ONT": "America/Los_Angeles", "SAN": "America/Los_Angeles",
	"PSP": "America/Los_Angeles", "SFO": "America/Los_Angeles",
	"OAK": "America/Los_Angeles", "SJC": "America/Los_Angeles",
	"SMF": "America/Los_Angeles", "SEA": "America/Los_Angeles",
	"PDX": "America/Los_Angeles", "SLC": "America/Denver",
	"RNO": "America/Los_Angeles", "EGE": "America/Denver",
	"JAC": "America/Denver", "BZN": "America/Denver",
	"HDN": "America/Denver", "ANC": "America/Anchorage",
	"YYZ": "America/Toronto", "YUL": "America/Toronto",
	"YVR": "America/Vancouver",
	"MEX": "America/Mexico_City", "CUN": "America/Cancun",
	"GDL": "America/Mexico_City", "PVR": "America/Mexico_City",
	"SJD": "America/Mazatlan", "LIR": "America/Costa_Rica",
	"SJO": "America/Costa_Rica", "GUA": "America/Guatemala",
	"SAL": "America/El_Salvador", "PTY": "America/Panama",
	"BOG": "America/Bogota", "MDE": "America/Bogota",
	"CTG": "America/Bogota", "LIM": "America/Lima",
	"GYE": "America/Guayaquil", "UIO": "America/Guayaquil",
	"EZE": "America/Argentina/Buenos_Aires",
	"GRU": "America/Sao_Paulo", "SCL": "America/Santiago",
	"AUA": "America/Aruba", "CUR": "America/Curacao",
	"BGI": "America/Barbados", "ANU": "America/Antigua",
	"SXM": "America/Lower_Princes", "SKB": "America/St_Kitts",
	"UVF": "America/St_Lucia", "GND": "America/Grenada",
	"POS": "America/Port_of_Spain", "BDA": "Atlantic/Bermuda",
	"KIN": "America/Jamaica", "MBJ": "America/Jamaica",
	"SDQ": "America/Santo_Domingo",
	"STI": "America/Santo_Domingo", "POP": "America/Santo_Domingo",
	"PUJ": "America/Santo_Domingo", "LRM": "America/Santo_Domingo",
	"SNU": "America/Havana", "HAV": "America/Havana",
	"PAP": "America/Port-au-Prince", "CAP": "America/Port-au-Prince",
	"GCM": "America/Cayman", "PLS": "America/Grand_Turk",
	"EIS": "America/Tortola", "FDF": "America/Martinique",
	"PTP": "America/Guadeloupe", "GEO": "America/Guyana",
	"PBM": "America/Paramaribo", "RTB": "America/Tegucigalpa",
	"SAP": "America/Tegucigalpa", "TGU": "America/Tegucigalpa",
	"BZE": "America/Belize", "LHR": "Europe/London",
	"LGW": "Europe/London", "DUB": "Europe/Dublin",
	"EDI": "Europe/London", "CDG": "Europe/Paris",
	"AMS": "Europe/Amsterdam", "MAD": "Europe/Madrid",
	"BCN": "Europe/Madrid", "LIS": "Europe/Lisbon",
	"FCO": "Europe/Rome", "MXP": "Europe/Rome",
	"ATH": "Europe/Athens", "NCE": "Europe/Paris",
}
