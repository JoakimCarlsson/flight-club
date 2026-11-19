package britishairways

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/joakimcarlsson/flight-club/internal/award"
	"github.com/joakimcarlsson/flight-club/internal/browser"
)

// Timings of a search: how long the autocomplete of an airport field is
// given to offer the airport, how long the results are given to appear
// after the form is sent, and how long a fare's price is given to load.
const (
	suggestWait = 2500 * time.Millisecond
	resultsWait = 100 * time.Second
	priceWait   = 15 * time.Second
)

// searchCodes are the cabins the form is sent with. The results show
// three cabins around the one asked for, so economy shows economy,
// premium economy and business, and first adds first.
var searchCodes = []string{"M", "F"}

// rawRow is one itinerary as the results page lists it.
type rawRow struct {
	// Segments are its flights, in order.
	Segments []rawSegment `json:"segments"`
	// Total is its door-to-door time, e.g. "6 h 23m".
	Total string `json:"total"`
	// OffPeak is set when the page marks the day as off-peak.
	OffPeak bool `json:"offPeak"`
	// Cabins are the cabins it can be booked in.
	Cabins []rawCabin `json:"cabins"`
}

// rawSegment is one flight of an itinerary, with local times and dates.
type rawSegment struct {
	// Flight is the marketing flight, e.g. "AA0171".
	Flight string `json:"flight"`
	// Carrier is the marketing carrier code.
	Carrier string `json:"carrier"`
	// Operating is the operating carrier code.
	Operating string `json:"operating"`
	// From is the departure airport code.
	From string `json:"from"`
	// To is the arrival airport code.
	To string `json:"to"`
	// DepTime is the local departure time, e.g. "07:00".
	DepTime string `json:"depTime"`
	// DepDate is the local departure day, e.g. "20 Jan".
	DepDate string `json:"depDate"`
	// ArrTime is the local arrival time.
	ArrTime string `json:"arrTime"`
	// ArrDate is the local arrival day.
	ArrDate string `json:"arrDate"`
}

// rawCabin is one cabin an itinerary can be booked in, with its price
// once it has been asked for.
type rawCabin struct {
	// Code is the cabin column: M, W, C or F.
	Code string `json:"code"`
	// Radio numbers the cabin's radio button on the page.
	Radio int `json:"radio"`
	// Value is the radio button's value, which names the fare.
	Value string `json:"value"`
	// Classes are the booking classes, one letter per segment.
	Classes string `json:"classes"`
	// Seats is how many seats are left, nil when not said.
	Seats *int `json:"seats"`
	// SegCabins are the cabin names of the segments, in order.
	SegCabins []string `json:"segCabins"`
	// Price is the price text, e.g. "66000 Avios + 5,737.05 SEK".
	Price string `json:"-"`
}

// searchPage runs the search for q on page, which shows the results
// form, once per cabin in searchCodes, and prices every cabin of every
// itinerary found. It returns the itineraries, none when the site has no
// award space, and an error when the form cannot be sent or the results
// do not appear.
func searchPage(
	ctx context.Context,
	page *browser.Page,
	q award.Query,
) ([]rawRow, error) {
	seen := map[string]bool{}
	var rows []rawRow
	for i, code := range searchCodes {
		if i > 0 {
			if err := backToForm(page); err != nil {
				return rows, nil
			}
		}
		got, err := searchCabin(ctx, page, q, code, seen)
		if err != nil {
			if i > 0 {
				return rows, nil
			}
			return nil, err
		}
		rows = append(rows, got...)
	}
	return rows, nil
}

// searchCabin sends the results form on page for q with cabin code, waits
// for the results, and returns their itineraries with the cabins not in
// seen, priced. It adds those cabins to seen. It returns an error when the
// form cannot be filled in or the results do not appear.
func searchCabin(
	ctx context.Context,
	page *browser.Page,
	q award.Query,
	code string,
	seen map[string]bool,
) ([]rawRow, error) {
	if err := fillForm(ctx, page, q, code); err != nil {
		return nil, err
	}
	if err := waitResults(ctx, page); err != nil {
		return nil, err
	}
	v, err := page.Evaluate(extractJS)
	if err != nil {
		return nil, fmt.Errorf("britishairways: read results: %w", err)
	}
	s, _ := v.(string)
	var rows []rawRow
	if err := json.Unmarshal([]byte(s), &rows); err != nil {
		return nil, errors.New("britishairways: unreadable results")
	}
	var out []rawRow
	for _, r := range rows {
		var cabins []rawCabin
		for _, c := range r.Cabins {
			key := c.Code + "|" + c.Value
			if seen[key] {
				continue
			}
			seen[key] = true
			c.Price = price(ctx, page, c.Radio)
			if c.Price != "" {
				cabins = append(cabins, c)
			}
		}
		if len(cabins) > 0 {
			r.Cabins = cabins
			out = append(out, r)
		}
	}
	return out, nil
}

