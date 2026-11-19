package etihad

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/joakimcarlsson/flight-club/internal/award"
)

// answer is the part of an air-bounds API answer the offers are read from.
type answer struct {
	Data struct {
		AirBoundGroups []boundGroup `json:"airBoundGroups"`
	} `json:"data"`
	Errors       []apiError   `json:"errors"`
	Dictionaries dictionaries `json:"dictionaries"`
}

// apiError is one error the air-bounds API answers with.
type apiError struct {
	Code   string `json:"code"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// dictionaries are the lookup tables an answer refers into by id.
type dictionaries struct {
	Flight     map[string]flight     `json:"flight"`
	Currency   map[string]currency   `json:"currency"`
	FareFamily map[string]fareFamily `json:"fareFamilyWithServices"`
}

// flight is one flight of the flight dictionary.
type flight struct {
	MarketingAirlineCode  string   `json:"marketingAirlineCode"`
	OperatingAirlineCode  string   `json:"operatingAirlineCode"`
	MarketingFlightNumber string   `json:"marketingFlightNumber"`
	Departure             endpoint `json:"departure"`
	Arrival               endpoint `json:"arrival"`
	AircraftCode          string   `json:"aircraftCode"`
	Duration              int      `json:"duration"`
}

// endpoint is where and when a flight departs or arrives.
type endpoint struct {
	LocationCode string `json:"locationCode"`
	DateTime     string `json:"dateTime"`
}

// currency is one currency of the currency dictionary.
type currency struct {
	DecimalPlaces int `json:"decimalPlaces"`
}

// fareFamily is one fare family of the fare family dictionary.
type fareFamily struct {
	CommercialFareFamily string `json:"commercialFareFamily"`
	Cabin                string `json:"cabin"`
}

// boundGroup is one itinerary with every fare it is sold in.
type boundGroup struct {
	BoundDetails struct {
		OriginLocationCode      string `json:"originLocationCode"`
		DestinationLocationCode string `json:"destinationLocationCode"`
		Duration                int    `json:"duration"`
		Segments                []struct {
			FlightID string `json:"flightId"`
		} `json:"segments"`
	} `json:"boundDetails"`
	AirBounds []airBound `json:"airBounds"`
}

// airBound is one fare of an itinerary.
type airBound struct {
	FareFamilyCode      string         `json:"fareFamilyCode"`
	AvailabilityDetails []availability `json:"availabilityDetails"`
	Prices              prices         `json:"prices"`
}

// availability is the cabin, booking class and seats of one flight of a
// fare.
type availability struct {
	FlightID     string `json:"flightId"`
	Cabin        string `json:"cabin"`
	BookingClass string `json:"bookingClass"`
	Quota        *int   `json:"quota"`
}

// prices are what a fare costs, per passenger and for every passenger.
type prices struct {
	UnitPrices []struct {
		TravelerIDs     []string   `json:"travelerIds"`
		MilesConversion conversion `json:"milesConversion"`
	} `json:"unitPrices"`
	IsRedemption    bool       `json:"isRedemption"`
	MilesConversion conversion `json:"milesConversion"`
}

// conversion is a price split into the miles paid and the cash left over.
type conversion struct {
	ConvertedMiles struct {
		Total int `json:"total"`
	} `json:"convertedMiles"`
	RemainingNonConverted struct {
		Total        float64 `json:"total"`
		CurrencyCode string  `json:"currencyCode"`
	} `json:"remainingNonConverted"`
}

// parseAnswer reads one air-bounds API answer. It returns an error when
// body is not JSON, or is an error answer other than one saying there are
// no flights.
func parseAnswer(body []byte) (answer, error) {
	var a answer
	if err := json.Unmarshal(body, &a); err != nil {
		return a, fmt.Errorf("etihad: reading air bounds: %w", err)
	}
	if len(a.Data.AirBoundGroups) == 0 && len(a.Errors) > 0 &&
		!a.noFlights() {
		e := a.Errors[0]
		return a, fmt.Errorf(
			"etihad: air bounds error %s: %s", e.Code, e.Title,
		)
	}
	return a, nil
}

// noFlights reports whether a's errors only say that nothing was found.
func (a answer) noFlights() bool {
	for _, e := range a.Errors {
		text := strings.ToLower(e.Title + " " + e.Detail)
		if !strings.Contains(text, "no ") &&
			!strings.Contains(text, "not found") &&
			!strings.Contains(text, "unavailab") {
			return false
		}
	}
	return true
}

// offers turns every award fare of a into an offer for q, booked at link.
func (a answer) offers(q award.Query, link string) []award.Offer {
	out := []award.Offer{}
	for _, g := range a.Data.AirBoundGroups {
		segs, ok := a.segments(g)
		if !ok || !sameDay(segs[0].Departure, q.Date) {
			continue
		}
		for _, b := range g.AirBounds {
			if o, ok := a.offer(g, segs, b, q, link); ok {
				out = append(out, o)
			}
		}
	}
	return out
}

// segments builds the segments of g, without cabins and booking codes,
// which differ per fare. It returns false when a flight of g is missing
// from the dictionary or has a time it cannot read.
func (a answer) segments(g boundGroup) ([]award.Segment, bool) {
	segs := make([]award.Segment, 0, len(g.BoundDetails.Segments))
	for _, ref := range g.BoundDetails.Segments {
		f, ok := a.Dictionaries.Flight[ref.FlightID]
		if !ok {
			return nil, false
		}
		dep, err := time.Parse(time.RFC3339, f.Departure.DateTime)
		if err != nil {
			return nil, false
		}
		arr, err := time.Parse(time.RFC3339, f.Arrival.DateTime)
		if err != nil {
			return nil, false
		}
		operating := f.OperatingAirlineCode
		if operating == "" {
			operating = f.MarketingAirlineCode
		}
		segs = append(segs, award.Segment{
			Flight:           f.MarketingAirlineCode + f.MarketingFlightNumber,
			MarketingCarrier: f.MarketingAirlineCode,
			OperatingCarrier: operating,
			Origin:           f.Departure.LocationCode,
			Destination:      f.Arrival.LocationCode,
			Departure:        dep,
			Arrival:          arr,
			DurationMinutes:  minutes(f.Duration, dep, arr),
			Aircraft:         f.AircraftCode,
		})
	}
	for i := 0; i+1 < len(segs); i++ {
		gap := segs[i+1].Departure.Sub(segs[i].Arrival)
		segs[i].LayoverMinutes = int(gap.Minutes())
	}
	return segs, len(segs) > 0
}

// sameDay reports whether the local date of t is the calendar date of
// day, so the next day's flights the results page also lists are dropped.
func sameDay(t, day time.Time) bool {
	return t.Format(time.DateOnly) == day.Format(time.DateOnly)
}

// minutes returns seconds as whole minutes, or the time from dep to arr
// when seconds is not given.
func minutes(seconds int, dep, arr time.Time) int {
	if seconds > 0 {
		return seconds / 60
	}
	return int(arr.Sub(dep).Minutes())
}

// offer builds the offer of fare b on itinerary g, whose segments are
// base, for q, booked at link. It returns false when b is not priced in
// miles.
func (a answer) offer(
	g boundGroup,
	base []award.Segment,
	b airBound,
	q award.Query,
	link string,
) (award.Offer, bool) {
	total := b.Prices.MilesConversion
	if total.ConvertedMiles.Total <= 0 {
		return award.Offer{}, false
	}
	pax := max(q.Passengers, 1)
	unit := total
	if len(b.Prices.UnitPrices) > 0 {
		unit = b.Prices.UnitPrices[0].MilesConversion
	} else {
		unit.ConvertedMiles.Total /= pax
		unit.RemainingNonConverted.Total /= float64(pax)
	}
	code := total.RemainingNonConverted.CurrencyCode
	if code == "" {
		code = unit.RemainingNonConverted.CurrencyCode
	}
	decimals := a.Dictionaries.Currency[code].DecimalPlaces

	segs, cabin, mixed, seats := a.fareSegments(base, b)
	first, last := segs[0], segs[len(segs)-1]
	duration := g.BoundDetails.Duration / 60
	if duration <= 0 {
		duration = int(last.Arrival.Sub(first.Departure).Minutes())
	}
	return award.Offer{
		Program:         Key,
		Availability:    award.Confirmed,
		Origin:          first.Origin,
		Destination:     last.Destination,
		Departure:       first.Departure,
		Arrival:         last.Arrival,
		DurationMinutes: duration,
		Stops:           len(segs) - 1,
		Airlines:        airlines(segs),
		Cabin:           cabin,
		MixedCabin:      mixed,
		Fare:            b.FareFamilyCode,
		Price: award.Price{
			PointsPerPassenger: unit.ConvertedMiles.Total,
			PointsTotal:        total.ConvertedMiles.Total,
			TaxesPerPassenger: award.Money{
				Amount: scale(
					unit.RemainingNonConverted.Total, decimals,
				),
				Currency: code,
			},
			TaxesTotal: award.Money{
				Amount: scale(
					total.RemainingNonConverted.Total, decimals,
				),
				Currency: code,
			},
		},
		Seats:      seats,
		BookingURL: link,
		Notes:      a.notes(b),
		Segments:   segs,
	}, true
}

// fareSegments copies base and fills in each segment's cabin and booking
// code from fare b. It returns the segments, the fare's cabin, whether the
// segments fly different cabins, and the fewest seats left on any of them,
// nil when b does not say.
func (a answer) fareSegments(
	base []award.Segment,
	b airBound,
) ([]award.Segment, award.Cabin, bool, *int) {
	byFlight := make(map[string]availability, len(b.AvailabilityDetails))
	for _, d := range b.AvailabilityDetails {
		byFlight[d.FlightID] = d
	}
	fam := a.Dictionaries.FareFamily[b.FareFamilyCode]
	cabin := mapCabin(fam.Cabin)
	if cabin == "" && len(b.AvailabilityDetails) > 0 {
		cabin = mapCabin(b.AvailabilityDetails[0].Cabin)
	}
	if cabin == "" {
		cabin = award.Economy
	}
	segs := make([]award.Segment, len(base))
	copy(segs, base)
	var (
		mixed bool
		seats *int
	)
	for i, d := range b.AvailabilityDetails {
		if i >= len(segs) {
			break
		}
		segs[i].BookingCode = d.BookingClass
		segs[i].Cabin = mapCabin(d.Cabin)
		if segs[i].Cabin == "" {
			segs[i].Cabin = cabin
		}
		if segs[i].Cabin != cabin {
			mixed = true
		}
		if d.Quota != nil && (seats == nil || *d.Quota < *seats) {
			n := *d.Quota
			seats = &n
		}
	}
	for i := range segs {
		if segs[i].Cabin == "" {
			segs[i].Cabin = cabin
		}
	}
	return segs, cabin, mixed, seats
}

// notes describes fare b: its commercial fare family, and whether it is
// one of the Guest award fares or one priced from the cash fare, whose
// taxes are paid in miles too.
func (a answer) notes(b airBound) []string {
	var out []string
	if fam := a.Dictionaries.FareFamily[b.FareFamilyCode]; fam.
		CommercialFareFamily != "" {
		out = append(out, "Fare family "+fam.CommercialFareFamily)
	}
	if guestAward(b.FareFamilyCode) {
		out = append(out, "Etihad Guest award fare")
	}
	if !b.Prices.IsRedemption {
		out = append(out, "Priced from the cash fare, taxes paid in miles")
	}
	return out
}

// guestAward reports whether a fare family code is one of the Guest award
// families, such as YGVALUE or JGCOMFORT, whose second letter is G.
func guestAward(code string) bool {
	return len(code) > 2 && code[1] == 'G'
}

// mapCabin maps the booking engine's cabin names to a Cabin, returning ""
// for a name it does not know.
func mapCabin(name string) award.Cabin {
	switch strings.ToLower(name) {
	case "eco", "economy":
		return award.Economy
	case "ecopremium", "premium", "premiumeconomy", "premium_economy":
		return award.Premium
	case "business":
		return award.Business
	case "first":
		return award.First
	}
	return ""
}

// airlines returns the operating carriers of segs, in order of first
// appearance.
func airlines(segs []award.Segment) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range segs {
		if !seen[s.OperatingCarrier] {
			seen[s.OperatingCarrier] = true
			out = append(out, s.OperatingCarrier)
		}
	}
	return out
}
