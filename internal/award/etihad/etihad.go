// Package etihad searches Etihad Guest award availability, priced in
// Etihad Guest Miles. Etihad sells award seats in its fare families: the
// Guest award families (codes such as YGVALUE, JGCOMFORT or FGDELUXE) and
// the dearer families priced from the cash fare (YVALUE, JDELUXE).
//
// The search needs no login. etihad.com hands the search to its Amadeus
// Digital Experience booking engine at digital.etihad.com, which, given
// FLOW=AWARD, prices every fare in miles. The results page calls the
// engine's air-bounds API on api-des.etihad.com, once for economy and
// business and once for first, with a token the page earns for itself; the
// site sits behind Akamai, so the search is run the way the site runs it:
// the stealth browser opens the home page, then the results page, and the
// air-bounds answers are captured as the page loads them.
package etihad

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/joakimcarlsson/flight-club/internal/award"
	"github.com/joakimcarlsson/flight-club/internal/browser"
)

// Key is the program key Etihad offers carry.
const Key = "etihad"

// searchURL is the booking engine's search deep link, the address the
// site's own search form sends a traveller to.
const searchURL = "https://digital.etihad.com/book/search"

// homeURL is the site's home page, opened before the results page so the
// bot protection's sensor script earns its cookies on an ordinary visit.
const homeURL = "https://www.etihad.com/en-ae/"

// boundsPath is the part of the air-bounds API's URL that identifies its
// answers among the responses the results page loads.
const boundsPath = "/v2/search/air-bounds"

// loadTimeout bounds the navigation to the results page.
const loadTimeout = 60 * time.Second

// settleRounds and settleDelay are how long the results page is given,
// after it loads, to call the air-bounds API: settleRounds waits of
// settleDelay each.
const (
	settleRounds = 5
	settleDelay  = 4 * time.Second
)

// Program searches etihad.com through a stealth browser.
type Program struct {
	browser *browser.Browser
}

// New builds a Program that loads etihad.com in b.
func New(b *browser.Browser) *Program {
	return &Program{browser: b}
}

// Key returns the program key, "etihad".
func (p *Program) Key() string {
	return Key
}

// errBlocked is returned when the results page is denied by the site's bot
// protection.
var errBlocked = errors.New(
	"etihad: search blocked by the site's bot protection (Access Denied)",
)

// errNoAnswer is returned when the results page never calls the
// air-bounds API.
var errNoAnswer = errors.New(
	"etihad: the results page did not load any award availability",
)

// Search returns Etihad Guest award offers for q. It returns an error when
// the browser cannot load the results page, the site's bot protection
// stops the search, or the air-bounds API answers with something that
// cannot be read. A route Etihad does not sell gives no offers and no
// error.
func (p *Program) Search(
	ctx context.Context,
	q award.Query,
) ([]award.Offer, error) {
	link := bookingURL(q)
	captured, err := p.browser.Collect(ctx, link, browser.CollectOpts{
		Timeout:     loadTimeout,
		WarmUp:      homeURL,
		Scrolls:     settleRounds,
		ScrollDelay: settleDelay,
		Accept:      func(string, string) bool { return false },
		CaptureBody: func(u, _ string) bool {
			return strings.Contains(u, boundsPath)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("etihad: %w", err)
	}
	bodies := make([][]byte, 0, len(captured.Bodies))
	for _, b := range captured.Bodies {
		bodies = append(bodies, b.Data)
	}
	return fromBodies(bodies, captured.HTML, q, link)
}

// fromBodies turns the air-bounds answers the results page loaded into the
// offers for q, booked at link. html is the page's markup, read to tell a
// denied page from one that never searched. It returns errBlocked or
// errNoAnswer when no answer was loaded, and an error when none of the
// answers can be read.
func fromBodies(
	bodies [][]byte,
	html string,
	q award.Query,
	link string,
) ([]award.Offer, error) {
	var (
		out     = []award.Offer{}
		seen    = map[string]bool{}
		parsed  bool
		lastErr error
	)
	for _, body := range bodies {
		a, err := parseAnswer(body)
		if err != nil {
			lastErr = err
			continue
		}
		parsed = true
		for _, o := range a.offers(q, link) {
			k := o.Fare + "|" + o.Departure.String() + "|" +
				flightsOf(o)
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, o)
		}
	}
	switch {
	case parsed:
		return out, nil
	case lastErr != nil:
		return nil, lastErr
	case strings.Contains(html, "Access Denied"):
		return nil, errBlocked
	}
	return nil, errNoAnswer
}

// flightsOf joins the flight numbers of o's segments, to tell itineraries
// apart that leave at the same time.
func flightsOf(o award.Offer) string {
	parts := make([]string, 0, len(o.Segments))
	for _, s := range o.Segments {
		parts = append(parts, s.Flight)
	}
	return strings.Join(parts, "-")
}

// bookingURL returns the booking engine's award search deep link for q,
// the page a traveller searches and books from.
func bookingURL(q award.Query) string {
	travelers := make([]string, max(q.Passengers, 1))
	for i := range travelers {
		travelers[i] = "ADT"
	}
	v := url.Values{}
	v.Set("LANGUAGE", "EN")
	v.Set("CHANNEL", "DESKTOP")
	v.Set("B_LOCATION", q.Origin)
	v.Set("E_LOCATION", q.Destination)
	v.Set("TRIP_TYPE", "O")
	v.Set("CABIN", "E")
	v.Set("TRAVELERS", strings.Join(travelers, ","))
	v.Set("TRIP_FLOW_TYPE", "AVAILABILITY")
	v.Set("DATE_1", q.Date.Format("20060102")+"0000")
	v.Set("FLOW", "AWARD")
	v.Set("SITE_EDITION", "EN-AE")
	return searchURL + "?" + v.Encode()
}

// scale divides an amount given in a currency's minor units by the
// currency's decimal places, giving the major unit.
func scale(amount float64, decimals int) float64 {
	for range decimals {
		amount /= 10
	}
	return amount
}
