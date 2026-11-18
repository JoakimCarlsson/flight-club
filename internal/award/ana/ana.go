// Package ana searches ANA Mileage Club award availability, priced in ANA
// miles, without a login.
//
// ANA's award booking engine needs a member session, so this package reads
// ANA's public award calendar instead: one static JSONP file per class of
// service and zone, CAL_<class>_<zone>_<tier>.js on cam.ana.co.jp, which
// answers a plain GET. Each file holds a header row of dates and a pair of
// rows, from and to Japan, for every nonstop ANA route of the zone, with a
// state per day for about six months ahead: 3 wide open, 2 open, 1 tight or
// waitlisted, 0 none. The calendar carries no flight times, flight numbers
// or prices, so every offer is Estimated and its miles come from ANA's
// published award chart by the season of the departure date.
package ana

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/joakimcarlsson/flight-club/internal/award"
)

// Key is the program key ANA offers carry.
const Key = "ana"

// carrier is ANA's IATA code.
const carrier = "NH"

// calendarURL is the folder ANA's award calendar files are served from.
const calendarURL = "https://cam.ana.co.jp/amctop/"

// bookingURL is ANA's international award search, where a member books.
const bookingURL = "https://aswbe-i.ana.co.jp/international_asw/pages/" +
	"award/search/roundtrip/award_search_roundtrip_input.xhtml" +
	"?CONNECTION_KIND=JPN&LANG=en"

// tier is the member tier whose calendar is read: the general member,
// whose seats any member can book.
const tier = "N"

// userAgent is a current desktop Chrome.
const userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36"

// maxBody is the most of a calendar file that is read; files are a few
// tens of kilobytes.
const maxBody = 4 << 20

// cacheTTL is how long a calendar file is reused before it is fetched
// again.
const cacheTTL = 10 * time.Minute

// zones are ANA's award zones that have a calendar, in the order searched.
var zones = []string{"Z2", "Z3", "Z4", "Z5", "Z6", "Z7", "ZA"}

// cabinClass maps each cabin to the class letter of its calendar files.
var cabinClass = map[award.Cabin]string{
	award.Economy:  "X",
	award.Premium:  "R",
	award.Business: "I",
	award.First:    "O",
}

// cabins are the cabins searched, cheapest first.
var cabins = []award.Cabin{
	award.Economy, award.Premium, award.Business, award.First,
}

// noCabin lists the zones that do not sell a cabin; their files are
// missing or left over from years ago.
var noCabin = map[award.Cabin][]string{
	award.Premium: {"Z2", "Z3"},
	award.First:   {"Z2", "Z3", "Z4", "ZA"},
}

// stateNames names the calendar's day states.
var stateNames = map[int]string{
	1: "few seats or waitlist",
	2: "seats available",
	3: "many seats available",
}

// cities maps the city codes of ANA's calendar airports to the airports.
var cities = map[string][]string{
	"TYO": {"NRT", "HND"},
	"OSA": {"KIX", "ITM"},
	"NYC": {"JFK", "EWR"},
	"WAS": {"IAD"},
	"CHI": {"ORD"},
	"LON": {"LHR"},
	"PAR": {"CDG"},
	"MIL": {"MXP"},
	"STO": {"ARN"},
	"SEL": {"ICN", "GMP"},
	"BJS": {"PEK", "PKX"},
	"SHA": {"PVG", "SHA"},
	"YTO": {"YYZ"},
	"BKK": {"BKK"},
	"JKT": {"CGK"},
}

// calDate matches a calendar header date, e.g. "2026/11/19".
var calDate = regexp.MustCompile(`^(\d{4})/(\d{1,2})/(\d{1,2})$`)

// Program searches ANA's public award calendar.
type Program struct {
	client *http.Client
	mu     sync.Mutex
	files  map[string]*file
}

// file is one cached calendar file.
type file struct {
	once    sync.Once
	fetched time.Time
	cal     *calendar
	err     error
}

// calendar is a parsed calendar file.
type calendar struct {
	dates  map[string]int
	routes []route
}

// route is one direction of a nonstop route with its state per date column.
type route struct {
	from, to string
	states   []int
}

// New builds a Program that makes its requests with client.
func New(client *http.Client) *Program {
	return &Program{client: client, files: map[string]*file{}}
}

// Key returns the program key, "ana".
func (p *Program) Key() string {
	return Key
}

// Search returns ANA's calendar availability for q as Estimated offers, one
// per nonstop route and cabin with seats on q.Date. It returns an error when
// no calendar file of any zone could be read; a route ANA's calendar does
// not list is an empty slice.
func (p *Program) Search(
	ctx context.Context,
	q award.Query,
) ([]award.Offer, error) {
	date := q.Date.Format(time.DateOnly)
	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		found  = map[award.Cabin][]award.Offer{}
		errs   []error
		loaded int
	)
	for _, cabin := range cabins {
		for _, zone := range zones {
			if slices.Contains(noCabin[cabin], zone) {
				continue
			}
			wg.Go(func() {
				cal, err := p.load(ctx, cabinClass[cabin], zone)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					errs = append(errs, err)
					return
				}
				loaded++
				found[cabin] = append(found[cabin],
					matches(cal, q, date, cabin, zone)...)
			})
		}
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if loaded == 0 && len(errs) > 0 {
		return nil, fmt.Errorf("ana: %w", errors.Join(errs...))
	}
	out := []award.Offer{}
	for _, cabin := range cabins {
		out = append(out, found[cabin]...)
	}
	return out, nil
}

