// Package sas searches SAS award availability, priced in EuroBonus points.
// City codes such as STO or NYC are searched as every airport of the city.
//
// flysas.com's booking page loads its flights from the offers API at
// /api/offers/flights, which answers a plain GET without a session, a cookie
// or a login. With bookingFlow=points it prices every flight of the day in
// points: the "BONUS" products (ECONOMY BONUS, BUSINESS BONUS) are classic
// fixed-price award seats, and the other products (LIGHT, STANDARD, FLEX,
// PREMIUM FLEX, ...) are the cash fares sold for a dynamic number of points.
// Both are live and bookable. Taxes are priced in the currency of the point
// of sale, DKK for the Danish one used here.
package sas

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/joakimcarlsson/flight-club/internal/award"
)

// Key is the program key SAS offers carry.
const Key = "sas"

// apiURL is the offers API the booking page searches flights with.
const apiURL = "https://www.flysas.com/api/offers/flights"

// bookURL is the booking page a traveller picks and books a flight on.
const bookURL = "https://www.flysas.com/en/book/flights"

// pointOfSale is the market the search is priced in; it sets the tax
// currency.
const pointOfSale = "dk"

// userAgent is a current desktop Chrome, which the site answers like a
// browser.
const userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36"

// maxBody is the most of an answer that is read, well above the few hundred
// kilobytes a busy route answers with.
const maxBody = 16 << 20

// noAvailability are the API's error codes for a search that found no
// flights: 225050 for a route with no seats that day and 225036 for a
// route SAS does not sell. Both are no availability rather than a failure.
var noAvailability = map[string]bool{"225050": true, "225036": true}

// Program searches flysas.com.
type Program struct {
	client *http.Client
}

// New builds a Program that makes its requests with a copy of client whose
// transport offers only classic TLS key exchanges. flysas.com sits behind
// Cloudflare, which denies Go's default TLS handshake (it offers the
// post-quantum X25519MLKEM768 key share) with a "Denied boarding" 403
// while it answers the same request over a classic handshake. client's
// timeout, cookie jar and redirect policy are kept; a transport that is not
// an *http.Transport is used as it is.
func New(client *http.Client) *Program {
	c := *client
	c.Transport = classicTLS(client.Transport)
	return &Program{client: &c}
}

// classicTLS returns a copy of rt, or of http.DefaultTransport when rt is
// nil, that prefers only the X25519 and P-256 key exchanges. It returns rt
// unchanged when it is not an *http.Transport.
func classicTLS(rt http.RoundTripper) http.RoundTripper {
	if rt == nil {
		rt = http.DefaultTransport
	}
	t, ok := rt.(*http.Transport)
	if !ok {
		return rt
	}
	t = t.Clone()
	if t.TLSClientConfig == nil {
		t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	t.TLSClientConfig.CurvePreferences = []tls.CurveID{
		tls.X25519, tls.CurveP256,
	}
	return t
}

// Key returns the program key, "sas".
func (p *Program) Key() string {
	return Key
}

// Search returns SAS's points offers for q. It returns an error when the
// site cannot be reached, answers with a status other than 200, answers
// with something that is not the offers JSON, or reports an error other
// than having no availability.
func (p *Program) Search(
	ctx context.Context,
	q award.Query,
) ([]award.Offer, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		searchURL(q),
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("sas: building request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Referer", bookingURL(q))

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sas: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sas: answered %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("sas: reading answer: %w", err)
	}

	res, err := parseResults(body)
	if err != nil {
		return nil, err
	}
	return offers(res, q.Passengers, bookingURL(q)), nil
}

// searchURL is the offers API URL for a one-way points search for q.
func searchURL(q award.Query) string {
	v := url.Values{}
	v.Set("from", q.Origin)
	v.Set("to", q.Destination)
	v.Set("outDate", q.Date.Format("20060102"))
	v.Set("adt", strconv.Itoa(q.Passengers))
	v.Set("bookingFlow", "points")
	v.Set("pos", pointOfSale)
	v.Set("channel", "web")
	v.Set("displayType", "upsell")
	return apiURL + "?" + v.Encode()
}