// fillForm fills in the results form on page for a one-way search for q
// with cabin code and sends it. It returns an error when the form is not
// shown or an airport is not offered.
func fillForm(
	ctx context.Context,
	page *browser.Page,
	q award.Query,
	code string,
) error {
	acceptCookies(page)
	if _, err := waitVisible(ctx, page, "#departurePoint"); err != nil {
		return errors.New("britishairways: results form not shown")
	}
	if err := fillAirport(ctx, page, "#departurePoint", q.Origin); err != nil {
		return err
	}
	err := fillAirport(ctx, page, "#destinationPoint", q.Destination)
	if err != nil {
		return err
	}
	date, _ := json.Marshal(q.Date.Format(time.DateOnly))
	cabin, _ := json.Marshal(code)
	adults, _ := json.Marshal(strconv.Itoa(min(max(q.Passengers, 1), 9)))
	_, err = page.Evaluate(`(() => {` +
		`const w = document.querySelector('#oneWay');` +
		`if (w && !w.checked) w.click();` +
		`const d = document.querySelector('#departDatePlaceholder');` +
		`d.value = ` + string(date) + `;` +
		`d.dispatchEvent(new Event('input', {bubbles: true}));` +
		`d.dispatchEvent(new Event('change', {bubbles: true}));` +
		`document.querySelector('#cabin').value = ` + string(cabin) + `;` +
		`document.querySelector('#ad').value = ` + string(adults) + `;` +
		`window.__fcOld = true; })()`)
	if err != nil {
		return fmt.Errorf("britishairways: fill form: %w", err)
	}
	return clickVisible(ctx, page, "#submitBtn")
}

// fillAirport types airport into the airport field sel on page and picks
// the first airport the field offers. It returns an error when the field
// cannot be typed into or the airport picked is not airport.
func fillAirport(
	ctx context.Context,
	page *browser.Page,
	sel, airport string,
) error {
	if err := typeInto(page, sel, airport); err != nil {
		return err
	}
	if err := sleep(ctx, suggestWait); err != nil {
		return err
	}
	if err := page.Press(sel, "Enter"); err != nil {
		return fmt.Errorf("britishairways: airport %s: %w", airport, err)
	}
	s, _ := json.Marshal(sel)
	v, err := page.Evaluate(`document.querySelector(` + string(s) +
		`).value`)
	got, _ := v.(string)
	want := "(" + strings.ToUpper(airport) + ")"
	if err != nil || !strings.Contains(strings.ToUpper(got), want) {
		return fmt.Errorf("britishairways: airport %s not offered", airport)
	}
	return nil
}

// stateJS says what page shows after the form was sent: "results",
// "stopover" for the question about stopovers, "blocked" for a denial or
// gateway error, "error: ..." for an error on the form, or "" while the
// page is still on its way.
const stateJS = `(() => {
	if (window.__fcOld) return '';
	if (document.querySelector('#flightListOutbound0')) return 'results';
	if (document.querySelector('#stopOverForm')) return 'stopover';
	const t = document.body ? document.body.innerText : '';
	if (/Bad Gateway|Access Denied/.test(t)) return 'blocked';
	const e = [...document.querySelectorAll('[class*="rror"]')]
		.filter(x => x.offsetParent && x.innerText.trim())
		.map(x => x.innerText.trim()).join(' ');
	if (e && document.querySelector('#plan_redeem_trip'))
		return 'error: ' + e;
	return '';
})()`

// waitResults waits for page to show the results after the form was sent,
// answering the question about stopovers with no stopovers. It returns an
// error when the site denies the search, shows an error, or shows no
// results in time.
func waitResults(ctx context.Context, page *browser.Page) error {
	deadline := time.Now().Add(resultsWait)
	for time.Now().Before(deadline) {
		if err := sleep(ctx, pollDelay); err != nil {
			return err
		}
		v, _ := page.Evaluate(stateJS)
		s, _ := v.(string)
		switch {
		case s == "results":
			return nil
		case s == "blocked":
			return errBlocked
		case s == "stopover":
			_, _ = page.Evaluate(`(() => { window.__fcOld = true;` +
				`document.querySelector('#noStopovers').click();` +
				`document.querySelector('#continueTopPod').click(); })()`)
		case strings.HasPrefix(s, "error: "):
			msg := strings.TrimPrefix(s, "error: ")
			return fmt.Errorf("britishairways: %s", shorten(msg, 100))
		}
	}
	return errors.New("britishairways: results did not load")
}

