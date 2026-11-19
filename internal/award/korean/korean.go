// Package korean searches Korean Air SKYPASS award availability on Korean
// Air's own flights.
//
// The live award booking on koreanair.com ("Book with miles") opens a
// login dialog for anyone signed out, but the site also publishes an
// "Award Seat Availability" calendar that needs no login: for one route
// and month it lists every nonstop Korean Air flight and, per award class,
// whether a seat is left. The calendar is refreshed once a day, so its
// offers are estimates. It names flights and departure times only, so
// arrivals are not known, and it carries no prices, so the miles come
// from the SKYPASS redemption table at off-peak rates.
//
// The calendar's API sits behind Akamai Bot Manager, which refuses a plain
// request, and it fails without the headers the site's own client sends.
// The search is therefore run the way the site runs it: the stealth
// browser opens the calendar page with the route prefilled through its
// deep link, presses its search button, and reads the answer off the
// site's own request.
package korean

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/joakimcarlsson/flight-club/internal/award"
	"github.com/joakimcarlsson/flight-club/internal/browser"
)

// Key is the program key Korean Air offers carry.
const Key = "korean"

// calendarURL is the award seat availability page, which the search runs
// on and travellers are sent to.
const calendarURL = "https://www.koreanair.com/booking/book-and-manage/" +
	"award-seat-availability"

// apiPath is the part of the calendar API's URL that identifies its
// answers among the responses the page loads.
const apiPath = "/api/hmp/bonusSeatView/bonusSeatView"

// resultID is the id of the element the page script writes the answer to.
const resultID = "ke-award"

// loadTimeout bounds each navigation of the calendar page.
const loadTimeout = 60 * time.Second

// warmUpDelay is how long the calendar page is given to load, earn its
// bot protection cookies and prefill the route before its search button
// is pressed.
const warmUpDelay = 8 * time.Second

// settleRounds and settleDelay are how long the page is given, once the
// answer is in, before its markup is read.
const (
	settleRounds = 2
	settleDelay  = time.Second
)

// cacheTTL is how long a month's calendar is reused. The site refreshes
// it once a day, and one answer covers every day of the month.
const cacheTTL = time.Hour

// carrier is Korean Air's IATA code.
const carrier = "KE"

// awardClass is one award booking class the calendar reports.
type awardClass struct {
	cabin award.Cabin
	name  string
}

// awardClasses are the award ticket classes the calendar reports, by
// booking class. Upgrade classes (Z and P) are left out, as they are not
// award tickets, and so is premium economy (N), which the redemption
// table has no price for.
var awardClasses = map[string]awardClass{
	"X": {award.Economy, "Economy"},
	"O": {award.Business, "Prestige"},
	"A": {award.First, "First"},
}

// Program searches Korean Air's award seat calendar through a stealth
// browser.
type Program struct {
	browser *browser.Browser
	mu      sync.Mutex
	months  map[string]*month
}

// month is one route-month calendar, fetched once and shared by every
// search of its days.
type month struct {
	done    chan struct{}
	fetched time.Time
	answer  *answer
	err     error
}

// New builds a Program that loads koreanair.com in b.
func New(b *browser.Browser) *Program {
	return &Program{browser: b, months: map[string]*month{}}
}

// Key returns the program key, "korean".
func (p *Program) Key() string {
	return Key
}

// answer is the calendar API's reply for one route and month.
type answer struct {
	Code             string `json:"code"`
	Message          string `json:"message"`
	DepartureAirport string `json:"departureAirport"`
	ArrivalAirport   string `json:"arrivalAirport"`
	FlightList       []day  `json:"flightList"`
	Error            string `json:"error"`
}

// day is the calendar's flights on one date.
type day struct {
	DepartureDate    string   `json:"departureDate"`
	FlightDetailList []detail `json:"flightDetailList"`
}

// detail is one flight in one award class.
type detail struct {
	DepartureTime string `json:"departureTime"`
	FlightNumber  string `json:"flightNumber"`
	AvailableSeat bool   `json:"availableSeat"`
	BookingClass  string `json:"bookingClass"`
}

// errNoAnswer is returned when the calendar page never asked its API,
// such as when the site does not offer the route.
var errNoAnswer = errors.New(
	"korean: the award calendar did not answer for this route",
)

// errBlocked is returned when the site's bot protection denied the page.
var errBlocked = errors.New(
	"korean: search blocked by the site's bot protection (Access Denied)",
)

