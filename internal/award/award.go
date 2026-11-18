// Package award is the program-independent model of award availability: what
// a search asks for, what an offer is, and the fan-out that asks every
// loyalty program for every day of a search. Each program lives in a
// subpackage that adapts that program's own API to this model.
package award

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// maxDays is the most days one direction of a search may span. Every day is
// one request to every program asked, so the range is bounded to keep a
// single search from turning into a crawl.
const maxDays = 7

// Search is what a caller asks for: a route, a departure range, an optional
// return range, the passengers, the cabins wanted and the programs to ask.
type Search struct {
	// Origin is an IATA airport or city code, e.g. "SEA" or "NYC".
	Origin string
	// Destination is an IATA airport or city code.
	Destination string
	// Depart is the range of departure dates to search.
	Depart DateRange
	// Return is the range of return dates, nil for a one-way search. Award
	// seats are priced per direction, so a return is searched as a second
	// one-way from Destination to Origin.
	Return *DateRange
	// Passengers is how many adults travel, 1 to 9.
	Passengers int
	// Cabins keeps only offers in these cabins; empty keeps every cabin.
	Cabins []Cabin
	// Programs are the keys of the programs to ask; empty asks every one.
	Programs []string
}

// DateRange is an inclusive range of local dates.
type DateRange struct {
	// From is the first date.
	From time.Time
	// To is the last date, equal to From for a single day.
	To time.Time
}

// days returns every date in r, in order.
func (r DateRange) days() []time.Time {
	var out []time.Time
	for d := r.From; !d.After(r.To); d = d.AddDate(0, 0, 1) {
		out = append(out, d)
	}
	return out
}

// Query is one one-way, one-day search, the unit a program answers.
type Query struct {
	// Origin is an IATA airport or city code.
	Origin string
	// Destination is an IATA airport or city code.
	Destination string
	// Date is the local departure date.
	Date time.Time
	// Passengers is how many adults travel.
	Passengers int
}

// Direction is which leg of a search an offer answers.
type Direction string

// The legs of a search.
const (
	Outbound Direction = "outbound"
	Inbound  Direction = "return"
)

// Availability says how far an offer's price and seats can be trusted.
type Availability string

// The kinds of availability.
const (
	// Confirmed is live award space the program would sell now, at the
	// price shown.
	Confirmed Availability = "confirmed"
	// Estimated is a price from a chart or a cached calendar rather than a
	// live search; the seat may not be bookable.
	Estimated Availability = "estimated"
	// Unavailable is a flight the program lists with no award space.
	Unavailable Availability = "unavailable"
)

// Offer is one itinerary a program sells for points, in one fare.
type Offer struct {
	// Program is the key of the loyalty program selling it, e.g. "alaska".
	Program string `json:"program"`
	// Direction is the leg of the search it answers.
	Direction Direction `json:"direction"`
	// Availability says whether the price and seats are live.
	Availability Availability `json:"availability"`
	// Origin is the IATA airport code the itinerary departs from.
	Origin string `json:"origin"`
	// Destination is the IATA airport code the itinerary arrives at.
	Destination string `json:"destination"`
	// Departure is when the first segment leaves, in the origin's offset.
	Departure time.Time `json:"departure"`
	// Arrival is when the last segment lands, in the destination's offset.
	Arrival time.Time `json:"arrival"`
	// DurationMinutes is the door-to-door time including connections.
	DurationMinutes int `json:"durationMinutes"`
	// Stops is how many connections the itinerary makes.
	Stops int `json:"stops"`
	// Airlines are the IATA codes of every operating carrier, in order of
	// first appearance.
	Airlines []string `json:"airlines"`
	// Cabin is the cabin of the fare: economy, premium, business or first.
	Cabin Cabin `json:"cabin"`
	// MixedCabin is set when not every segment flies in Cabin.
	MixedCabin bool `json:"mixedCabin"`
	// Fare is the program's own name for the fare, e.g. "SAVER_MAIN".
	Fare string `json:"fare"`
	// Price is what the fare costs in points and cash.
	Price Price `json:"price"`
	// Seats is how many seats are left at this price, nil when the program
	// does not say.
	Seats *int `json:"seats"`
	// BookingURL is where a traveller books it, empty when there is none.
	BookingURL string `json:"bookingUrl,omitempty"`
	// Notes are anything else the program says about the offer, such as a
	// partner or a refund rule.
	Notes []string `json:"notes,omitempty"`
	// Segments are the flights of the itinerary, in order.
	Segments []Segment `json:"segments"`
}

// Price is what a fare costs.
type Price struct {
	// PointsPerPassenger is the points price for one passenger.
	PointsPerPassenger int `json:"pointsPerPassenger"`
	// PointsTotal is the points price for every passenger.
	PointsTotal int `json:"pointsTotal"`
	// TaxesPerPassenger is the taxes, fees and surcharges for one
	// passenger.
	TaxesPerPassenger Money `json:"taxesPerPassenger"`
	// TaxesTotal is the taxes, fees and surcharges for every passenger.
	TaxesTotal Money `json:"taxesTotal"`
}