// matches builds the offers of cal's routes between q's airports that have
// seats on date, in cabin, priced from zone's chart.
func matches(
	cal *calendar,
	q award.Query,
	date string,
	cabin award.Cabin,
	zone string,
) []award.Offer {
	col, ok := cal.dates[date]
	if !ok {
		return nil
	}
	var out []award.Offer
	for _, r := range cal.routes {
		if !airportIn(r.from, q.Origin) || !airportIn(r.to, q.Destination) {
			continue
		}
		if col >= len(r.states) || r.states[col] <= 0 {
			continue
		}
		out = append(out, offer(r, q, cabin, zone, r.states[col]))
	}
	return out
}

// airportIn reports whether airport is code or one of code's city airports.
func airportIn(airport, code string) bool {
	return airport == code || slices.Contains(cities[code], airport)
}

// offer builds the Estimated offer for route r on q's date in cabin, whose
// calendar state is state.
func offer(
	r route,
	q award.Query,
	cabin award.Cabin,
	zone string,
	state int,
) award.Offer {
	day := time.Date(
		q.Date.Year(), q.Date.Month(), q.Date.Day(), 0, 0, 0, 0, time.UTC,
	)
	miles, season := chartMiles(zone, cabin, day)
	notes := []string{
		"ANA award calendar: " + stateNames[min(state, 3)] +
			" on a nonstop ANA flight; times and taxes not given",
		"Miles from ANA's award chart: " + season,
	}
	if state == 1 && q.Passengers > 1 {
		notes = append(notes, "seats may be too few for every passenger")
	}
	return award.Offer{
		Program:      Key,
		Availability: award.Estimated,
		Origin:       r.from,
		Destination:  r.to,
		Departure:    day,
		Arrival:      day,
		Airlines:     []string{carrier},
		Cabin:        cabin,
		Fare:         "CAL_" + cabinClass[cabin] + "_" + zone,
		Price: award.Price{
			PointsPerPassenger: miles,
			PointsTotal:        miles * q.Passengers,
		},
		BookingURL: bookingURL,
		Notes:      notes,
		Segments: []award.Segment{{
			MarketingCarrier: carrier,
			OperatingCarrier: carrier,
			Origin:           r.from,
			Destination:      r.to,
			Departure:        day,
			Arrival:          day,
			Cabin:            cabin,
		}},
	}
}

// load returns the calendar file of class and zone, from the cache when
// it was fetched within cacheTTL. A failed fetch is not cached.
func (p *Program) load(
	ctx context.Context,
	class, zone string,
) (*calendar, error) {
	name := "CAL_" + class + "_" + zone + "_" + tier + ".js"
	p.mu.Lock()
	f, ok := p.files[name]
	if !ok || (f.cal != nil && time.Since(f.fetched) > cacheTTL) {
		f = &file{}
		p.files[name] = f
	}
	p.mu.Unlock()

	f.once.Do(func() {
		cal, err := p.fetch(ctx, name)
		p.mu.Lock()
		defer p.mu.Unlock()
		f.cal, f.err, f.fetched = cal, err, time.Now()
	})
	if f.err != nil {
		p.mu.Lock()
		if p.files[name] == f {
			delete(p.files, name)
		}
		p.mu.Unlock()
	}
	return f.cal, f.err
}

// fetch downloads and parses the calendar file name. It returns an error
// when the file cannot be fetched, is not there or cannot be read.
func (p *Program) fetch(ctx context.Context, name string) (*calendar, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		calendarURL+name,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Referer", calendarURL)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s", name, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", name, err)
	}
	cal, err := parseCalendar(body)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return cal, nil
}

// parseCalendar reads a calendar file, cal([[header], [row], ...]). It
// returns an error when the file is not that shape.
func parseCalendar(body []byte) (*calendar, error) {
	body = bytes.TrimSpace(body)
	body = bytes.TrimPrefix(body, []byte("cal("))
	body = bytes.TrimSuffix(body, []byte(";"))
	body = bytes.TrimSuffix(body, []byte(")"))
	var rows [][]json.RawMessage
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("reading calendar: %w", err)
	}
	if len(rows) == 0 {
		return nil, errors.New("calendar has no header")
	}
	cal := &calendar{dates: map[string]int{}}
	for i, raw := range rows[0] {
		if d, ok := headerDate(raw); ok && i >= 2 {
			cal.dates[d] = i - 2
		}
	}
	for _, row := range rows[1:] {
		if r, ok := parseRoute(row); ok {
			cal.routes = append(cal.routes, r)
		}
	}
	return cal, nil
}

// headerDate reads a header cell as a YYYY-MM-DD date, false when it is not
// a date.
func headerDate(raw json.RawMessage) (string, bool) {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	m := calDate.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	y, _ := strconv.Atoi(m[1])
	mo, _ := strconv.Atoi(m[2])
	d, _ := strconv.Atoi(m[3])
	return fmt.Sprintf("%04d-%02d-%02d", y, mo, d), true
}

// parseRoute reads a route row, ["NRT","LAX",state,...], false when the
// row has no airports.
func parseRoute(row []json.RawMessage) (route, bool) {
	if len(row) < 2 {
		return route{}, false
	}
	var r route
	if json.Unmarshal(row[0], &r.from) != nil ||
		json.Unmarshal(row[1], &r.to) != nil {
		return route{}, false
	}
	for _, raw := range row[2:] {
		r.states = append(r.states, stateOf(raw))
	}
	return r, true
}

// stateOf reads a day state written as a number or a string, 0 when it is
// neither.
func stateOf(raw json.RawMessage) int {
	var n float64
	if json.Unmarshal(raw, &n) == nil {
		return int(n)
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		v, _ := strconv.Atoi(s)
		return v
	}
	return 0
}
