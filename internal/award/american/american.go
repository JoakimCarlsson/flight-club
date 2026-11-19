// Package american searches American Airlines AAdvantage award
// availability. AAdvantage awards are dynamically priced and sold in the
// site's product columns (Main, Premium Economy, Business, First), with
// partner flights such as British Airways filed in the column of their
// cabin. No member login is needed.
//
// aa.com sits behind Akamai Bot Manager: a plain request is refused with a
// 403 and a cold deep link gets "Access Denied". The search is therefore
// run the way a traveller runs it: the stealth browser opens the home page,
// then the award search link in the same context, which redirects to the
// choose-flights page. That page is rendered on the server and carries the
// answer in its Angular transfer state (<script id="ng-state">), under
// SearchData.itineraryResult.slices; the page's own HTML response is
// captured as it loads. Akamai flags bursts quickly, so the program should
// be paced to one search at a time with half a minute between.
package american

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/joakimcarlsson/flight-club/internal/award"
	"github.com/joakimcarlsson/flight-club/internal/browser"
)

// Key is the program key American offers carry.
const Key = "american"

// homeURL is the site's home page, opened before the search so the bot
// protection's sensor script earns its cookies on an ordinary visit.
const homeURL = "https://www.aa.com/homePage.do"

// searchURL is the award search link a traveller searches and books from,
// followed by its query.
const searchURL = "https://www.aa.com/booking/search?"

// resultsPath identifies the choose-flights page among the responses the
// search link loads.
const resultsPath = "/booking/choose-flights"

// noFlightsCode is the site's error code for a search with no flights,
// such as a route it does not serve or a date outside its schedule.
const noFlightsCode = "858"

// loadTimeout bounds each navigation.
const loadTimeout = 60 * time.Second

// warmUpDelay is how long the home page is given to run its scripts.
const warmUpDelay = 10 * time.Second

// settleRounds and settleDelay are how long the results page is given to
// finish loading: settleRounds waits of settleDelay each.
const (
	settleRounds = 2
	settleDelay  = 3 * time.Second
)

// stateScript finds the Angular transfer state in the results page.
var stateScript = regexp.MustCompile(
	`(?s)<script id="ng-state" type="application/json">(.*?)</script>`,
)

// titleTag finds the page title.
var titleTag = regexp.MustCompile(`(?s)<title>(.*?)</title>`)

// errBlocked is returned when Akamai answers with its "Access Denied" or
// challenge page instead of results.
var errBlocked = errors.New(
	"american: search blocked by the site's bot protection",
)

// Program searches aa.com through a stealth browser.
type Program struct {
	browser *browser.Browser
}

// New builds a Program that loads aa.com in b.
func New(b *browser.Browser) *Program {
	return &Program{browser: b}
}

// Key returns the program key, "american".
func (p *Program) Key() string {
	return Key
}

// Search returns American's award offers for q. It returns an error when
// the browser cannot load the results, the site's bot protection stops the
// search, or the answer cannot be read. A route or date American does not
// sell gives no offers and no error. City codes such as NYC are passed to
// the site, which searches every airport of the city.
func (p *Program) Search(
	ctx context.Context,
	q award.Query,
) ([]award.Offer, error) {
	booking := bookingURL(q)
	captured, err := p.browser.Collect(ctx, booking, browser.CollectOpts{
		Timeout:     loadTimeout,
		WarmUp:      homeURL,
		WarmUpDelay: warmUpDelay,
		Scrolls:     settleRounds,
		ScrollDelay: settleDelay,
		Accept:      func(string, string) bool { return false },
		CaptureBody: func(u, ct string) bool {
			return strings.Contains(u, resultsPath) &&
				strings.Contains(ct, "html")
		},
	})
	if err != nil {
		return nil, fmt.Errorf("american: %w", err)
	}
	pages := make([]string, 0, len(captured.Bodies)+1)
	for _, b := range captured.Bodies {
		pages = append(pages, string(b.Data))
	}
	pages = append(pages, captured.HTML)
	return fromPages(pages, captured.URL, q, booking)
}

// bookingURL returns the award search link for q.
func bookingURL(q award.Query) string {
	slices := fmt.Sprintf(
		`[{"orig":%q,"origNearby":false,"dest":%q,`+
			`"destNearby":false,"date":%q}]`,
		strings.ToUpper(q.Origin),
		strings.ToUpper(q.Destination),
		q.Date.Format("2006-01-02"),
	)
	pax := strconv.Itoa(passengers(q))
	v := url.Values{}
	v.Set("locale", "en_US")
	v.Set("pax", pax)
	v.Set("adult", pax)
	v.Set("type", "OneWay")
	v.Set("searchType", "Award")
	v.Set("cabin", "")
	v.Set("carriers", "ALL")
	v.Set("slices", slices)
	return searchURL + v.Encode()
}

