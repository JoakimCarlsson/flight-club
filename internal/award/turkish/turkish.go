// Package turkish searches Turkish Airlines Miles&Smiles award
// availability, priced in Miles&Smiles miles.
//
// The search needs no login: the site's settings switch on award booking
// without one. turkishairlines.com sits behind Akamai and PerimeterX, so
// the availability API is called from inside the site: the stealth
// browser opens the award booking page, which earns the bot protection's
// cookies, and a script left there sends the search as a keepalive fetch
// as the page is left. The browser then opens robots.txt, a cheap page on
// the same site, and captures the availability answer as it arrives.
package turkish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/joakimcarlsson/flight-club/internal/award"
	"github.com/joakimcarlsson/flight-club/internal/browser"
)

// Key is the program key Turkish Airlines offers carry.
const Key = "turkish"

// BookingURL is the Miles&Smiles award booking page, where a traveller
// searches and books an award.
const BookingURL = "https://www.turkishairlines.com/en-int/" +
	"miles-and-smiles/book-award-tickets/"

// landingURL is the same-site page opened after the booking page, so the
// keepalive search is sent while the browser goes on listening.
const landingURL = "https://www.turkishairlines.com/robots.txt"

// apiPath is the path of the availability API, the end of its URL.
const apiPath = "/api/v1/availability"

// taxNote is the note every offer carries, as the answer has no taxes.
const taxNote = "Taxes and fees are quoted when the award is booked."

// Timings of a search: loadTimeout bounds each navigation, warmUpDelay is
// how long the booking page runs its scripts before the search is sent,
// and the answer is awaited for settleRounds waits of settleDelay each.
const (
	loadTimeout  = 60 * time.Second
	warmUpDelay  = 8 * time.Second
	settleRounds = 6
	settleDelay  = 2 * time.Second
)

// cityCodes are the metropolitan codes that stand for several airports,
// searched with the multi-port flag set.
var cityCodes = map[string]bool{
	"NYC": true, "LON": true, "PAR": true, "MIL": true, "ROM": true,
	"WAS": true, "CHI": true, "TYO": true, "OSA": true, "SEL": true,
	"MOW": true, "STO": true, "BUE": true, "SAO": true, "RIO": true,
	"YTO": true, "YMQ": true, "BJS": true, "SHA": true, "JKT": true,
	"DTT": true, "HOU": true, "QDF": true, "BUH": true,
}

// Program searches turkishairlines.com through a stealth browser.
type Program struct {
	browser *browser.Browser
}

// New builds a Program that loads turkishairlines.com in b.
func New(b *browser.Browser) *Program {
	return &Program{browser: b}
}

// Key returns the program key, "turkish".
func (p *Program) Key() string {
	return Key
}

// errBlocked is returned when the site's bot protection denies the pages.
var errBlocked = errors.New(
	"turkish: search blocked by the site's bot protection",
)

// errNoAnswer is returned when no availability answer arrives.
var errNoAnswer = errors.New(
	"turkish: the availability search did not answer",
)

// Search returns Miles&Smiles award offers for q. It returns an error when
// the browser cannot load the site, the bot protection stops the search,
// or the availability API answers with an error or something that cannot
// be read. A day with no flights gives no offers and no error.
func (p *Program) Search(
	ctx context.Context,
	q award.Query,
) ([]award.Offer, error) {
	script, err := prepareScript(q)
	if err != nil {
		return nil, err
	}
	captured, err := p.browser.Collect(ctx, landingURL, browser.CollectOpts{
		Timeout:     loadTimeout,
		WarmUp:      BookingURL,
		WarmUpDelay: warmUpDelay,
		Prepare:     script,
		Scrolls:     settleRounds,
		ScrollDelay: settleDelay,
		Accept:      func(string, string) bool { return false },
		CaptureBody: isAnswer,
	})
	if err != nil {
		return nil, fmt.Errorf("turkish: %w", err)
	}
	if len(captured.Bodies) == 0 {
		html := strings.ToLower(captured.HTML)
		if strings.Contains(html, "access denied") ||
			strings.Contains(html, "px-captcha") {
			return nil, errBlocked
		}
		return nil, errNoAnswer
	}
	var lastErr error
	for _, b := range captured.Bodies {
		offers, err := parse(b.Data, q)
		if err == nil {
			return offers, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// isAnswer reports whether a response at rawURL is the availability API's.
func isAnswer(rawURL, _ string) bool {
	path, _, _ := strings.Cut(rawURL, "?")
	return strings.HasSuffix(path, apiPath)
}

// request is the availability API's search body.
type request struct {
	SelectedBookerSearch string          `json:"selectedBookerSearch"`
	SelectedCabinClass   string          `json:"selectedCabinClass"`
	ModuleType           string          `json:"moduleType"`
	PassengerTypeList    []passengerType `json:"passengerTypeList"`

	ODList []odRequest `json:"originDestinationInformationList"`
}

// passengerType is how many passengers of one type travel.
type passengerType struct {
	Quantity int    `json:"quantity"`
	Code     string `json:"code"`
}

// odRequest is one direction of a search.
type odRequest struct {
	OriginAirportCode      string `json:"originAirportCode"`
	OriginMultiPort        bool   `json:"originMultiPort"`
	DestinationAirportCode string `json:"destinationAirportCode"`
	DestinationMultiPort   bool   `json:"destinationMultiPort"`
	DepartureDate          string `json:"departureDate"`
}

// requestBody returns the JSON search body for q, a one-way economy award
// search, whose answer prices every cabin. It returns an error only when
// the body cannot be encoded.
func requestBody(q award.Query) ([]byte, error) {
	return json.Marshal(request{
		SelectedBookerSearch: "O",
		SelectedCabinClass:   "ECONOMY",
		ModuleType:           "AWARD",
		PassengerTypeList: []passengerType{
			{Quantity: max(q.Passengers, 1), Code: "ADULT"},
		},
		ODList: []odRequest{{
			OriginAirportCode:      q.Origin,
			OriginMultiPort:        cityCodes[q.Origin],
			DestinationAirportCode: q.Destination,
			DestinationMultiPort:   cityCodes[q.Destination],
			DepartureDate:          q.Date.Format("02-01-2006"),
		}},
	})
}

// prepareScript returns the JavaScript left on the booking page that
// sends the search for q as a keepalive fetch when the page is left. It
// returns an error when the search body cannot be encoded.
func prepareScript(q award.Query) (string, error) {
	body, err := requestBody(q)
	if err != nil {
		return "", fmt.Errorf("turkish: encode search: %w", err)
	}
	literal, err := json.Marshal(string(body))
	if err != nil {
		return "", fmt.Errorf("turkish: encode search: %w", err)
	}
	return `(() => {
  const id = () => crypto.randomUUID();
  const headers = {
    "Accept": "application/json",
    "Content-Type": "application/json",
    "Accept-Language": "en",
    "X-country": "int",
    "X-platform": "WEB",
    "X-clientId": id(),
    "X-conversationId": id(),
    "X-requestId": id(),
  };
  let sent = false;
  const send = () => {
    if (sent) { return; }
    sent = true;
    fetch("` + apiPath + `", {
      method: "POST",
      headers,
      body: ` + string(literal) + `,
      credentials: "include",
      keepalive: true,
    });
  };
  window.addEventListener("pagehide", send);
  window.addEventListener("beforeunload", send);
  return true;
})()`, nil
}