// bookingURL is the booking page for a one-way points search for q.
func bookingURL(q award.Query) string {
	v := url.Values{}
	v.Set("search", fmt.Sprintf("OW_%s-%s-%s_a%dc0i0y0",
		q.Origin, q.Destination, q.Date.Format("20060102"), q.Passengers))
	v.Set("view", "upsell")
	v.Set("bookingFlow", "points")
	return bookURL + "?" + v.Encode()
}

// results is the offers API's answer.
type results struct {
	// Errors are set instead of flights when the search failed or found
	// nothing.
	Errors []apiError `json:"errors"`
	// Currency is the currency taxes are priced in.
	Currency struct {
		// Code is the ISO 4217 code.
		Code string `json:"code"`
	} `json:"currency"`
	// OutboundFlights are the itineraries keyed by an id such as "F0".
	OutboundFlights map[string]flight `json:"outboundFlights"`
}

// apiError is one error the API reports.
type apiError struct {
	// ErrorCode is the API's code, e.g. "225050" for no availability.
	ErrorCode string `json:"errorCode"`
	// ErrorMessage says what went wrong.
	ErrorMessage string `json:"errorMessage"`
}

// flight is one itinerary.
type flight struct {
	// Origin is the airport it departs from.
	Origin place `json:"origin"`
	// Destination is the airport it arrives at.
	Destination place `json:"destination"`
	// ConnectionDuration is the door-to-door time as "HH:MM:SS".
	ConnectionDuration string `json:"connectionDuration"`
	// Stops is how many connections it makes.
	Stops int `json:"stops"`
	// Segments are its flights, in order.
	Segments []segment `json:"segments"`
	// Cabins are its products keyed by cabin ("ECONOMY", "PREMIUM",
	// "BUSINESS"), then by product name, e.g. "ECONOMY BONUS".
	Cabins map[string]map[string]productGroup `json:"cabins"`
}

// place is an airport, city or aircraft with a code and a name.
type place struct {
	// Code is the IATA code.
	Code string `json:"code"`
	// Name is the display name.
	Name string `json:"name"`
}

// segment is one flight of an itinerary.
type segment struct {
	// ID identifies the segment within its flight's fares.
	ID int `json:"id"`
	// DepartureAirport is the airport it departs from.
	DepartureAirport place `json:"departureAirport"`
	// ArrivalAirport is the airport it arrives at.
	ArrivalAirport place `json:"arrivalAirport"`
	// DepartureDateTimeInLocal is RFC 3339 in the origin's offset.
	DepartureDateTimeInLocal time.Time `json:"departureDateTimeInLocal"`
	// ArrivalDateTimeInLocal is RFC 3339 in the destination's offset.
	ArrivalDateTimeInLocal time.Time `json:"arrivalDateTimeInLocal"`
	// AirCraft is the aircraft type.
	AirCraft place `json:"airCraft"`
	// FlightNumber is the flight number without the airline code.
	FlightNumber string `json:"flightNumber"`
	// Duration is the time in the air as "HH:MM:SS".
	Duration string `json:"duration"`
	// MarketingCarrier is the airline selling it.
	MarketingCarrier place `json:"marketingCarrier"`
	// OperatingCarrier is the airline flying it, absent when it is the
	// marketing carrier.
	OperatingCarrier *place `json:"operatingCarrier"`
}

// productGroup holds the products of one product name.
type productGroup struct {
	// Products are keyed by an id such as "O_32".
	Products map[string]product `json:"products"`
}

// product is one flight's price in one fare product.
type product struct {
	// ProductName is SAS's fare name, e.g. "ECONOMY BONUS" or "LIGHT".
	ProductName string `json:"productName"`
	// IsStandardAward is set on fixed-price award seats.
	IsStandardAward bool `json:"isStandardAward"`
	// Price is the price for every passenger.
	Price price `json:"price"`
	// Fares are the booking per segment.
	Fares []fare `json:"fares"`
}

