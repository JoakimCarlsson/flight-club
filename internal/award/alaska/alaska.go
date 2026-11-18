// Package alaska searches Alaska Airlines award availability, priced in
// Atmos Rewards points. City codes such as NYC are searched as every airport
// of the city, and partner flights are included.
//
// alaskaair.com is a SvelteKit app. Its search results page loads its data
// from the route's __data.json endpoint, which answers a plain GET without a
// session or a login. The answer is newline-delimited: a first line with the
// page data and one "chunk" line per promise the page streams, each in
// SvelteKit's devalue format. The chunk whose value carries "rows" holds the
// itineraries, each priced in every fare family that has availability.
package alaska

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/joakimcarlsson/flight-club/internal/award"
	"github.com/joakimcarlsson/flight-club/internal/devalue"
)

// Key is the program key Alaska offers carry.
const Key = "alaska"

// resultsURL is the search results page a traveller books from.
const resultsURL = "https://www.alaskaair.com/search/results"

// dataURL is the search results route's data endpoint.
const dataURL = resultsURL + "/__data.json"

// userAgent is a current desktop Chrome, which the site answers like a
// browser.
const userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36"

// maxBody is the most of an answer that is read, well above the few hundred
// kilobytes a busy route answers with.
const maxBody = 16 << 20

// taxCurrency is the currency Alaska prices taxes in on alaskaair.com.
const taxCurrency = "USD"

// Program searches alaskaair.com.
type Program struct {
	client *http.Client
}

// New builds a Program that makes its requests with client.
func New(client *http.Client) *Program {
	return &Program{client: client}
}

// Key returns the program key, "alaska".
func (p *Program) Key() string {
	return Key
}

// Search returns Alaska's award offers for q. It returns an error when the
// site cannot be reached, answers with a status other than 200, or answers
// with data that has no itineraries in it.
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
		return nil, fmt.Errorf("alaska: building request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("alaska: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("alaska: answered %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("alaska: reading answer: %w", err)
	}

	res, err := parseResults(body)
	if err != nil {
		return nil, err
	}
	return offers(res, bookingURL(q)), nil
}

// searchURL is the data endpoint for a one-way points search for q.
func searchURL(q award.Query) string {
	return dataURL + "?" + searchParams(q).Encode()
}

// bookingURL is the results page for a one-way points search for q, where
// a traveller picks and books an offer.
func bookingURL(q award.Query) string {
	return resultsURL + "?" + searchParams(q).Encode()
}

// searchParams are the query parameters of a one-way points search for q.
func searchParams(q award.Query) url.Values {
	v := url.Values{}
	v.Set("O", q.Origin)
	v.Set("D", q.Destination)
	v.Set("OD", q.Date.Format(time.DateOnly))
	v.Set("A", strconv.Itoa(q.Passengers))
	v.Set("RT", "false")
	v.Set("ShoppingMethod", "onlineaward")
	return v
}

// line is one line of a __data.json answer. Only chunk lines are read.
type line struct {
	// Type is "data" for the page data and "chunk" for a streamed promise.
	Type string `json:"type"`
	// Data is the devalue array of a chunk.
	Data json.RawMessage `json:"data"`
}

// results is the streamed search result.
type results struct {
	// Rows are the itineraries.
	Rows []row `json:"rows"`
}

// row is one itinerary.
type row struct {
	// Origin is the IATA code it departs from.
	Origin string `json:"origin"`
	// Destination is the IATA code it arrives at.
	Destination string `json:"destination"`
	// Duration is the door-to-door time in minutes.
	Duration int `json:"duration"`
	// MatrixOperationalDisclosures say which carriers operate which
	// flights.
	MatrixOperationalDisclosures []disclosure `json:"matrixOperationalDisclosures"`
	// Segments are its flights, in order.
	Segments []segment `json:"segments"`
	// Solutions are its prices keyed by fare family, e.g.
	// "SAVER_MAIN" or "REFUNDABLE_FIRST". A family without availability is
	// absent.
	Solutions map[string]solution `json:"solutions"`
}

// disclosure is one carrier's operating disclosure.
type disclosure struct {
	// Disclosures are the sentences the site shows.
	Disclosures []string `json:"disclosures"`
}

