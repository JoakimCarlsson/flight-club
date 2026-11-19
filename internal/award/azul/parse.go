package azul

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/joakimcarlsson/flight-club/internal/award"
)

// answer is the part of the availability API's answer that is read.
type answer struct {
	Data *struct {
		Trips []trip `json:"trips"`
	} `json:"data"`
}

// trip is one direction of the search with its journeys.
type trip struct {
	Currency string    `json:"currencyCode"`
	Journeys []journey `json:"journeys"`
}

// journey is one itinerary with its fares.
type journey struct {
	Status *struct {
		Available bool `json:"available"`
	} `json:"status"`
	Fares      []fare `json:"fares"`
	Identifier struct {
		Duration *duration `json:"duration"`
	} `json:"identifier"`
	Segments []segment `json:"segments"`
}

// fare is one cabin's points fare for a journey.
type fare struct {
	Available    bool `json:"available"`
	ProductClass struct {
		Code     string `json:"code"`
		Category string `json:"category"`
		Name     string `json:"name"`
	} `json:"productClass"`
	ClassOfService string `json:"classOfService"`
	PaxPoints      []struct {
		AmountLevel int     `json:"amountLevel"`
		Levels      []level `json:"levels"`
	} `json:"paxPoints"`
}

// level is one passenger type's price at one points-and-cash mix.
type level struct {
	PaxType   string  `json:"paxType"`
	FareMoney float64 `json:"fareMoney"`
	Points    struct {
		Amount float64 `json:"amount"`
	} `json:"points"`
	TaxesAndFees   float64 `json:"taxesAndFees"`
	ConvenienceFee float64 `json:"convenienceFee"`
	TotalMoney     float64 `json:"totalMoney"`
	Currency       string  `json:"currencyCode"`
}

