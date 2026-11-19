package turkish

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/joakimcarlsson/flight-club/internal/award"
)

// localLayout is how the answer writes local dates and times.
const localLayout = "02-01-2006 15:04"

// cabinOrder is the order the answer's cabins are read in, with the cabin
// each maps to.
var cabinOrder = []struct {
	name  string
	cabin award.Cabin
}{
	{"ECONOMY", award.Economy},
	{"PREMIUM_ECONOMY", award.Premium},
	{"PREMIUM", award.Premium},
	{"BUSINESS", award.Business},
	{"FIRST", award.First},
}

// answer is the availability API's reply.
type answer struct {
	Success          bool           `json:"success"`
	Data             *answerData    `json:"data"`
	StatusDetailList []statusDetail `json:"statusDetailList"`
}

// statusDetail is one message the API reports with a failure.
type statusDetail struct {
	Code    string `json:"code"`
	Message string `json:"translatedMessage"`
}

// answerData is the reply's payload.
type answerData struct {
	ODList []odInfo `json:"originDestinationInformationList"`
}

// odInfo is one direction of the reply.
type odInfo struct {
	Options []option `json:"originDestinationOptionList"`
}

// option is one itinerary with its fares by cabin.
type option struct {
	SoldOut         bool                `json:"soldOut"`
	JourneyDuration int64               `json:"journeyDuration"`
	FareCategory    map[string]category `json:"fareCategory"`
	SegmentList     []segment           `json:"segmentList"`
}

// category is the fares of one cabin.
type category struct {
	Status   string      `json:"status"`
	Bookings []priceInfo `json:"bookingPriceInfoList"`
}

// priceInfo is one fare brand of a cabin.
type priceInfo struct {
	BrandCode      string   `json:"brandCode"`
	CabinType      string   `json:"cabinType"`
	ResBookDesig   []string `json:"resBookDesigCodeList"`
	ReferenceFare  paxFare  `json:"referencePassengerFare"`
	FareBasisCodes []string `json:"fareBasisCodeList"`
}

// paxFare is the fare for one passenger.
type paxFare struct {
	TotalFare *amount `json:"totalFare"`
}

// amount is a price in a currency, MILE for awards.
type amount struct {
	CurrencyCode string  `json:"currencyCode"`
	Amount       float64 `json:"amount"`
}

// segment is one flight of an itinerary.
type segment struct {
	Departure     string   `json:"departureAirportCode"`
	Arrival       string   `json:"arrivalAirportCode"`
	DepartureTime string   `json:"departureDateTime"`
	ArrivalTime   string   `json:"arrivalDateTime"`
	Comment       string   `json:"comment"`
	Equipment     string   `json:"equipmentName"`
	EquipmentCode string   `json:"equipmentCode"`
	DurationMs    int64    `json:"journeyDurationInMillis"`
	Carrier       *airline `json:"carrierAirline"`
	FlightCode    flight   `json:"flightCode"`
	CodeSharing   *airline `json:"codeSharingAirline"`
}

// airline names an airline by its IATA code.
type airline struct {
	AirlineCode string `json:"airlineCode"`
}

// flight is a marketing flight number.
type flight struct {
	AirlineCode  string `json:"airlineCode"`
	FlightNumber string `json:"flightNumber"`
}

// parse reads an availability reply into the offers for q. It returns an
// error when the reply cannot be read or reports a failure other than no
// flights.
func parse(body []byte, q award.Query) ([]award.Offer, error) {
	var a answer
	if err := json.Unmarshal(body, &a); err != nil {
		return nil, fmt.Errorf("turkish: read availability: %w", err)
	}
	if !a.Success {
		msg := failureText(a.StatusDetailList)
		if noFlights(msg) {
			return []award.Offer{}, nil
		}
		return nil, fmt.Errorf("turkish: availability failed: %s", msg)
	}
	out := []award.Offer{}
	if a.Data == nil {
		return out, nil
	}
	for _, od := range a.Data.ODList {
		for _, opt := range od.Options {
			if opt.SoldOut {
				continue
			}
			out = append(out, opt.offers(q)...)
		}
	}
	return out, nil
}

// failureText joins the codes and messages of a failed reply.
func failureText(details []statusDetail) string {
	parts := make([]string, 0, len(details))
	for _, d := range details {
		parts = append(parts, strings.TrimSpace(d.Code+" "+d.Message))
	}
	if len(parts) == 0 {
		return "no reason given"
	}
	return strings.Join(parts, "; ")
}

// noFlights reports whether a failure message means the day has no
// flights rather than that the search went wrong.
func noFlights(msg string) bool {
	m := strings.ToLower(msg)
	for _, s := range []string{
		"no flight", "flight not found", "no availability",
		"not available", "could not find", "no result",
	} {
		if strings.Contains(m, s) {
			return true
		}
	}
	return false
}

// offers returns one offer per available fare brand of o.
func (o option) offers(q award.Query) []award.Offer {
	segs, ok := o.segments()
	if !ok {
		return nil
	}
	var out []award.Offer
	for _, c := range cabinOrder {
		cat, ok := o.FareCategory[c.name]
		if !ok || !strings.EqualFold(cat.Status, "AVAILABLE") {
			continue
		}
		for _, b := range cat.Bookings {
			if offer, ok := o.offer(q, segs, c.cabin, b); ok {
				out = append(out, offer)
			}
		}
	}
	return out
}