// extractJS reads the itineraries of the results page as JSON, tagging
// every cabin's radio button with a number so it can be priced.
const extractJS = `(() => {
	const trs = document.querySelectorAll(
		'table[id^="flightListOutbound"] tr.journeyOptions');
	const text = e => e ? e.textContent.trim() : '';
	const rows = [];
	let radio = 0;
	for (const tr of trs) {
		const g = id => tr.querySelector('[id="' + id + '"]');
		const row = {segments: [], total: '', cabins: [],
			offPeak: /OFF-PEAK/.test(tr.textContent)};
		const flights = [...tr.querySelectorAll('a[href*="eId=100023"]')]
			.map(a => (a.getAttribute('title') || '').trim().split(/\s+/))
			.map(w => w.slice(-2).join(''));
		const deps = tr.querySelectorAll('[id^="dtlDepartureTime"]');
		deps.forEach((d, i) => {
			const k = d.id.slice('dtlDepartureTime'.length);
			const val = id => g(id + k) ? g(id + k).value : '';
			row.segments.push({
				flight: flights[i] || '',
				carrier: val('hdnCarrierCodeSB'),
				operating: val('hdnOperatingCarrierCodeSB'),
				from: text(g('deptAirportCodeLink' + k)),
				to: text(g('arrivalAirportCodeLink' + k)),
				depTime: text(d),
				depDate: text(g('dtlDepartureDate' + k)),
				arrTime: text(g('dtlArrivalTime' + k)),
				arrDate: text(g('dtlArrivalDate' + k)),
			});
		});
		row.total = [...tr.querySelectorAll('p')].map(text)
			.filter(t => /^\d+\s*h\s*\d+\s*m/.test(t)).pop() || '';
		for (const r of tr.querySelectorAll('input.cabinSelector')) {
			const m = r.id.match(/CabinCode(\w+)$/);
			if (!m) continue;
			const box = r.closest('.outerDivRadioCls');
			const sfx = box ? box.id.replace('DtlOuterDivRadio', '') : '';
			const cls = g('hdnSlngClsSB' + sfx);
			const cell = r.closest('.cabin-data');
			const left = cell && cell.querySelector('.tertiaryLink');
			const n = left ? ((left.getAttribute('aria-label') || '') +
				' ' + left.textContent).match(/(\d+)/) : null;
			const pre = 'cbnName' + sfx + '-';
			radio++;
			r.setAttribute('data-fc-radio', String(radio));
			row.cabins.push({code: m[1], radio: radio, value: r.value,
				classes: cls ? cls.value : '', seats: n ? +n[1] : null,
				segCabins: [...tr.querySelectorAll('[id^="' + pre + '"]')]
					.map(text)});
		}
		rows.push(row);
	}
	return JSON.stringify(rows);
})()`

// price selects the cabin whose radio button is numbered radio on page
// and returns the total price the page then shows, e.g. "66000 Avios +
// 5,737.05 SEK", once two reads in a row agree. It returns "" when no
// price shows in time.
func price(ctx context.Context, page *browser.Page, radio int) string {
	n := strconv.Itoa(radio)
	v, err := page.Evaluate(`(() => {` +
		`document.querySelectorAll('#priceDetails .totalPriceAviosTxt')` +
		`.forEach(e => e.setAttribute('data-fc-old', '1'));` +
		`const r = document.querySelector('[data-fc-radio="` + n + `"]');` +
		`if (!r) return false; r.click(); return true; })()`)
	if err != nil || v != true {
		return ""
	}
	last := ""
	deadline := time.Now().Add(priceWait)
	for time.Now().Before(deadline) {
		if sleep(ctx, 500*time.Millisecond) != nil {
			return ""
		}
		v, _ := page.Evaluate(`(() => { const e = document.querySelector(` +
			`'#priceDetails .totalPriceAviosTxt:not([data-fc-old])');` +
			`return e ? e.textContent.trim() : ''; })()`)
		s, _ := v.(string)
		if s != "" && s == last {
			return s
		}
		last = s
	}
	return last
}