// price is a points and cash price.
type price struct {
	// Currency is the currency taxes are priced in.
	Currency string `json:"currency"`
	// TotalTax is the taxes and fees.
	TotalTax float64 `json:"totalTax"`
	// Points is the points price.
	Points int `json:"points"`
	// PricePerPassengerType is the price per passenger of each type.
	PricePerPassengerType []paxPrice `json:"pricePerPassengerType"`
}

// paxPrice is the price of one passenger type.
type paxPrice struct {
	// Type is the passenger type, "ADT" for adults.
	Type string `json:"type"`
	// Price is the price for one passenger of the type.
	Price struct {
		// TotalTax is the taxes and fees for one passenger.
		TotalTax float64 `json:"totalTax"`
		// Points is the points price for one passenger.
		Points int `json:"points"`
	} `json:"price"`
}

// fare is how one segment is booked in a product.
type fare struct {
	// SegmentID is the ID of the segment it describes.
	SegmentID string `json:"segmentId"`
	// BookingClass is the fare class letter.
	BookingClass string `json:"bookingClass"`
	// AvlSeats is how many seats are left in it.
	AvlSeats int `json:"avlSeats"`
}

// parseResults decodes an offers API answer. It returns an error when body
// is not JSON or carries an error other than no availability; no
// availability yields results without flights.
func parseResults(body []byte) (results, error) {
	var res results
	if err := json.Unmarshal(body, &res); err != nil {
		return results{}, fmt.Errorf("sas: reading answer: %w", err)
	}
	if len(res.Errors) == 0 {
		return res, nil
	}
	if e := res.Errors[0]; !noAvailability[e.ErrorCode] {
		return results{}, fmt.Errorf("sas: %s (%s)", e.ErrorMessage,
			e.ErrorCode)
	}
	return results{}, nil
}

// offers turns every product of every flight in res into an offer for
// passengers travellers, booked at booking.
func offers(res results, passengers int, booking string) []award.Offer {
	out := []award.Offer{}
	ids := make([]string, 0, len(res.OutboundFlights))
	for id := range res.OutboundFlights {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		f := res.OutboundFlights[id]
		if len(f.Segments) == 0 {
			continue
		}
		for cabinName, groups := range f.Cabins {
			for _, g := range groups {
				for _, pr := range g.Products {
					out = append(out, offer(f, cabinName, pr,
						res.Currency.Code, passengers, booking))
				}
			}
		}
	}
	return out
}