// Search returns the Korean Air flights of q's date with award seats left,
// one offer per flight and cabin. City codes are searched as each of
// their airports. It returns an error when the browser cannot load the
// calendar, the site refuses the search or answers with something that
// cannot be read.
func (p *Program) Search(
	ctx context.Context,
	q award.Query,
) ([]award.Offer, error) {
	out := []award.Offer{}
	var lastErr error
	searched := 0
	for _, from := range airportsOf(q.Origin) {
		for _, to := range airportsOf(q.Destination) {
			if from == to {
				continue
			}
			searched++
			a, err := p.month(ctx, from, to, q.Date)
			if err != nil {
				lastErr = err
				continue
			}
			out = append(out, offers(a, from, to, q)...)
		}
	}
	if len(out) == 0 && lastErr != nil && searched > 0 {
		return nil, lastErr
	}
	return out, nil
}

// month returns the calendar of the route from to to for the month of
// date, from the cache when it is fresh, and fetches it otherwise.
// Concurrent searches of one month share one fetch. It returns the fetch's
// error, which is not cached.
func (p *Program) month(
	ctx context.Context,
	from, to string,
	date time.Time,
) (*answer, error) {
	key := from + to + date.Format("200601")
	p.mu.Lock()
	m, ok := p.months[key]
	if ok {
		select {
		case <-m.done:
			if m.err != nil || time.Since(m.fetched) > cacheTTL {
				ok = false
			}
		default:
		}
	}
	if !ok {
		m = &month{done: make(chan struct{})}
		p.months[key] = m
		p.mu.Unlock()
		m.answer, m.err = p.fetch(ctx, from, to, date)
		m.fetched = time.Now()
		close(m.done)
		return m.answer, m.err
	}
	p.mu.Unlock()
	select {
	case <-m.done:
		return m.answer, m.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// fetch runs the calendar search for the route from to to and the month of
// date in the browser and returns its answer. It returns an error when the
// page cannot be loaded, the site refuses the search, or the answer
// cannot be read or reports a failure.
func (p *Program) fetch(
	ctx context.Context,
	from, to string,
	date time.Time,
) (*answer, error) {
	page := deepLink(from, to, date)
	captured, err := p.browser.Collect(ctx, page+"#"+resultID,
		browser.CollectOpts{
			Timeout:     loadTimeout,
			WarmUp:      page,
			WarmUpDelay: warmUpDelay,
			Prepare:     prepareScript,
			Scrolls:     settleRounds,
			ScrollDelay: settleDelay,
			Accept:      func(string, string) bool { return false },
			CaptureBody: func(u, _ string) bool {
				return strings.Contains(u, apiPath)
			},
		})
	if err != nil {
		return nil, fmt.Errorf("korean: %w", err)
	}
	var bodies [][]byte
	for _, b := range captured.Bodies {
		bodies = append(bodies, b.Data)
	}
	if body, ok := resultText(captured.HTML); ok {
		bodies = append(bodies, body)
	}
	return pickAnswer(bodies, captured.HTML)
}

// pickAnswer returns the first of bodies that is a calendar answer. html
// is the page's markup, read to tell a denied page from one that never
// answered. It returns errBlocked, errNoAnswer, or an error naming the
// failure the site reported.
func pickAnswer(bodies [][]byte, page string) (*answer, error) {
	var siteErr error
	for _, body := range bodies {
		body = bytes.TrimSpace(body)
		if len(body) == 0 || body[0] != '{' {
			continue
		}
		var a answer
		if err := json.Unmarshal(body, &a); err != nil {
			continue
		}
		switch {
		case a.FlightList != nil:
			return &a, nil
		case a.Error != "":
			continue
		case a.Code != "" || a.Message != "":
			siteErr = fmt.Errorf("korean: site error %s: %s",
				a.Code, a.Message)
		}
	}
	switch {
	case siteErr != nil:
		return nil, siteErr
	case strings.Contains(page, "Access Denied"):
		return nil, errBlocked
	}
	return nil, errNoAnswer
}

// resultPattern matches the element the page script writes the answer to.
var resultPattern = regexp.MustCompile(
	`(?s)<pre id="` + resultID + `">(.*?)</pre>`,
)

// resultText returns the answer the page script wrote into page, and
// false when it wrote none.
func resultText(page string) ([]byte, bool) {
	m := resultPattern.FindStringSubmatch(page)
	if m == nil {
		return nil, false
	}
	return []byte(html.UnescapeString(m[1])), true
}

// prepareScript runs on the calendar page once the route is prefilled. It
// watches the page's requests for the calendar API's answer, presses the
// page's search button, and once the answer is in, replaces the document
// with it. Rewriting the document fires the load event the browser waits
// for when it is sent to the page's own fragment, and leaves the answer
// in the markup it reads. When there is no button or no answer, it writes
// an error object instead.
const prepareScript = `(() => {
const id = "` + resultID + `";
let written = false;
const done = text => {
  if (written) { return; }
  written = true;
  const pre = document.createElement("pre");
  pre.id = id;
  pre.textContent = text;
  setTimeout(() => {
    document.open();
    document.write("<html><body></body></html>");
    document.body.appendChild(pre);
    document.close();
  }, 500);
};
const open = XMLHttpRequest.prototype.open;
XMLHttpRequest.prototype.open = function (method, target) {
  if (String(target).includes("` + apiPath + `")) {
    this.addEventListener("loadend", () => done(this.responseText));
  }
  return open.apply(this, arguments);
};
const button = [...document.querySelectorAll("kds-button")]
  .find(b => /search/i.test(b.textContent));
if (!button) {
  done('{"error":"no search button"}');
  return true;
}
button.dispatchEvent(new CustomEvent("eventKdsButtonClicked"));
setTimeout(() => done('{"error":"no answer"}'), 20000);
return true;
})()`

// deepLink is the calendar page with the route from to to and the month of
// date prefilled, as the site's own links to it are built.
func deepLink(from, to string, date time.Time) string {
	v := url.Values{}
	v.Set("departure", from)
	v.Set("arrival", to)
	v.Set("departureDate", date.Format("2006-01")+"-01")
	return calendarURL + "?" + v.Encode()
}

// offers turns the calendar a for the route from to to into the offers of
// q's date, one per flight and award class with a seat left.
func offers(a *answer, from, to string, q award.Query) []award.Offer {
	want := q.Date.Format("20060102")
	out := []award.Offer{}
	for _, d := range a.FlightList {
		if d.DepartureDate != want {
			continue
		}
		for _, f := range d.FlightDetailList {
			if !f.AvailableSeat {
				continue
			}
			o, ok := offer(f, from, to, q)
			if ok {
				out = append(out, o)
			}
		}
	}
	return out
}

// offer builds the offer of flight f in its award class on the route from
// to to, for q. It returns false when the class is not an award ticket,
// the redemption table has no award in its cabin on the route, or its
// departure time cannot be read.
func offer(
	f detail,
	from, to string,
	q award.Query,
) (award.Offer, bool) {
	class, ok := awardClasses[f.BookingClass]
	if !ok {
		return award.Offer{}, false
	}
	clock, err := time.Parse("15:04", f.DepartureTime)
	if err != nil {
		return award.Offer{}, false
	}
	dep := time.Date(q.Date.Year(), q.Date.Month(), q.Date.Day(),
		clock.Hour(), clock.Minute(), 0, 0, location(from))
	pax := max(q.Passengers, 1)
	miles, chartNote, ok := chartMiles(from, to, class.cabin)
	if !ok {
		return award.Offer{}, false
	}
	notes := []string{
		"From Korean Air's award seat calendar, refreshed once a day; " +
			"book with miles after signing in",
		"Arrival time not given by the calendar",
		chartNote,
		"Taxes and fuel surcharges not included",
	}
	flight := strings.ToUpper(strings.TrimSpace(f.FlightNumber))
	if !strings.HasPrefix(flight, carrier) {
		flight = carrier + flight
	}
	zero := award.Money{Currency: "USD"}
	return award.Offer{
		Program:      Key,
		Availability: award.Estimated,
		Origin:       from,
		Destination:  to,
		Departure:    dep,
		Arrival:      dep,
		Airlines:     []string{carrier},
		Cabin:        class.cabin,
		Fare:         class.name + " award (" + f.BookingClass + ")",
		Price: award.Price{
			PointsPerPassenger: miles,
			PointsTotal:        miles * pax,
			TaxesPerPassenger:  zero,
			TaxesTotal:         zero,
		},
		BookingURL: deepLink(from, to, q.Date),
		Notes:      notes,
		Segments: []award.Segment{{
			Flight:           flight,
			MarketingCarrier: carrier,
			OperatingCarrier: carrier,
			Origin:           from,
			Destination:      to,
			Departure:        dep,
			Arrival:          dep,
			Cabin:            class.cabin,
			BookingCode:      f.BookingClass,
		}},
	}, true
}