// segment is one flight of a row.
type segment struct {
	// Hash identifies the segment within its row's fare mappings.
	Hash string `json:"hash"`
	// PublishingCarrier is the marketing carrier and flight number.
	PublishingCarrier carrier `json:"publishingCarrier"`
	// DisplayCarrier is the operating carrier and flight number.
	DisplayCarrier carrier `json:"displayCarrier"`
	// DepartureStation is the IATA code it departs from.
	DepartureStation string `json:"departureStation"`
	// ArrivalStation is the IATA code it arrives at.
	ArrivalStation string `json:"arrivalStation"`
	// DepartureTime is RFC 3339 in the origin's offset.
	DepartureTime time.Time `json:"departureTime"`
	// ArrivalTime is RFC 3339 in the destination's offset.
	ArrivalTime time.Time `json:"arrivalTime"`
	// Aircraft is the aircraft type's name.
	Aircraft string `json:"aircraft"`
	// Duration is the time in the air in minutes.
	Duration int `json:"duration"`
	// StopoverDuration is the connection time after it in minutes.
	StopoverDuration int `json:"stopoverDuration"`
}

// carrier is an airline and a flight number.
type carrier struct {
	// CarrierCode is the IATA airline code.
	CarrierCode string `json:"carrierCode"`
	// FlightNumber is the flight number without the airline code.
	FlightNumber int `json:"flightNumber"`
}

// solution is one row's price in one fare family.
type solution struct {
	// AtmosPoints is the points price for one passenger.
	AtmosPoints int `json:"atmosPoints"`
	// GrandTotal is the taxes and fees for one passenger, in USD.
	GrandTotal float64 `json:"grandTotal"`
	// AllPaxPoints is the points price for every passenger.
	AllPaxPoints int `json:"allPaxPoints"`
	// AllPaxTotal is the taxes and fees for every passenger, in USD.
	AllPaxTotal float64 `json:"allPaxTotal"`
	// SeatsRemaining is how many seats are left at this price.
	SeatsRemaining int `json:"seatsRemaining"`
	// MixedCabin is set when the segments fly different cabins.
	MixedCabin bool `json:"mixedCabin"`
	// Refundable is set when the points are returned on cancellation.
	Refundable bool `json:"refundable"`
	// FareSegmentMapping is the cabin and booking code per segment.
	FareSegmentMapping []fareSegment `json:"fareSegmentMapping"`
}

// fareSegment is how one segment is booked in a solution.
type fareSegment struct {
	// SegmentHash is the Hash of the segment it describes.
	SegmentHash string `json:"segmentHash"`
	// BookingCode is the fare class letter.
	BookingCode string `json:"bookingCode"`
	// Cabin is Alaska's cabin name: COACH, PREMIUM, BUSINESS or FIRST.
	Cabin string `json:"cabin"`
}

// errNoResults is returned when the answer carries no search result, which
// is how the endpoint answers a search it could not run.
var errNoResults = errors.New("alaska: answer has no search result")

// parseResults finds the chunk that carries the itineraries in a
// __data.json answer and decodes it. It returns errNoResults when no chunk
// does, and an error when a chunk cannot be decoded.
func parseResults(body []byte) (results, error) {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 0, 1<<20), maxBody)
	for scanner.Scan() {
		var l line
		if err := json.Unmarshal(scanner.Bytes(), &l); err != nil {
			return results{}, fmt.Errorf("alaska: reading answer: %w", err)
		}
		if l.Type != "chunk" {
			continue
		}
		var probe map[string]any
		if err := devalue.Unmarshal(l.Data, &probe); err != nil {
			continue
		}
		if _, ok := probe["rows"]; !ok {
			continue
		}
		var res results
		if err := devalue.Unmarshal(l.Data, &res); err != nil {
			return results{}, fmt.Errorf("alaska: decoding rows: %w", err)
		}
		return res, nil
	}
	if err := scanner.Err(); err != nil {
		return results{}, fmt.Errorf("alaska: reading answer: %w", err)
	}
	return results{}, errNoResults
}