// offer builds the offer for flight f in cabin cabinName priced in product
// pr, for passengers travellers, with taxes in currency unless the product
// names its own, booked at booking.
func offer(
	f flight,
	cabinName string,
	pr product,
	currency string,
	passengers int,
	booking string,
) award.Offer {
	cabin := mapCabin(cabinName)
	if pr.Price.Currency != "" {
		currency = pr.Price.Currency
	}
	bookings := make(map[string]fare, len(pr.Fares))
	seats := -1
	for _, fr := range pr.Fares {
		bookings[fr.SegmentID] = fr
		if seats < 0 || fr.AvlSeats < seats {
			seats = fr.AvlSeats
		}
	}
	segments := make([]award.Segment, 0, len(f.Segments))
	var airlines []string
	for i, s := range f.Segments {
		operating := s.MarketingCarrier.Code
		if s.OperatingCarrier != nil && s.OperatingCarrier.Code != "" {
			operating = s.OperatingCarrier.Code
		}
		if !slices.Contains(airlines, operating) {
			airlines = append(airlines, operating)
		}
		layover := 0
		if i < len(f.Segments)-1 {
			next := f.Segments[i+1].DepartureDateTimeInLocal
			layover = int(next.Sub(s.ArrivalDateTimeInLocal).Minutes())
		}
		segments = append(segments, award.Segment{
			Flight:           s.MarketingCarrier.Code + s.FlightNumber,
			MarketingCarrier: s.MarketingCarrier.Code,
			OperatingCarrier: operating,
			Origin:           s.DepartureAirport.Code,
			Destination:      s.ArrivalAirport.Code,
			Departure:        s.DepartureDateTimeInLocal,
			Arrival:          s.ArrivalDateTimeInLocal,
			DurationMinutes:  minutes(s.Duration),
			LayoverMinutes:   layover,
			Aircraft:         s.AirCraft.Name,
			Cabin:            cabin,
			BookingCode:      bookings[strconv.Itoa(s.ID)].BookingClass,
		})
	}
	perPoints, perTax := perPassenger(pr.Price, passengers)
	o := award.Offer{
		Program:         Key,
		Availability:    award.Confirmed,
		Origin:          f.Origin.Code,
		Destination:     f.Destination.Code,
		Departure:       segments[0].Departure,
		Arrival:         segments[len(segments)-1].Arrival,
		DurationMinutes: minutes(f.ConnectionDuration),
		Stops:           len(segments) - 1,
		Airlines:        airlines,
		Cabin:           cabin,
		Fare:            pr.ProductName,
		Price: award.Price{
			PointsPerPassenger: perPoints,
			PointsTotal:        pr.Price.Points,
			TaxesPerPassenger: award.Money{
				Amount: perTax, Currency: currency,
			},
			TaxesTotal: award.Money{
				Amount: pr.Price.TotalTax, Currency: currency,
			},
		},
		BookingURL: booking,
		Notes:      notes(f, pr),
		Segments:   segments,
	}
	if seats >= 0 {
		o.Seats = &seats
	}
	return o
}

// perPassenger returns the points and taxes for one adult of p, from its
// adult price when it has one and otherwise p split over passengers.
func perPassenger(p price, passengers int) (int, float64) {
	for _, pp := range p.PricePerPassengerType {
		if pp.Type == "ADT" {
			return pp.Price.Points, pp.Price.TotalTax
		}
	}
	n := max(passengers, 1)
	return p.Points / n, p.TotalTax / float64(n)
}

// notes are what the site says about an offer beyond its price: whether it
// is a fixed-price award seat or a cash fare priced in points, and which
// airlines other than SAS operate it.
func notes(f flight, pr product) []string {
	var out []string
	if pr.IsStandardAward || strings.Contains(pr.ProductName, "BONUS") {
		out = append(out, "Standard award (fixed EuroBonus price)")
	} else {
		out = append(out, "Cash fare priced in points (dynamic price)")
	}
	for _, s := range f.Segments {
		if s.OperatingCarrier == nil || s.OperatingCarrier.Name == "" ||
			s.OperatingCarrier.Code == s.MarketingCarrier.Code {
			continue
		}
		n := fmt.Sprintf("%s%s operated by %s", s.MarketingCarrier.Code,
			s.FlightNumber, s.OperatingCarrier.Name)
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// mapCabin maps SAS's cabin name to a cabin: ECONOMY, PREMIUM (SAS Plus)
// or BUSINESS.
func mapCabin(name string) award.Cabin {
	switch strings.ToUpper(name) {
	case "FIRST":
		return award.First
	case "BUSINESS":
		return award.Business
	case "PREMIUM", "PLUS":
		return award.Premium
	default:
		return award.Economy
	}
}

// minutes parses a duration written "HH:MM:SS" or "D.HH:MM:SS" into whole
// minutes. It returns 0 for a duration it cannot read.
func minutes(d string) int {
	days := 0
	if dot := strings.Index(d, "."); dot >= 0 {
		days, _ = strconv.Atoi(d[:dot])
		d = d[dot+1:]
	}
	parts := strings.Split(d, ":")
	if len(parts) < 2 {
		return 0
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0
	}
	return days*24*60 + h*60 + m
}