// passengers returns how many adults q asks for, at least one.
func passengers(q award.Query) int {
	return max(q.Passengers, 1)
}

// fromPages reads the offers for q, booked at booking, from the HTML of the
// pages the search loaded, the results page first. final is the address the
// browser ended on. It returns errBlocked when the site refused the search
// and an error when no page carries a readable answer.
func fromPages(
	pages []string,
	final string,
	q award.Query,
	booking string,
) ([]award.Offer, error) {
	for _, html := range pages {
		m := stateScript.FindStringSubmatch(html)
		if m == nil {
			continue
		}
		var st state
		if err := json.Unmarshal([]byte(m[1]), &st); err != nil {
			return nil, fmt.Errorf("american: read results: %w", err)
		}
		if st.SearchData == nil {
			continue
		}
		return fromSearch(*st.SearchData, q, booking)
	}
	if strings.Contains(final, "ERRORCODE=ERRCODE"+noFlightsCode) {
		return []award.Offer{}, nil
	}
	for _, html := range pages {
		if isBlocked(html) {
			return nil, errBlocked
		}
	}
	return nil, errors.New("american: results page carried no search data")
}

// isBlocked reports whether html is one of Akamai's refusal pages.
func isBlocked(html string) bool {
	m := titleTag.FindStringSubmatch(html)
	if m == nil {
		return strings.Contains(html, "Access Denied")
	}
	t := strings.TrimSpace(m[1])
	return strings.Contains(t, "Access Denied") ||
		strings.Contains(t, "Challenge Validation")
}

// fromSearch turns the site's search data into the offers for q, booked at
// booking. It returns an error when the site reports one other than having
// no flights.
func fromSearch(
	d searchData,
	q award.Query,
	booking string,
) ([]award.Offer, error) {
	if d.Error != nil && d.Error.Code != "" {
		if d.Error.Code == noFlightsCode {
			return []award.Offer{}, nil
		}
		return nil, fmt.Errorf("american: site error %s: %.60s",
			d.Error.Code, d.Error.Message)
	}
	if d.ItineraryResult == nil {
		return []award.Offer{}, nil
	}
	r := d.ItineraryResult
	if len(r.Slices) == 0 && r.Error != "" {
		if r.Error == noFlightsCode {
			return []award.Offer{}, nil
		}
		return nil, fmt.Errorf("american: site error %.60s", r.Error)
	}
	out := []award.Offer{}
	for _, s := range r.Slices {
		offers, err := sliceOffers(s, q, booking)
		if err != nil {
			return nil, err
		}
		out = append(out, offers...)
	}
	return out, nil
}

// sliceOffers returns one offer per product American sells s in. It
// returns an error when a time in s cannot be read.
func sliceOffers(
	s slice,
	q award.Query,
	booking string,
) ([]award.Offer, error) {
	var out []award.Offer
	for _, p := range s.PricingDetail {
		cabin, ok := productCabin(p.ProductType)
		if !ok || !p.ProductAvailable || p.PerPassengerAwardPoints <= 0 {
			continue
		}
		segs, mixed, err := segments(s, p.ProductType, cabin)
		if err != nil {
			return nil, err
		}
		if len(segs) == 0 {
			continue
		}
		out = append(out, offer(s, p, segs, cabin, mixed, q, booking))
	}
	return out, nil
}

// offer builds the offer for product p of s, flown as segs.
func offer(
	s slice,
	p pricing,
	segs []award.Segment,
	cabin award.Cabin,
	mixed bool,
	q award.Query,
	booking string,
) award.Offer {
	pax := passengers(q)
	var airlines []string
	for _, sg := range segs {
		if !containsString(airlines, sg.OperatingCarrier) {
			airlines = append(airlines, sg.OperatingCarrier)
		}
	}
	first, last := segs[0], segs[len(segs)-1]
	duration := s.DurationInMinutes
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
		Airlines:        airlines,
		Cabin:           cabin,
		MixedCabin:      mixed,
		Fare:            fareName(p),
		Price:           price(p, pax),
		Seats:           seats(p.SeatsRemaining),
		BookingURL:      booking,
		Notes:           notes(s),
		Segments:        segs,
	}
}

// price returns what p costs for pax passengers. The site's all-passenger
// taxes are used when given, else the per-passenger taxes times pax.
func price(p pricing, pax int) award.Price {
	per := money(p.PerPassengerTaxesAndFees)
	total := money(p.AllPassengerTaxesAndFees)
	if total.Amount <= 0 {
		total = award.Money{
			Amount:   per.Amount * float64(pax),
			Currency: per.Currency,
		}
	}
	return award.Price{
		PointsPerPassenger: p.PerPassengerAwardPoints,
		PointsTotal:        p.PerPassengerAwardPoints * pax,
		TaxesPerPassenger:  per,
		TaxesTotal:         total,
	}
}

