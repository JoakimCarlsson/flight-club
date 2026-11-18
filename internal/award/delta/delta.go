// Package delta searches Delta Air Lines award availability, priced in
// SkyMiles. SkyMiles awards are dynamically priced and sold in Delta's fare
// columns (Basic, Main, Comfort, First, Premium Select, Delta One), with
// partner fares filed in the column of their cabin.
//
// The award search is the site's own GraphQL POST to
// offer-api-prd.delta.com. Logged out, delta.com sends it with
// "Authorization: GUEST", but the API sits behind Akamai Bot Manager: a
// plain request without the cookies the site's sensor script earns gets a
// 429 bot challenge or a 444 "Access Denied". The search is therefore run
// the way the site runs it: the stealth browser opens the home page, stores
// the search there the way the site's search form does (the public deep
// link lands on the flexible dates grid, which has no flights), opens the
// award results page for it, and the GraphQL answer is captured as the page
// loads it. Delta limits
// searches hard (a handful back to back get a 429 or 444), so the program
// should be paced to one search at a time with several seconds between.
//
// The answer gives local times without offsets, so offsets come from a
// table of airports, and an airport missing from it gets its offset from
// the trip's total time or the segment before it.
package delta

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/joakimcarlsson/flight-club/internal/award"
	"github.com/joakimcarlsson/flight-club/internal/browser"
)

// Key is the program key Delta offers carry.
const Key = "delta"

// resultsURL is the flight search page a traveller searches and books from.
const resultsURL = "https://www.delta.com/flight-search/search"

// searchResultsURL is the results page the site's own search form opens,
// followed by the key under which the form stored the search.
const searchResultsURL = "https://www.delta.com/flightsearch/search-results" +
	"?cacheKeySuffix="

// offersPath is the part of the GraphQL API's URL that identifies its
// answers among the responses the results page loads.
const offersPath = "/rm-offer-gql"

// homeURL is the site's home page, opened before the results page so the
// bot protection's sensor script earns its cookies on an ordinary visit.
const homeURL = "https://www.delta.com/"

// loadTimeout bounds the navigation to the results page.
const loadTimeout = 60 * time.Second

// settleRounds and settleDelay are how long the results page is given, after
// it loads, to call the offer API: settleRounds waits of settleDelay each.
const (
	settleRounds = 5
	settleDelay  = 4 * time.Second
)

// Program searches delta.com through a stealth browser.
type Program struct {
	browser *browser.Browser
}

// New builds a Program that loads delta.com in b.
func New(b *browser.Browser) *Program {
	return &Program{browser: b}
}

// Key returns the program key, "delta".
func (p *Program) Key() string {
	return Key
}

// errBlocked is returned when Akamai answers the offer API with its "Access
// Denied" page, or the results page never calls the API because it was
// denied itself.
var errBlocked = errors.New(
	"delta: search blocked by the site's bot protection (Access Denied)",
)

// errLimited is returned when the offer API answers with its rate-limit
// challenge rather than offers.
var errLimited = errors.New(
	"delta: the site is limiting searches; wait a minute and try again",
)

