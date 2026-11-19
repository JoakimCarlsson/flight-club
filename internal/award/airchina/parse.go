package airchina

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/joakimcarlsson/flight-club/internal/award"
)

// taxNote tells a traveller the site quotes taxes only at checkout.
const taxNote = "Taxes and surcharges are quoted at checkout"

// flight is one flight the endpoint found, with its award cabins.
type flight struct {
	// FlightNo is the flight number without the carrier, e.g. "965".
	FlightNo string `json:"flightNo"`
	// Airline is the marketing carrier.
	Airline string `json:"airline"`
	// ShareAirline is the operating carrier of a codeshare, else empty.
	ShareAirline *string `json:"shareAirline"`
	// Aircraft is the aircraft flying it.
	Aircraft struct {
		// AircraftNo is the aircraft type code, e.g. "773".
		AircraftNo string `json:"aircraftNo"`
	} `json:"aircraft"`
	// Org is where it departs from.
	Org place `json:"org"`
	// Dst is where it arrives at.
	Dst place `json:"dst"`
	// Departure is the local departure date and time.
	Departure moment `json:"departure"`
	// Arrival is the local arrival date and time.
	Arrival moment `json:"arrival"`
	// StopInfo describes a stop on the way, null for a nonstop.
	StopInfo any `json:"stopInfo"`
	// FlightTime is the flying time, HH:MM.
	FlightTime string `json:"flightTime"`
	// Cabins are the award cabins with space.
	Cabins []cabin `json:"cabins"`
}

// place is an airport of a flight.
type place struct {
	// Airport is the IATA airport code.
	Airport string `json:"airport"`
	// TimeZone is its UTC offset, e.g. "GMT+8" or "GMT-4:30".
	TimeZone string `json:"timeZone"`
}

// moment is a local date and time.
type moment struct {
	// Date is YYYY-MM-DD.
	Date string `json:"date"`
	// Time is HH:MM.
	Time string `json:"time"`
}

// cabin is one award cabin of a flight.
type cabin struct {
	// ClassCode is the booking class, e.g. "X".
	ClassCode string `json:"classCode"`
	// ClassOfService is the cabin name, e.g. "economy" or "business".
	ClassOfService string `json:"classOfService"`
	// SeatNum is the seats left, "A" for nine or more.
	SeatNum string `json:"seatNum"`
}

// offers turns a search result into offers for passengers adults. A nil
// result has none.
func offers(res *resultBody, passengers int) []award.Offer {
	if res == nil {
		return nil
	}
	passengers = max(passengers, 1)
	var out []award.Offer
	for _, f := range res.Segment.Flights {
		for _, c := range f.Cabins {
			o, ok := offer(f, c, res.MileageFares, passengers)
			if ok {
				out = append(out, o)
			}
		}
	}
	return out
}

// offer builds the offer for cabin c of flight f, priced from fares. It
// returns false when the flight's times or price cannot be read.
func offer(
	f flight,
	c cabin,
	fares map[string]int,
	passengers int,
) (award.Offer, bool) {
	marketing := strings.ToUpper(f.Airline)
	if marketing == "" {
		marketing = carrier
	}
	number := marketing + f.FlightNo
	miles, ok := fares[number+c.ClassCode]
	if !ok {
		miles, ok = fares[number]
	}
	if !ok || miles <= 0 {
		return award.Offer{}, false
	}
	dep, err := localTime(f.Departure, f.Org.TimeZone)
	if err != nil {
		return award.Offer{}, false
	}
	arr, err := localTime(f.Arrival, f.Dst.TimeZone)
	if err != nil {
		return award.Offer{}, false
	}
	operating := marketing
	if f.ShareAirline != nil && *f.ShareAirline != "" {
		operating = strings.ToUpper(*f.ShareAirline)
	}
	cab := mapCabin(c.ClassOfService)
	total := int(arr.Sub(dep).Minutes())
	air := flyingMinutes(f.FlightTime)
	if air <= 0 {
		air = total
	}
	stops := 0
	if f.StopInfo != nil {
		stops = 1
	}
	zero := award.Money{Amount: 0, Currency: taxCurrency}
	return award.Offer{
		Program:         Key,
		Availability:    award.Confirmed,
		Origin:          f.Org.Airport,
		Destination:     f.Dst.Airport,
		Departure:       dep,
		Arrival:         arr,
		DurationMinutes: total,
		Stops:           stops,
		Airlines:        []string{operating},
		Cabin:           cab,
		Fare:            c.ClassCode,
		Price: award.Price{
			PointsPerPassenger: miles,
			PointsTotal:        miles * passengers,
			TaxesPerPassenger:  zero,
			TaxesTotal:         zero,
		},
		Seats:      seats(c.SeatNum),
		BookingURL: bookingURL,
		Notes:      []string{taxNote},
		Segments: []award.Segment{{
			Flight:           number,
			MarketingCarrier: marketing,
			OperatingCarrier: operating,
			Origin:           f.Org.Airport,
			Destination:      f.Dst.Airport,
			Departure:        dep,
			Arrival:          arr,
			DurationMinutes:  air,
			Aircraft:         f.Aircraft.AircraftNo,
			Cabin:            cab,
			BookingCode:      c.ClassCode,
		}},
	}, true
}

// mapCabin maps the site's class of service to a cabin, economy when it
// is not known.
func mapCabin(name string) award.Cabin {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "premium"):
		return award.Premium
	case strings.Contains(n, "business"):
		return award.Business
	case strings.Contains(n, "first"):
		return award.First
	}
	return award.Economy
}

// seats reads the site's seats left: a count, or "A" for nine or more.
// It returns nil when the value is not a count.
func seats(s string) *int {
	s = strings.TrimSpace(s)
	if s == seatsMany {
		n := 9
		return &n
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return nil
	}
	return &n
}

// flyingMinutes reads an HH:MM duration, 0 when it cannot be read.
func flyingMinutes(s string) int {
	h, m, ok := strings.Cut(s, ":")
	if !ok {
		return 0
	}
	hours, err1 := strconv.Atoi(h)
	mins, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil {
		return 0
	}
	return hours*60 + mins
}

// localTime reads m in the UTC offset zone, such as "GMT+8".
func localTime(m moment, zone string) (time.Time, error) {
	offset, err := parseOffset(zone)
	if err != nil {
		return time.Time{}, err
	}
	loc := time.FixedZone(zone, offset)
	return time.ParseInLocation("2006-01-02 15:04", m.Date+" "+m.Time, loc)
}

// parseOffset reads a "GMT+8", "GMT-3:30" or "GMT+0530" offset into
// seconds east of UTC.
func parseOffset(zone string) (int, error) {
	z := strings.TrimSpace(strings.TrimPrefix(
		strings.ToUpper(strings.TrimSpace(zone)), "GMT"))
	z = strings.TrimPrefix(z, "UTC")
	if z == "" {
		return 0, nil
	}
	sign := 1
	switch z[0] {
	case '-':
		sign = -1
		z = z[1:]
	case '+':
		z = z[1:]
	}
	h, m, found := strings.Cut(z, ":")
	if !found && len(z) > 2 {
		h, m = z[:len(z)-2], z[len(z)-2:]
	}
	hours, err := strconv.Atoi(h)
	if err != nil {
		return 0, fmt.Errorf("airchina: bad time zone %q", zone)
	}
	mins := 0
	if m != "" {
		if mins, err = strconv.Atoi(m); err != nil {
			return 0, fmt.Errorf("airchina: bad time zone %q", zone)
		}
	}
	return sign * (hours*3600 + mins*60), nil
}