// segment is one flight of a journey.
type segment struct {
	Equipment *struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"equipment"`
	Identifier struct {
		OperatedBy  string `json:"operatedBy"`
		Carrier     string `json:"carrierCode"`
		FlightNo    string `json:"flightNumber"`
		Origin      string `json:"departureStation"`
		Destination string `json:"arrivalStation"`
		Departure   string `json:"std"`
		Arrival     string `json:"sta"`
	} `json:"identifier"`
	Legs []struct {
		Identifier struct {
			Duration *duration `json:"duration"`
		} `json:"identifier"`
		Info *struct {
			Remaining *int `json:"remainingSeats"`
		} `json:"legInfo"`
	} `json:"legs"`
}

// duration is a span as the API writes it.
type duration struct {
	Days    int `json:"days"`
	Hours   int `json:"hours"`
	Minutes int `json:"minutes"`
}

// minutes returns d in minutes, 0 for a nil d.
func (d *duration) minutes() int {
	if d == nil {
		return 0
	}
	return d.Days*24*60 + d.Hours*60 + d.Minutes
}

// localLayout is the layout of the answer's offset-less local times.
const localLayout = "2006-01-02T15:04:05"

// parse turns an availability API answer into the offers for q, booked
// at booking. It returns an error when body is not the API's JSON or
// carries no data; an answer with no journeys gives no offers.
func parse(
	body []byte,
	q award.Query,
	booking string,
) ([]award.Offer, error) {
	var a answer
	if err := json.Unmarshal(body, &a); err != nil {
		return nil, fmt.Errorf("azul: unreadable answer: %s", compact(body))
	}
	out := []award.Offer{}
	if a.Data == nil {
		if noFlights(body) {
			return out, nil
		}
		return nil, fmt.Errorf("azul: search failed: %s", compact(body))
	}
	n := max(q.Passengers, 1)
	for _, t := range a.Data.Trips {
		for _, j := range t.Journeys {
			out = append(out, offers(j, t.Currency, n, booking)...)
		}
	}
	return out, nil
}

// noFlights reports whether an answer without data says the route or
// day simply has no flights to sell.
func noFlights(body []byte) bool {
	s := strings.ToUpper(string(body))
	for _, m := range []string{
		"NO FLIGHTS", "NOT FOUND", "NOFLIGHT", "NO_FLIGHT",
		"INDISPON", "NÃO HÁ VOOS", "NAO HA VOOS", "MARKET",
	} {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// compact shortens raw JSON to a line fit for an error message.
func compact(raw []byte) string {
	s := strings.Join(strings.Fields(string(raw)), " ")
	if len(s) > 100 {
		s = s[:100] + "…"
	}
	if s == "" {
		return "empty answer"
	}
	return s
}

// offers turns one journey into an offer per available cabin fare, for
// n passengers priced in currency, booked at booking. A journey whose
// times cannot be read gives no offers.
func offers(
	j journey,
	currency string,
	n int,
	booking string,
) []award.Offer {
	if j.Status != nil && !j.Status.Available || len(j.Segments) == 0 {
		return nil
	}
	segs, airlines, seats, ok := segments(j.Segments)
	if !ok {
		return nil
	}
	first, last := segs[0], segs[len(segs)-1]
	total := j.Identifier.Duration.minutes()
	if total <= 0 {
		total = int(last.Arrival.Sub(first.Departure).Minutes())
	}
	var out []award.Offer
	for _, f := range j.Fares {
		lv, ok := pointsLevel(f)
		if !f.Available || !ok {
			continue
		}
		cabin := cabinOf(f.ProductClass.Category, f.ProductClass.Name)
		cur := lv.Currency
		if cur == "" {
			cur = currency
		}
		taxes := lv.TotalMoney - lv.FareMoney
		fs := make([]award.Segment, len(segs))
		for i, s := range segs {
			s.Cabin = cabin
			s.BookingCode = f.ClassOfService
			fs[i] = s
		}
		o := award.Offer{
			Program:         Key,
			Availability:    award.Confirmed,
			Origin:          first.Origin,
			Destination:     last.Destination,
			Departure:       first.Departure,
			Arrival:         last.Arrival,
			DurationMinutes: total,
			Stops:           len(segs) - 1,
			Airlines:        airlines,
			Cabin:           cabin,
			Fare:            fareName(f),
			Price: award.Price{
				PointsPerPassenger: int(lv.Points.Amount),
				PointsTotal:        int(lv.Points.Amount) * n,
				TaxesPerPassenger: award.Money{
					Amount: round(taxes), Currency: cur,
				},
				TaxesTotal: award.Money{
					Amount: round(taxes * float64(n)), Currency: cur,
				},
			},
			BookingURL: booking,
			Segments:   fs,
		}
		if cabin == award.Economy && seats != nil {
			v := *seats
			o.Seats = &v
		}
		if lv.ConvenienceFee > 0 {
			o.Notes = append(o.Notes, fmt.Sprintf(
				"Taxes include a %s %.2f booking fee",
				cur, lv.ConvenienceFee))
		}
		out = append(out, o)
	}
	return out
}

// pointsLevel returns the adult price of f paid wholly in points, or the
// mix with the least cash when there is none. It returns false when f has
// no points price.
func pointsLevel(f fare) (level, bool) {
	var best level
	found := false
	for _, p := range f.PaxPoints {
		for _, l := range p.Levels {
			if l.PaxType != "" && l.PaxType != "ADT" {
				continue
			}
			if l.Points.Amount <= 0 {
				continue
			}
			if !found || l.FareMoney < best.FareMoney {
				best, found = l, true
			}
		}
	}
	return best, found
}

// fareName is the traveller-facing name of f, e.g. "Business (ZE)".
func fareName(f fare) string {
	name := f.ProductClass.Name
	if name == "" {
		name = f.ProductClass.Category
	}
	if f.ProductClass.Code == "" {
		return name
	}
	return name + " (" + f.ProductClass.Code + ")"
}

// cabinOf maps a fare's category and name to a cabin; anything not
// named as a premium cabin is Economy.
func cabinOf(category, name string) award.Cabin {
	s := strings.ToLower(category + " " + name)
	switch {
	case strings.Contains(s, "first"):
		return award.First
	case strings.Contains(s, "business"), strings.Contains(s, "executiv"):
		return award.Business
	case strings.Contains(s, "premium"):
		return award.Premium
	}
	return award.Economy
}

// round rounds v to cents.
func round(v float64) float64 {
	return math.Round(v*100) / 100
}

// segments turns the journey's flights into offer segments with offset
// times, and returns the carriers in order of first appearance and the
// fewest seats left on any leg, nil when unknown. It returns false when a
// time cannot be read.
func segments(
	in []segment,
) ([]award.Segment, []string, *int, bool) {
	var out []award.Segment
	var airlines []string
	var seats *int
	var offset *int
	for _, s := range in {
		id := s.Identifier
		dep, err1 := time.Parse(localLayout, id.Departure)
		arr, err2 := time.Parse(localLayout, id.Arrival)
		if err1 != nil || err2 != nil {
			return nil, nil, nil, false
		}
		depOff := originOffset(id.Origin, dep, offset)
		flown := 0
		for _, l := range s.Legs {
			flown += l.Identifier.Duration.minutes()
			if l.Info != nil && l.Info.Remaining != nil &&
				(seats == nil || *l.Info.Remaining < *seats) {
				v := *l.Info.Remaining
				seats = &v
			}
		}
		arrOff := arrivalOffset(id.Destination, dep, arr, depOff, flown)
		offset = &arrOff
		departure := inOffset(dep, id.Origin, depOff)
		arrival := inOffset(arr, id.Destination, arrOff)
		if flown <= 0 {
			flown = int(arrival.Sub(departure).Minutes())
		}
		op := id.OperatedBy
		if op == "" {
			op = id.Carrier
		}
		airlines = appendNew(airlines, op)
		aircraft := ""
		if s.Equipment != nil {
			aircraft = s.Equipment.Name
			if aircraft == "" {
				aircraft = s.Equipment.Type
			}
		}
		out = append(out, award.Segment{
			Flight:           id.Carrier + strings.TrimSpace(id.FlightNo),
			MarketingCarrier: id.Carrier,
			OperatingCarrier: op,
			Origin:           id.Origin,
			Destination:      id.Destination,
			Departure:        departure,
			Arrival:          arrival,
			DurationMinutes:  flown,
			Aircraft:         aircraft,
		})
	}
	for i := 0; i+1 < len(out); i++ {
		gap := out[i+1].Departure.Sub(out[i].Arrival).Minutes()
		out[i].LayoverMinutes = max(int(gap), 0)
	}
	if seats != nil && *seats <= 0 {
		seats = nil
	}
	return out, airlines, seats, len(out) > 0
}

// appendNew appends code to list unless it is empty or already there.
func appendNew(list []string, code string) []string {
	if code == "" {
		return list
	}
	for _, c := range list {
		if c == code {
			return list
		}
	}
	return append(list, code)
}

// originOffset is the UTC offset in seconds of airport at the local time
// local: the airport's zone when known, else prev, the offset the journey
// arrived in, else Brasília time.
func originOffset(airport string, local time.Time, prev *int) int {
	if off, ok := zoneOffset(airport, local); ok {
		return off
	}
	if prev != nil {
		return *prev
	}
	return brasilia
}

// arrivalOffset is the UTC offset in seconds of airport when a flight
// leaving at local dep in offset depOff lands at local arr after flown
// minutes. It uses the airport's zone when known, else works it out from
// the flight time, else assumes depOff.
func arrivalOffset(
	airport string,
	dep, arr time.Time,
	depOff, flown int,
) int {
	if off, ok := zoneOffset(airport, arr); ok {
		return off
	}
	if flown <= 0 {
		return depOff
	}
	landed := dep.Add(-time.Duration(depOff) * time.Second).
		Add(time.Duration(flown) * time.Minute)
	diff := arr.Sub(landed).Seconds()
	return int(math.Round(diff/900) * 900)
}

// inOffset gives the wall-clock time local the fixed offset off, named
// after airport.
func inOffset(local time.Time, airport string, off int) time.Time {
	return time.Date(local.Year(), local.Month(), local.Day(),
		local.Hour(), local.Minute(), local.Second(), 0,
		time.FixedZone(airport, off))
}