// money converts the site's amount to an award.Money, defaulting the
// currency to USD.
func money(a amount) award.Money {
	c := a.Currency
	if c == "" {
		c = "USD"
	}
	return award.Money{Amount: a.Amount, Currency: c}
}

// seats returns n as a seat count, nil when the site does not say (0).
func seats(n int) *int {
	if n <= 0 {
		return nil
	}
	return &n
}

// fareName returns the program's own name for p: its product type and,
// when given, its brand, e.g. "BUSINESS/FBUS".
func fareName(p pricing) string {
	for _, f := range p.Fares {
		if f.BrandInfo.BrandCode != "" {
			return p.ProductType + "/" + f.BrandInfo.BrandCode
		}
	}
	return p.ProductType
}

// notes returns the carriers' own names and the site's flags for s.
func notes(s slice) []string {
	var out []string
	if len(s.CarrierNames) > 0 {
		out = append(out, strings.Join(s.CarrierNames, ", "))
	}
	for _, sg := range s.Segments {
		for _, l := range sg.Legs {
			d := strings.TrimSpace(l.OperationalDisclosure)
			if d != "" && !containsString(out, "Operated by "+d) {
				out = append(out, "Operated by "+d)
			}
		}
	}
	return out
}

// segments returns the flights of s booked in product, one per leg, with
// mixed set when a leg flies below cabin. It returns an error when a time
// cannot be read.
func segments(
	s slice,
	product string,
	cabin award.Cabin,
) ([]award.Segment, bool, error) {
	var (
		out   []award.Segment
		mixed bool
	)
	for _, sg := range s.Segments {
		legs := sg.Legs
		if len(legs) == 0 {
			legs = []leg{{
				DepartureDateTime: sg.DepartureDateTime,
				ArrivalDateTime:   sg.ArrivalDateTime,
				Origin:            sg.Origin,
				Destination:       sg.Destination,
			}}
		}
		for _, l := range legs {
			seg, legMixed, err := legSegment(sg, l, product, cabin)
			if err != nil {
				return nil, false, err
			}
			mixed = mixed || legMixed
			out = append(out, seg)
		}
	}
	for i := range len(out) - 1 {
		gap := out[i+1].Departure.Sub(out[i].Arrival)
		out[i].LayoverMinutes = max(int(gap.Minutes()), 0)
	}
	return out, mixed, nil
}

// legSegment builds the segment for leg l of sg booked in product, and
// reports whether it flies below cabin. It returns an error when a time
// cannot be read.
func legSegment(
	sg segment,
	l leg,
	product string,
	cabin award.Cabin,
) (award.Segment, bool, error) {
	dep, err := parseTime(l.DepartureDateTime)
	if err != nil {
		return award.Segment{}, false, err
	}
	arr, err := parseTime(l.ArrivalDateTime)
	if err != nil {
		return award.Segment{}, false, err
	}
	carrier := sg.Flight.CarrierCode
	segCabin, code := cabin, ""
	for _, pd := range l.ProductDetails {
		if pd.ProductType != product {
			continue
		}
		code = pd.BookingCode
		if c, ok := productCabin(pd.CabinType); ok {
			segCabin = c
		}
	}
	duration := l.DurationInMinutes
	if duration <= 0 {
		duration = int(arr.Sub(dep).Minutes())
	}
	return award.Segment{
		Flight:           carrier + sg.Flight.FlightNumber,
		MarketingCarrier: carrier,
		OperatingCarrier: carrier,
		Origin:           l.Origin.Code,
		Destination:      l.Destination.Code,
		Departure:        dep,
		Arrival:          arr,
		DurationMinutes:  duration,
		Aircraft:         l.Aircraft.Name,
		Cabin:            segCabin,
		BookingCode:      code,
	}, rank(segCabin) < rank(cabin), nil
}

// parseTime reads one of the site's times, which carry their offset.
func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("american: read time %q: %w", s, err)
	}
	return t, nil
}

// productCabin maps the site's product or cabin type to a cabin. It
// returns false for a type it does not know.
func productCabin(t string) (award.Cabin, bool) {
	switch t {
	case "COACH", "MAIN", "BASIC_ECONOMY":
		return award.Economy, true
	case "PREMIUM_ECONOMY", "PREMIUM_COACH":
		return award.Premium, true
	case "BUSINESS":
		return award.Business, true
	case "FIRST":
		return award.First, true
	}
	return "", false
}

// rank orders cabins from economy (0) to first (3).
func rank(c award.Cabin) int {
	switch c {
	case award.Premium:
		return 1
	case award.Business:
		return 2
	case award.First:
		return 3
	}
	return 0
}

// containsString reports whether list holds s.
func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
