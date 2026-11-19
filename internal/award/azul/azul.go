// Package azul searches Azul Fidelidade (formerly TudoAzul) award
// availability, priced in Azul points. Azul sells every seat for points,
// so each flight has a points fare in each cabin Azul flies it in:
// "Azul" or "Economy" fares map to Economy and "Business" to Business.
//
// The search needs no login: logged out, voeazul.com.br's flight selection
// page, opened with the points currency, fetches its fares from the
// TudoAzul availability API with an anonymous token. That API sits behind
// bot protection, so the search is run the way the site runs it: the
// stealth browser opens the home page, then the points results page for
// the route and day, and the availability API's JSON answer is captured
// as the page loads it.
package azul

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/joakimcarlsson/flight-club/internal/award"
	"github.com/joakimcarlsson/flight-club/internal/browser"
)

// Key is the program key Azul offers carry.
const Key = "azul"

// resultsURL is the flight selection page a traveller searches and books
// from.
const resultsURL = "https://www.voeazul.com.br/br/pt/home/selecao-voo"

// homeURL is the site's home page, opened before the results page so the
// bot protection's scripts earn their cookies on an ordinary visit.
const homeURL = "https://www.voeazul.com.br/br/pt/home"

// availabilityPath identifies the points availability API's answer among
// the responses the results page loads.
const availabilityPath = "/tudoazul/reservation/availability/"

// loadTimeout bounds each navigation.
const loadTimeout = 60 * time.Second

// settleRounds and settleDelay are how long the results page is given,
// after it loads, to call the availability API.
const (
	settleRounds = 5
	settleDelay  = 3 * time.Second
)

// attempts is how many times a search is tried when the page never
// fetches fares.
const attempts = 2

// retryDelay is how long a refused search waits before it is tried again.
const retryDelay = 8 * time.Second

// errNoAnswer is returned when the results page never called the
// availability API, most often because the bot protection refused it.
var errNoAnswer = errors.New(
	"azul: the results page returned no fares (blocked or timed out)",
)

// Program searches voeazul.com.br through a stealth browser.
type Program struct {
	browser *browser.Browser
}

// New builds a Program that loads voeazul.com.br in b.
func New(b *browser.Browser) *Program {
	return &Program{browser: b}
}

// Key returns the program key, "azul".
func (p *Program) Key() string {
	return Key
}

// Search returns Azul's points offers for q. City codes are passed to the
// site, which searches every airport of the city. It returns an error when
// the browser cannot load the results page, the page never fetches fares,
// or the availability API answers with something that cannot be read. A
// route Azul does not fly gives no offers and no error.
func (p *Program) Search(
	ctx context.Context,
	q award.Query,
) ([]award.Offer, error) {
	var err error
	for i := range attempts {
		if i > 0 && !pause(ctx, retryDelay) {
			return nil, err
		}
		var out []award.Offer
		out, err = p.attempt(ctx, q)
		if err == nil || ctx.Err() != nil {
			return out, err
		}
	}
	return nil, err
}

// pause waits d, reporting false when ctx ends first.
func pause(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// attempt loads the results page for q once and reads its offers. It
// fails as Search does.
func (p *Program) attempt(
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
			return strings.Contains(u, availabilityPath)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("azul: %w", err)
	}
	var lastErr error = errNoAnswer
	var empty []award.Offer
	for _, b := range captured.Bodies {
		out, err := parse(b.Data, q, booking)
		switch {
		case err != nil:
			lastErr = err
		case len(out) > 0:
			return out, nil
		default:
			empty = out
		}
	}
	if empty != nil {
		return empty, nil
	}
	return nil, lastErr
}

// bookingURL is the one-way points results page for q, as the site's own
// search form builds it, for every passenger of q.
func bookingURL(q award.Query) string {
	n := strconv.Itoa(max(q.Passengers, 1))
	v := []string{
		"c[0].ds=" + url.QueryEscape(strings.ToUpper(q.Origin)),
		"c[0].std=" + q.Date.Format("01/02/2006"),
		"c[0].as=" + url.QueryEscape(strings.ToUpper(q.Destination)),
		"p[0].t=ADT",
		"p[0].c=" + n,
		"p[0].cp=false",
		"f.dl=3",
		"f.dr=3",
		"cc=PTS",
	}
	return resultsURL + "?" + strings.Join(v, "&")
}