// Search returns Delta's award offers for q. It returns an error when the
// browser cannot load the results page, the site's bot protection or rate
// limit stops the search, or the offer API answers with something that
// cannot be read. A route Delta does not sell gives no offers and no error.
func (p *Program) Search(
	ctx context.Context,
	q award.Query,
) ([]award.Offer, error) {
	booking := bookingURL(q)
	key, err := cacheKey()
	if err != nil {
		return nil, fmt.Errorf("delta: %w", err)
	}
	prepare, err := prepareScript(q, key)
	if err != nil {
		return nil, fmt.Errorf("delta: %w", err)
	}
	captured, err := p.browser.Collect(ctx, searchResultsURL+key, browser.CollectOpts{
		Timeout:     loadTimeout,
		WarmUp:      homeURL,
		Prepare:     prepare,
		Scrolls:     settleRounds,
		ScrollDelay: settleDelay,
		Accept:      func(string, string) bool { return false },
		CaptureBody: func(u, _ string) bool {
			return strings.Contains(u, offersPath)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("delta: %w", err)
	}
	bodies := make([][]byte, 0, len(captured.Bodies))
	for _, b := range captured.Bodies {
		bodies = append(bodies, b.Data)
	}
	return fromBodies(bodies, captured.HTML, q, booking)
}

// fromBodies turns the offer API answers the results page loaded into the
// offers for q, booked at booking. html is the page's markup, read to tell
// a denied page from a route without flights. It returns errBlocked or
// errLimited when the site refused the search, and an error when no answer
// can be read.
func fromBodies(
	bodies [][]byte,
	html string,
	q award.Query,
	booking string,
) ([]award.Offer, error) {
	var (
		out     = []award.Offer{}
		parsed  bool
		blocked bool
		limited bool
		lastErr error
	)
	for _, body := range bodies {
		trimmed := bytes.TrimSpace(body)
		switch {
		case len(trimmed) == 0:
			continue
		case denied(trimmed):
			blocked = true
			continue
		case bytes.Contains(trimmed, []byte("cpr_chlge")):
			limited = true
			continue
		}
		a, err := parseAnswer(trimmed)
		if err != nil {
			lastErr = err
			continue
		}
		if !a.searchesOffers() {
			continue
		}
		parsed = true
		out = append(out, offers(a, q, booking)...)
	}
	switch {
	case parsed:
		return dedupe(out), nil
	case limited:
		return nil, errLimited
	case blocked, strings.Contains(html, "Access Denied"):
		return nil, errBlocked
	case lastErr != nil:
		return nil, lastErr
	}
	return out, nil
}

// denied reports whether body is Akamai's "Access Denied" page.
func denied(body []byte) bool {
	return bytes.HasPrefix(body, []byte("<")) &&
		bytes.Contains(body, []byte("Access Denied"))
}

// dedupe drops offers repeated across answers, keeping the first of each
// itinerary and fare.
func dedupe(in []award.Offer) []award.Offer {
	seen := make(map[string]bool, len(in))
	out := make([]award.Offer, 0, len(in))
	for _, o := range in {
		k := o.Fare + "|" + o.Departure.String()
		for _, s := range o.Segments {
			k += "|" + s.Flight
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, o)
	}
	return out
}

// bookingURL is the award search results page for a one-way search for q,
// as the site's own search form builds it. The search is for one passenger,
// since prices are per passenger and seats are counted separately.
func bookingURL(q award.Query) string {
	v := url.Values{}
	v.Set("action", "findFlights")
	v.Set("tripType", "ONE_WAY")
	v.Set("priceSchedule", "price")
	v.Set("originCity", q.Origin)
	v.Set("destinationCity", q.Destination)
	v.Set("departureDate", q.Date.Format("01/02/2006"))
	v.Set("departureTime", "AT")
	v.Set("returnDate", "")
	v.Set("returnTime", "AT")
	v.Set("paxCount", "1")
	v.Set("searchByCabin", "true")
	v.Set("cabinFareClass", "BE")
	v.Set("deltaOnlySearch", "false")
	v.Set("deltaOnly", "off")
	v.Set("Go", "Find Flights")
	v.Set("meetingEventCode", "")
	v.Set("refundableFlightsOnly", "false")
	v.Set("compareAirport", "false")
	v.Set("awardTravel", "true")
	v.Set("shopWithMiles", "on")
	return resultsURL + "?" + v.Encode()
}

// cacheKey returns a random UUID, the key the site's search form stores a
// search under. It returns an error when no randomness can be read.
func cacheKey() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10],
		b[10:]), nil
}

// searchData is the exact-date search for q the way the site's search form
// stores it before opening the results page: a one-way award search for
// one passenger, priced in miles, without flexible dates or airports.
func searchData(q award.Query, key string) map[string]any {
	radius := map[string]any{"measure": 100, "unit": "MI"}
	return map[string]any{
		"action":                     "findFlights",
		"actionType":                 "",
		"awardTravel":                "true",
		"bestFare":                   "BE",
		"bookingPostVerify":          "RTR_YES",
		"cabinFareClass":             nil,
		"cacheKey":                   key,
		"currentSolution":            map[string]any{"sliceIndex": 0, "solutionId": ""},
		"datesFlexible":              false,
		"deltaOnlySearch":            "false",
		"destinationAirportRadius":   radius,
		"flexAirport":                false,
		"flexCalendar":               false,
		"flexDate":                   false,
		"flexDaysWeeks":              "",
		"isEdocsSearch":              false,
		"includeBasicFaresUnchecked": false,
		"initialSearchBy": map[string]any{
			"fareFamily":       "BE",
			"cabinFareClass":   nil,
			"meetingEventCode": "",
			"refundable":       false,
			"flexAirport":      false,
			"flexDate":         false,
			"flexDaysWeeks":    "",
			"deepLinkVendorId": "",
		},
		"is_Flex_Search":        true,
		"meetingEventCode":      "",
		"nonstopFlightsOnly":    "false",
		"originAirportRadius":   radius,
		"passengerInfo":         []any{},
		"passengers":            []any{map[string]any{"count": "1", "type": "ADT"}},
		"priceSchedule":         "",
		"priceType":             "Award",
		"refundableFlightsOnly": false,
		"schedulePrice":         "price",
		"searchByCabin":         true,
		"searchByFareClass":     nil,
		"searchType":            "simple",
		"segments": []any{map[string]any{
			"origin":                 q.Origin,
			"destination":            q.Destination,
			"originCountryCode":      "",
			"destinationCountryCode": "",
			"departureDate":          q.Date.Format(time.DateOnly),
			"originCity":             "",
			"destinationCity":        "",
		}},
		"shopType":       "MILES",
		"shopWithMiles":  "",
		"tripType":       "ONE_WAY",
		"upgradeRequest": false,
	}
}

// prepareScript is the script run on the home page before the results
// page opens: it stores the search for q under key and sets the trip type
// cookie, as the site's search form does. It returns an error when the
// search cannot be encoded.
func prepareScript(q award.Query, key string) (string, error) {
	data, err := json.Marshal(searchData(q, key))
	if err != nil {
		return "", err
	}
	stored, err := json.Marshal(string(data))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`(() => {
Object.keys(localStorage).filter(k => k.startsWith("postData")).forEach(k => localStorage.removeItem(k));
localStorage.setItem("postData%s", %s);
document.cookie = "trip_type=ONE_WAY; path=/; max-age=604800";
document.cookie = "mc_cache_key=; path=/; max-age=0";
return true;
})()`, key, stored), nil
}
