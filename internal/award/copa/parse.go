package copa

import (
	"strings"
	"time"

	"github.com/joakimcarlsson/flight-club/internal/award"
)

// originDestination is one direction of the award API's answer.
type originDestination struct {
	Currency struct {
		Code string `json:"code"`
	} `json:"currency"`
	Solutions []solution `json:"solutions"`
}

// solution is one itinerary with the fares it is sold in.
type solution struct {
	NumberOfLayovers int      `json:"numberOfLayovers"`
	JourneyTime      string   `json:"journeyTime"`
	Flights          []flight `json:"flights"`
	Offers           []fare   `json:"offers"`
}

// flight is one segment of an itinerary.
type flight struct {
	AircraftName     string   `json:"aircraftName"`
	MarketingCarrier *carrier `json:"marketingCarrier"`
	OperatingCarrier *carrier `json:"operatingCarrier"`
	Departure        endpoint `json:"departure"`
	Arrival          endpoint `json:"arrival"`
}

// carrier is an airline selling or flying a segment.
type carrier struct {
	FlightNumber *string `json:"flightNumber"`
	AirlineCode  *string `json:"airlineCode"`
	AirlineName  string  `json:"airlineName"`
}

// endpoint is where and when a segment departs or arrives, in local time.
type endpoint struct {
	AirportCode string `json:"airportCode"`
	FlightDate  string `json:"flightDate"`
	FlightTime  string `json:"flightTime"`
}

// fare is one fare family an itinerary is sold in.
type fare struct {
	PricePerAdult struct {
		Miles int     `json:"miles"`
		Taxes float64 `json:"taxes"`
	} `json:"pricePerAdult"`
	FareFamily struct {
		Name         string `json:"name"`
		Code         string `json:"code"`
		Descriptions []struct {
			Text string `json:"text"`
		} `json:"descriptions"`
	} `json:"fareFamily"`
	ClassOfService []string `json:"classOfService"`
}

// offers turns every priced fare of od into an offer for q, booked at
// booking. Itineraries whose times cannot be read are skipped.
func offers(
	od originDestination,
	q award.Query,
	booking string,
) []award.Offer {
	n := max(q.Passengers, 1)
	var out []award.Offer
	for _, s := range od.Solutions {
		if len(s.Flights) == 0 {
			continue
		}
		times, ok := tripTimes(s)
		if !ok {
			continue
		}
		for _, f := range s.Offers {
			if f.PricePerAdult.Miles <= 0 {
				continue
			}
			out = append(out, offer(s, times, f, od.Currency.Code, n, booking))
		}
	}
	return out
}

// offer builds the offer of itinerary s, whose segments have times, sold
// in fare f with taxes in currency, for n passengers, booked at booking.
func offer(
	s solution,
	times []segmentTimes,
	f fare,
	currency string,
	n int,
	booking string,
) award.Offer {
	cabin := cabinOf(f.FareFamily.Code, f.FareFamily.Name)
	segs := make([]award.Segment, len(s.Flights))
	var airlines []string
	for i, fl := range s.Flights {
		segs[i] = segment(fl, times[i], cabin)
		if i < len(f.ClassOfService) {
			segs[i].BookingCode = f.ClassOfService[i]
		}
		if i > 0 {
			segs[i-1].LayoverMinutes = minutes(
				times[i].departure.Sub(times[i-1].arrival),
			)
		}
		if !contains(airlines, segs[i].OperatingCarrier) {
			airlines = append(airlines, segs[i].OperatingCarrier)
		}
	}
	first, last := segs[0], segs[len(segs)-1]
	var notes []string
	for _, d := range f.FareFamily.Descriptions {
		if t := strings.TrimSpace(d.Text); t != "" {
			notes = append(notes, t)
		}
	}
	per := f.PricePerAdult
	return award.Offer{
		Program:         Key,
		Availability:    award.Confirmed,
		Origin:          first.Origin,
		Destination:     last.Destination,
		Departure:       first.Departure,
		Arrival:         last.Arrival,
		DurationMinutes: minutes(last.Arrival.Sub(first.Departure)),
		Stops:           len(segs) - 1,
		Airlines:        airlines,
		Cabin:           cabin,
		Fare:            f.FareFamily.Name,
		Price: award.Price{
			PointsPerPassenger: per.Miles,
			PointsTotal:        per.Miles * n,
			TaxesPerPassenger: award.Money{
				Amount: per.Taxes, Currency: currency,
			},
			TaxesTotal: award.Money{
				Amount: per.Taxes * float64(n), Currency: currency,
			},
		},
		BookingURL: booking,
		Notes:      notes,
		Segments:   segs,
	}
}

// segment builds the segment flown by fl at times t in cabin.
func segment(fl flight, t segmentTimes, cabin award.Cabin) award.Segment {
	mkt := code(fl.MarketingCarrier)
	op := code(fl.OperatingCarrier)
	if op == "" {
		op = mkt
	}
	number := ""
	if fl.MarketingCarrier != nil && fl.MarketingCarrier.FlightNumber != nil {
		number = *fl.MarketingCarrier.FlightNumber
	}
	return award.Segment{
		Flight:           mkt + number,
		MarketingCarrier: mkt,
		OperatingCarrier: op,
		Origin:           fl.Departure.AirportCode,
		Destination:      fl.Arrival.AirportCode,
		Departure:        t.departure,
		Arrival:          t.arrival,
		DurationMinutes:  minutes(t.arrival.Sub(t.departure)),
		Aircraft:         fl.AircraftName,
		Cabin:            cabin,
	}
}

// code is the IATA code of c, empty when c or its code is missing.
func code(c *carrier) string {
	if c == nil || c.AirlineCode == nil {
		return ""
	}
	return strings.TrimSpace(*c.AirlineCode)
}

// cabinOf maps a Copa fare family, by its code (ESV, ESD, BSV, BSD) or
// else its name, to a cabin; anything not business is economy.
func cabinOf(familyCode, name string) award.Cabin {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "business"):
		return award.Business
	case strings.Contains(lower, "first"):
		return award.First
	case strings.Contains(lower, "premium"):
		return award.Premium
	case strings.Contains(lower, "economy"):
		return award.Economy
	case strings.HasPrefix(strings.ToUpper(familyCode), "B"):
		return award.Business
	}
	return award.Economy
}

// minutes is d in whole minutes.
func minutes(d time.Duration) int {
	return int(d.Round(time.Minute) / time.Minute)
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