// Segment is one flight of an itinerary.
type Segment struct {
	// Flight is the marketing carrier and number, e.g. "AA747".
	Flight string `json:"flight"`
	// MarketingCarrier is the IATA code of the airline selling it.
	MarketingCarrier string `json:"marketingCarrier"`
	// OperatingCarrier is the IATA code of the airline flying it.
	OperatingCarrier string `json:"operatingCarrier"`
	// Origin is the IATA code it departs from.
	Origin string `json:"origin"`
	// Destination is the IATA code it arrives at.
	Destination string `json:"destination"`
	// Departure is when it leaves, in the origin's offset.
	Departure time.Time `json:"departure"`
	// Arrival is when it lands, in the destination's offset.
	Arrival time.Time `json:"arrival"`
	// DurationMinutes is the time in the air.
	DurationMinutes int `json:"durationMinutes"`
	// LayoverMinutes is the connection time after it, 0 for the last one.
	LayoverMinutes int `json:"layoverMinutes"`
	// Aircraft is the aircraft type as the program names it.
	Aircraft string `json:"aircraft"`
	// Cabin is the cabin this segment is booked in.
	Cabin Cabin `json:"cabin"`
	// BookingCode is the fare class letter it is booked in.
	BookingCode string `json:"bookingCode"`
}

// Money is an amount in a currency.
type Money struct {
	// Amount is in the currency's major unit.
	Amount float64 `json:"amount"`
	// Currency is the ISO 4217 code.
	Currency string `json:"currency"`
}

// Cabin is a cabin of service, the same across programs.
type Cabin string

// The cabins every program's own names are mapped to.
const (
	Economy  Cabin = "economy"
	Premium  Cabin = "premium"
	Business Cabin = "business"
	First    Cabin = "first"
)

// ParseCabin maps a cabin name, case-insensitively, to a Cabin. It accepts
// "premium economy" and "premium_economy" for Premium. It returns false for
// a name it does not know.
func ParseCabin(name string) (Cabin, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "economy":
		return Economy, true
	case "premium", "premium economy", "premium_economy", "premiumeconomy":
		return Premium, true
	case "business":
		return Business, true
	case "first":
		return First, true
	}
	return "", false
}

// Program is one loyalty program that can be searched.
type Program interface {
	// Key is the program's short name, as offers and errors carry it.
	Key() string
	// Search returns the program's award offers for q, with Direction left
	// for the caller to set. It returns an error when the program cannot
	// be reached or answers with something it cannot read; no availability
	// is an empty slice, not an error.
	Search(ctx context.Context, q Query) ([]Offer, error)
}

// ProgramError is one program-day that failed during a search.
type ProgramError struct {
	// Program is the key of the program that failed.
	Program string `json:"program"`
	// Direction is the leg that was being searched.
	Direction Direction `json:"direction"`
	// Date is the departure date that was being searched, YYYY-MM-DD.
	Date string `json:"date"`
	// Message says what went wrong.
	Message string `json:"message"`
}

// Result is what a search across programs found.
type Result struct {
	// Offers are every offer found, outbound before return, then by
	// departure, then cheapest in points first.
	Offers []Offer `json:"offers"`
	// Errors are the program-days that failed, so one failing program does
	// not hide what the others found.
	Errors []ProgramError `json:"errors"`
}

// ErrInvalidSearch is wrapped by every error a search is rejected with.
var ErrInvalidSearch = errors.New("invalid search")

// locationCode matches a three-letter IATA airport or city code.
var locationCode = regexp.MustCompile(`^[A-Z]{3}$`)

// Validate reports whether s can be run. It returns an error wrapping
// ErrInvalidSearch that names the first field that is wrong.
func (s Search) Validate() error {
	switch {
	case !locationCode.MatchString(s.Origin):
		return fmt.Errorf("%w: origin %q is not an IATA code",
			ErrInvalidSearch, s.Origin)
	case !locationCode.MatchString(s.Destination):
		return fmt.Errorf("%w: destination %q is not an IATA code",
			ErrInvalidSearch, s.Destination)
	case s.Origin == s.Destination:
		return fmt.Errorf("%w: origin and destination are the same",
			ErrInvalidSearch)
	case s.Passengers < 1 || s.Passengers > 9:
		return fmt.Errorf("%w: passengers must be 1 to 9", ErrInvalidSearch)
	}
	if err := s.Depart.validate("departure"); err != nil {
		return err
	}
	if s.Return == nil {
		return nil
	}
	if err := s.Return.validate("return"); err != nil {
		return err
	}
	if s.Return.To.Before(s.Depart.From) {
		return fmt.Errorf("%w: return range ends before departure starts",
			ErrInvalidSearch)
	}
	return nil
}

// validate reports whether r is a usable range for the leg named leg. It
// returns an error wrapping ErrInvalidSearch when a date is missing, the
// range is backwards or it spans more than maxDays.
func (r DateRange) validate(leg string) error {
	switch {
	case r.From.IsZero():
		return fmt.Errorf("%w: %s date is missing", ErrInvalidSearch, leg)
	case r.To.Before(r.From):
		return fmt.Errorf("%w: %s range ends before it starts",
			ErrInvalidSearch, leg)
	case len(r.days()) > maxDays:
		return fmt.Errorf("%w: %s range spans more than %d days",
			ErrInvalidSearch, leg, maxDays)
	}
	return nil
}