// offers turns every priced fare family of every row into an offer booked
// at booking.
func offers(res results, booking string) []award.Offer {
	out := []award.Offer{}
	for _, r := range res.Rows {
		if len(r.Segments) == 0 {
			continue
		}
		for fare, sol := range r.Solutions {
			out = append(out, offer(r, fare, sol, booking))
		}
	}
	return out
}

// offer builds the offer for row r priced in fare family fare, booked at
// booking.
func offer(r row, fare string, sol solution, booking string) award.Offer {
	bookings := make(map[string]fareSegment, len(sol.FareSegmentMapping))
	for _, fs := range sol.FareSegmentMapping {
		bookings[fs.SegmentHash] = fs
	}
	segments := make([]award.Segment, 0, len(r.Segments))
	var airlines []string
	for i, s := range r.Segments {
		b := bookings[s.Hash]
		if !slices.Contains(airlines, s.DisplayCarrier.CarrierCode) {
			airlines = append(airlines, s.DisplayCarrier.CarrierCode)
		}
		layover := s.StopoverDuration
		if i == len(r.Segments)-1 {
			layover = 0
		}
		segments = append(segments, award.Segment{
			Flight: s.PublishingCarrier.CarrierCode +
				strconv.Itoa(s.PublishingCarrier.FlightNumber),
			MarketingCarrier: s.PublishingCarrier.CarrierCode,
			OperatingCarrier: s.DisplayCarrier.CarrierCode,
			DurationMinutes:  s.Duration,
			LayoverMinutes:   layover,
			Origin:           s.DepartureStation,
			Destination:      s.ArrivalStation,
			Departure:        s.DepartureTime,
			Arrival:          s.ArrivalTime,
			Aircraft:         s.Aircraft,
			Cabin:            segmentCabin(b.Cabin),
			BookingCode:      b.BookingCode,
		})
	}
	seats := sol.SeatsRemaining
	return award.Offer{
		Program:         Key,
		Availability:    award.Confirmed,
		Origin:          r.Origin,
		Destination:     r.Destination,
		Departure:       segments[0].Departure,
		Arrival:         segments[len(segments)-1].Arrival,
		DurationMinutes: r.Duration,
		Stops:           len(segments) - 1,
		Airlines:        airlines,
		Cabin:           fareCabin(fare),
		MixedCabin:      sol.MixedCabin,
		Fare:            fare,
		Price: award.Price{
			PointsPerPassenger: sol.AtmosPoints,
			PointsTotal:        sol.AllPaxPoints,
			TaxesPerPassenger: award.Money{
				Amount: sol.GrandTotal, Currency: taxCurrency,
			},
			TaxesTotal: award.Money{
				Amount: sol.AllPaxTotal, Currency: taxCurrency,
			},
		},
		Seats:      &seats,
		BookingURL: booking,
		Notes:      notes(r, fare, sol),
		Segments:   segments,
	}
}

// notes are what the site says about an offer beyond its price: who
// operates it, whether it is a partner fare and whether it is refundable.
func notes(r row, fare string, sol solution) []string {
	var out []string
	for _, d := range r.MatrixOperationalDisclosures {
		out = append(out, d.Disclosures...)
	}
	if strings.Contains(fare, "PARTNER") {
		out = append(out, "Partner award")
	}
	if sol.Refundable {
		out = append(out, "Refundable: points are returned on cancellation")
	}
	return out
}

// fareCabin maps a fare family such as "SAVER_MAIN" or "REFUNDABLE_FIRST" to
// its cabin by the family's last word.
func fareCabin(fare string) award.Cabin {
	word := fare[strings.LastIndex(fare, "_")+1:]
	switch word {
	case "FIRST":
		return award.First
	case "BUSINESS":
		return award.Business
	case "PREMIUM":
		return award.Premium
	default:
		return award.Economy
	}
}

// segmentCabin maps Alaska's per-segment cabin name to a cabin.
func segmentCabin(name string) award.Cabin {
	switch name {
	case "FIRST":
		return award.First
	case "BUSINESS":
		return award.Business
	case "PREMIUM", "PREMIUM_COACH":
		return award.Premium
	default:
		return award.Economy
	}
}
