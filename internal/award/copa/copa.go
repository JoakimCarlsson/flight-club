// Package copa searches Copa Airlines award availability, priced in
// ConnectMiles. Copa sells awards on its own flights and on Star Alliance
// partners' in four fare families: Economy Saver, Economy Standard,
// Business Saver and Business Standard.
//
// The award search works logged out: shopping.copaair.com, opened with
// isMiles=true, asks api.copaair.com/ibe/booking/plan-redemptions for the
// day's itineraries. That API wants a nonce the page earns from
// /ibe/booking/get-id, and the site sits behind DataDome, so the search is
// run the way the site runs it: the stealth browser opens the home page,
// then the results page, and the plan-redemptions answer is captured as
// the page loads it.
//
// The answer gives local times without offsets, so offsets come from a
// table of the airports Copa and its partners fly in the Americas, and an
// airport missing from it gets its offset from the trip's journey time or
// the airport before it. The answer does not say how many seats are left.
package copa

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/joakimcarlsson/flight-club/internal/award"
	"github.com/joakimcarlsson/flight-club/internal/browser"
)

// Key is the program key Copa offers carry.
const Key = "copa"

// shoppingURL is the results page a traveller searches and books from.
const shoppingURL = "https://shopping.copaair.com/"

// homeURL is the site's home page, opened before the results page so the
// bot protection's script earns its cookies on an ordinary visit.
const homeURL = "https://www.copaair.com/en-us/"

// redemptionsPath is the part of the award API's URL that identifies its
// answers among the responses the results page loads.
const redemptionsPath = "/ibe/booking/plan-redemptions"

// loadTimeout bounds the navigation to the results page.
const loadTimeout = 60 * time.Second

// settleRounds and settleDelay are how long the results page is given, after
// it loads, to call the award API: settleRounds waits of settleDelay each.
const (
	settleRounds = 5
	settleDelay  = 4 * time.Second
)

// errNoAnswer is returned when the results page never answered the search,
// which happens when the bot protection holds the page back.
var errNoAnswer = errors.New(
	"copa: the results page did not search; the site may be blocking",
)

// Program searches copaair.com through a stealth browser.
type Program struct {
	browser *browser.Browser
}

// New builds a Program that loads copaair.com in b.
func New(b *browser.Browser) *Program {
	return &Program{browser: b}
}

// Key returns the program key, "copa".
func (p *Program) Key() string {
	return Key
}

// Search returns Copa's award offers for q. It returns an error when the
// browser cannot load the results page, the page never runs the search, or
// the award API answers with something that cannot be read. A day without
// award space gives no offers and no error.
func (p *Program) Search(
	ctx context.Context,
	q award.Query,
) ([]award.Offer, error) {
	booking := bookingURL(q)
	captured, err := p.browser.Collect(ctx, booking, browser.CollectOpts{
		Timeout:     loadTimeout,
		WarmUp:      homeURL,
		Scrolls:     settleRounds,
		ScrollDelay: settleDelay,
		Accept:      func(string, string) bool { return false },
		CaptureBody: func(u, _ string) bool {
			return strings.Contains(u, redemptionsPath)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("copa: %w", err)
	}
	bodies := make([][]byte, 0, len(captured.Bodies))
	for _, b := range captured.Bodies {
		bodies = append(bodies, b.Data)
	}
	return fromBodies(bodies, q, booking)
}

// fromBodies turns the award API answers the results page loaded into the
// offers for q, booked at booking. It returns errNoAnswer when no answer
// was captured and an error when none can be read.
func fromBodies(
	bodies [][]byte,
	q award.Query,
	booking string,
) ([]award.Offer, error) {
	var (
		out     = []award.Offer{}
		parsed  bool
		lastErr error
	)
	for _, body := range bodies {
		trimmed := bytes.TrimSpace(body)
		if len(trimmed) == 0 {
			continue
		}
		if trimmed[0] != '[' {
			if noFlights(trimmed) || unservedRoute(trimmed) {
				parsed = true
				continue
			}
			lastErr = fmt.Errorf("copa: unexpected answer: %.200s", trimmed)
			continue
		}
		var ods []originDestination
		if err := json.Unmarshal(trimmed, &ods); err != nil {
			lastErr = fmt.Errorf("copa: read answer: %w", err)
			continue
		}
		parsed = true
		for _, od := range ods {
			out = append(out, offers(od, q, booking)...)
		}
	}
	switch {
	case parsed:
		return dedupe(out), nil
	case lastErr != nil:
		return nil, lastErr
	}
	return nil, errNoAnswer
}

// noFlights reports whether body is the API's answer for a day or route
// without award space, an error object naming no availability.
func noFlights(body []byte) bool {
	lower := bytes.ToLower(body)
	for _, s := range []string{
		"no availability", "noavailability", "no flights",
		"not available", "no_availability", "no se encontr",
	} {
		if bytes.Contains(lower, []byte(s)) {
			return true
		}
	}
	return false
}

// dedupe drops offers repeated across answers, keeping the first of each
// itinerary and fare.
func dedupe(in []award.Offer) []award.Offer {
	seen := make(map[string]bool, len(in))
	out := make([]award.Offer, 0, len(in))
	for _, o := range in {
		var b strings.Builder
		b.WriteString(o.Fare)
		for _, s := range o.Segments {
			b.WriteString("|" + s.Flight + s.Departure.String())
		}
		k := b.String()
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, o)
	}
	return out
}

// bookingURL is the results page for q, priced in miles, which a
// traveller can book from.
func bookingURL(q award.Query) string {
	v := url.Values{}
	v.Set("roundtrip", "false")
	v.Set("adults", strconv.Itoa(max(q.Passengers, 1)))
	v.Set("children", "0")
	v.Set("infants", "0")
	v.Set("date1", q.Date.Format(time.DateOnly))
	v.Set("area1", q.Origin)
	v.Set("area2", q.Destination)
	v.Set("flexible_dates_v2", "false")
	v.Set("isMiles", "true")
	v.Set("langid", "en")
	return shoppingURL + "?" + v.Encode()
}

// unservedRoute reports whether body is Copa's code 325 answer, which it
// gives for a route it does not fly, so the search counts as no flights.
func unservedRoute(body []byte) bool {
	return bytes.Contains(body, []byte(`"code":"325"`))
}