// Searcher asks a set of programs, each paced on its own.
type Searcher struct {
	programs map[string]*paced
}

// Paced pairs a program with how hard it may be asked.
type Paced struct {
	// Program is the program to ask.
	Program Program
	// Concurrency is the most requests in flight to it at once; below 1
	// means 1.
	Concurrency int
	// MinInterval is the least time between the starts of two requests to
	// it; 0 does not space them.
	MinInterval time.Duration
}

// paced is a program with the gate its requests pass through.
type paced struct {
	program  Program
	slots    chan struct{}
	interval time.Duration
	mu       sync.Mutex
	next     time.Time
}

// NewSearcher builds a Searcher over programs, keyed by their Key.
func NewSearcher(programs ...Paced) *Searcher {
	s := &Searcher{programs: make(map[string]*paced, len(programs))}
	for _, p := range programs {
		n := max(p.Concurrency, 1)
		s.programs[p.Program.Key()] = &paced{
			program:  p.Program,
			slots:    make(chan struct{}, n),
			interval: p.MinInterval,
		}
	}
	return s
}

// Keys returns the keys of every program, sorted.
func (s *Searcher) Keys() []string {
	keys := make([]string, 0, len(s.programs))
	for k := range s.programs {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// job is one program asked about one direction on one day.
type job struct {
	program   *paced
	direction Direction
	query     Query
}

// Run asks the programs search names, or every program, about every day of
// every direction of search, in parallel within each program's pacing. A
// program-day that fails is reported in the result's Errors rather than
// failing the search. It returns an error wrapping ErrInvalidSearch when
// search is invalid or names a program that does not exist.
func (s *Searcher) Run(ctx context.Context, search Search) (Result, error) {
	if err := search.Validate(); err != nil {
		return Result{}, err
	}
	keys := search.Programs
	if len(keys) == 0 {
		keys = s.Keys()
	}
	jobs, err := s.jobs(search, keys)
	if err != nil {
		return Result{}, err
	}

	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		res = Result{Offers: []Offer{}, Errors: []ProgramError{}}
	)
	for _, j := range jobs {
		wg.Go(func() {
			offers, err := j.program.search(ctx, j.query)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				res.Errors = append(res.Errors, ProgramError{
					Program:   j.program.program.Key(),
					Direction: j.direction,
					Date:      j.query.Date.Format(time.DateOnly),
					Message:   err.Error(),
				})
				return
			}
			for _, o := range offers {
				if wanted(o, search.Cabins) {
					o.Direction = j.direction
					res.Offers = append(res.Offers, o)
				}
			}
		})
	}
	wg.Wait()

	slices.SortStableFunc(res.Offers, compareOffers)
	slices.SortFunc(res.Errors, func(a, b ProgramError) int {
		return strings.Compare(
			a.Program+string(a.Direction)+a.Date,
			b.Program+string(b.Direction)+b.Date,
		)
	})
	return res, nil
}

// jobs lists every program-direction-day search asks for. It returns an
// error wrapping ErrInvalidSearch when a key names no program.
func (s *Searcher) jobs(search Search, keys []string) ([]job, error) {
	var out []job
	for _, k := range keys {
		p, ok := s.programs[k]
		if !ok {
			return nil, fmt.Errorf("%w: unknown program %q",
				ErrInvalidSearch, k)
		}
		for _, d := range search.Depart.days() {
			out = append(out, job{p, Outbound, Query{
				search.Origin, search.Destination, d, search.Passengers,
			}})
		}
		if search.Return == nil {
			continue
		}
		for _, d := range search.Return.days() {
			out = append(out, job{p, Inbound, Query{
				search.Destination, search.Origin, d, search.Passengers,
			}})
		}
	}
	return out, nil
}

// search asks the program about q once a slot is free and its interval has
// passed. It returns ctx's error when ctx ends first, or the program's.
func (p *paced) search(ctx context.Context, q Query) ([]Offer, error) {
	select {
	case p.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-p.slots }()

	if wait := p.reserve(); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return p.program.Search(ctx, q)
}

// reserve claims the next start time of the program and returns how long to
// wait until it.
func (p *paced) reserve() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	start := p.next
	if start.Before(now) {
		start = now
	}
	p.next = start.Add(p.interval)
	return start.Sub(now)
}

// wanted reports whether o is in one of cabins, or cabins is empty.
func wanted(o Offer, cabins []Cabin) bool {
	return len(cabins) == 0 || slices.Contains(cabins, o.Cabin)
}

// compareOffers orders outbound before return, then by departure, then by
// points.
func compareOffers(a, b Offer) int {
	if a.Direction != b.Direction {
		if a.Direction == Outbound {
			return -1
		}
		return 1
	}
	if c := a.Departure.Compare(b.Departure); c != 0 {
		return c
	}
	return a.Price.PointsTotal - b.Price.PointsTotal
}