// offer builds the offer for fare brand b in cabin, over segs. It returns
// false when b has no miles price.
func (o option) offer(
	q award.Query,
	segs []award.Segment,
	cabin award.Cabin,
	b priceInfo,
) (award.Offer, bool) {
	fare := b.ReferenceFare.TotalFare
	if fare == nil || fare.Amount <= 0 {
		return award.Offer{}, false
	}
	if fare.CurrencyCode != "" && fare.CurrencyCode != "MILE" {
		return award.Offer{}, false
	}
	pax := max(q.Passengers, 1)
	points := int(fare.Amount)
	class := bookingClass(b)
	own := make([]award.Segment, len(segs))
	copy(own, segs)
	var airlines []string
	for i := range own {
		own[i].Cabin = cabin
		own[i].BookingCode = class
		if !slices.Contains(airlines, own[i].OperatingCarrier) {
			airlines = append(airlines, own[i].OperatingCarrier)
		}
	}
	cabinType := b.CabinType
	if cabinType == "" {
		cabinType = strings.ToUpper(string(cabin))
	}
	first, last := own[0], own[len(own)-1]
	duration := int(o.JourneyDuration / 60000)
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
		Stops:           len(own) - 1,
		Airlines:        airlines,
		Cabin:           cabin,
		Fare:            cabinType + "_" + b.BrandCode,
		Price: award.Price{
			PointsPerPassenger: points,
			PointsTotal:        points * pax,
			TaxesPerPassenger:  award.Money{Currency: "USD"},
			TaxesTotal:         award.Money{Currency: "USD"},
		},
		Seats:      o.seats(class),
		BookingURL: BookingURL,
		Notes:      []string{taxNote},
		Segments:   own,
	}, true
}

// bookingClass returns the booking class letter of fare brand b.
func bookingClass(b priceInfo) string {
	for _, list := range [][]string{b.ResBookDesig, b.FareBasisCodes} {
		if len(list) > 0 && list[0] != "" {
			return list[0][:1]
		}
	}
	if b.BrandCode != "" {
		return b.BrandCode[:1]
	}
	return ""
}

// seats returns the fewest seats left in class on any segment of o, read
// from each segment's class counts such as "X5", or nil when no segment
// says.
func (o option) seats(class string) *int {
	if class == "" {
		return nil
	}
	var low *int
	for _, s := range o.SegmentList {
		for _, part := range strings.Split(s.Comment, "|") {
			if len(part) < 2 || part[:1] != class {
				continue
			}
			n, err := strconv.Atoi(part[1:])
			if err != nil {
				continue
			}
			if low == nil || n < *low {
				low = &n
			}
		}
	}
	return low
}

// segments returns o's flights with their times placed in each airport's
// offset. It returns false when a time cannot be read.
func (o option) segments() ([]award.Segment, bool) {
	if len(o.SegmentList) == 0 {
		return nil, false
	}
	legs := make([]leg, 0, len(o.SegmentList))
	for _, s := range o.SegmentList {
		dep, err := time.Parse(localLayout, s.DepartureTime)
		if err != nil {
			return nil, false
		}
		arr, err := time.Parse(localLayout, s.ArrivalTime)
		if err != nil {
			return nil, false
		}
		legs = append(legs, leg{
			from:     s.Departure,
			to:       s.Arrival,
			dep:      dep,
			arr:      arr,
			duration: millis(s.DurationMs),
		})
	}
	placed := placeLegs(legs)
	out := make([]award.Segment, len(o.SegmentList))
	for i, s := range o.SegmentList {
		marketing := s.FlightCode.AirlineCode
		operating := marketing
		if s.Carrier != nil && s.Carrier.AirlineCode != "" {
			operating = s.Carrier.AirlineCode
		}
		if s.CodeSharing != nil && s.CodeSharing.AirlineCode != "" {
			operating = s.CodeSharing.AirlineCode
		}
		if marketing == "" {
			marketing = operating
		}
		aircraft := s.Equipment
		if aircraft == "" {
			aircraft = s.EquipmentCode
		}
		out[i] = award.Segment{
			Flight: marketing +
				flightNumber(s.FlightCode),
			MarketingCarrier: marketing,
			OperatingCarrier: operating,
			Origin:           s.Departure,
			Destination:      s.Arrival,
			Departure:        placed[i].dep,
			Arrival:          placed[i].arr,
			DurationMinutes: int(
				placed[i].arr.Sub(placed[i].dep).Minutes(),
			),
			Aircraft: aircraft,
		}
		if i > 0 {
			out[i-1].LayoverMinutes = int(
				placed[i].dep.Sub(placed[i-1].arr).Minutes(),
			)
		}
	}
	return out, true
}

// flightNumber returns f's number without leading zeros.
func flightNumber(f flight) string {
	n := strings.TrimLeft(f.FlightNumber, "0")
	if n == "" {
		return f.FlightNumber
	}
	return n
}

// millis returns ms milliseconds as a duration.
func millis(ms int64) time.Duration {
	return time.Duration(ms) * time.Millisecond
}
