// Package airchina searches Air China award availability, priced in
// PhoenixMiles (凤凰知音) miles.
//
// The PhoenixMiles site ffp.airchina.com.cn shows award seats on Air China
// flights without a login: its results page posts a one-way search to a
// public JSON endpoint, which answers with every flight that has award
// space, the cabins and seats left on each, and the miles each flight
// costs. The site does not quote taxes before checkout, so offers carry
// zero taxes in CNY and a note saying so.
//
// The endpoint only takes airport codes, so city codes such as BJS are
// searched as every airport of the city.
package airchina

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/joakimcarlsson/flight-club/internal/award"
)

// Key is the program key Air China offers carry.
const Key = "airchina"

// siteURL is the PhoenixMiles site the endpoint lives on.
const siteURL = "https://ffp.airchina.com.cn"

// searchURL is the public one-way award search endpoint.
const searchURL = siteURL +
	"/gateway/air-trade/api/public/air/purchase/direct/get/route"

// bookingURL is the award ticket search page a traveller books from.
const bookingURL = siteURL + "/travel.html"

// refererURL is the results page the endpoint expects requests from.
const refererURL = siteURL + "/ticket-reward.html"

// userAgent is a current desktop Chrome, which the site answers like a
// browser.
const userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36"

// carrier is the airline searched; the endpoint sells Air China flights.
const carrier = "CA"

// taxCurrency is the currency taxes are paid in on the site.
const taxCurrency = "CNY"

// maxBody is the most of an answer that is read.
const maxBody = 8 << 20

// The status codes the endpoint answers with.
const (
	statusOK        = 1
	statusNoFlights = 90106
)

// seatsMany is the seat count the site gives for nine seats or more.
const seatsMany = "A"

// cityAirports are the airports searched for a city code the endpoint
// does not take. Codes not listed are passed through as they are.
var cityAirports = map[string][]string{
	"BJS": {"PEK", "PKX"},
	"SHA": {"SHA", "PVG"},
	"CTU": {"CTU", "TFU"},
	"TYO": {"HND", "NRT"},
	"OSA": {"KIX", "ITM"},
	"SEL": {"ICN", "GMP"},
	"LON": {"LHR", "LGW"},
	"PAR": {"CDG", "ORY"},
	"MIL": {"MXP", "LIN"},
	"ROM": {"FCO"},
	"STO": {"ARN"},
	"NYC": {"JFK", "EWR"},
	"WAS": {"IAD"},
	"CHI": {"ORD"},
	"YTO": {"YYZ"},
	"BUE": {"EZE"},
	"SAO": {"GRU"},
	"MOW": {"SVO", "DME"},
	"BKK": {"BKK", "DMK"},
	"JKT": {"CGK"},
}

// Program searches ffp.airchina.com.cn.
type Program struct {
	client *http.Client
}

// New builds a Program that makes its requests with client.
func New(client *http.Client) *Program {
	return &Program{client: client}
}

// Key returns the program key, "airchina".
func (p *Program) Key() string {
	return Key
}

// Search returns Air China's award offers for q, searching every airport
// pair a city code expands to. It returns an error when the site cannot be
// reached, answers with a status other than 200, or rejects a search.
func (p *Program) Search(
	ctx context.Context,
	q award.Query,
) ([]award.Offer, error) {
	var out []award.Offer
	seen := map[string]bool{}
	for _, org := range airports(q.Origin) {
		for _, dst := range airports(q.Destination) {
			res, err := p.searchPair(ctx, org, dst, q)
			if err != nil {
				return nil, err
			}
			for _, o := range offers(res, q.Passengers) {
				id := offerID(o)
				if seen[id] {
					continue
				}
				seen[id] = true
				out = append(out, o)
			}
		}
	}
	if out == nil {
		out = []award.Offer{}
	}
	return out, nil
}

// airports returns the airport codes to search for code: the airports of
// a city code, or code itself.
func airports(code string) []string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if list, ok := cityAirports[code]; ok {
		return list
	}
	return []string{code}
}

// offerID identifies an offer by its flight, departure and cabin, so the
// same flight found under two airport pairs is kept once.
func offerID(o award.Offer) string {
	flight := ""
	if len(o.Segments) > 0 {
		flight = o.Segments[0].Flight
	}
	return flight + "|" + o.Departure.String() + "|" + string(o.Cabin)
}

// searchPair posts one one-way search from org to dst on q's date. It
// returns nil and no error when the site has no flights for the pair.
func (p *Program) searchPair(
	ctx context.Context,
	org, dst string,
	q award.Query,
) (*resultBody, error) {
	payload, err := json.Marshal(newRequest(org, dst, q))
	if err != nil {
		return nil, fmt.Errorf("airchina: encoding request: %w", err)
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		searchURL,
		bytes.NewReader(payload),
	)
	if err != nil {
		return nil, fmt.Errorf("airchina: building request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/javascript, */*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Origin", siteURL)
	req.Header.Set("Referer", refererURL)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("airchina: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("airchina: answered %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("airchina: reading answer: %w", err)
	}
	return parseResponse(body, org, dst)
}

// parseResponse reads an answer of the endpoint for a search from org to
// dst. It returns nil and no error when the site has no flights, and an
// error when the answer is not JSON or the site rejected the search.
func parseResponse(body []byte, org, dst string) (*resultBody, error) {
	var res response
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, errors.New("airchina: answer is not JSON, likely blocked")
	}
	switch res.Status {
	case statusOK:
		return res.Body, nil
	case statusNoFlights:
		return nil, nil
	}
	msg := strings.TrimSpace(res.Message)
	if msg == "" {
		msg = "no reason given"
	}
	if r := []rune(msg); len(r) > 60 {
		msg = string(r[:60])
	}
	return nil, fmt.Errorf(
		"airchina: %s-%s rejected (status %d): %s",
		org, dst, res.Status, msg,
	)
}

// newRequest is the endpoint's request for a one-way search from org to
// dst on q's date.
func newRequest(org, dst string, q award.Query) request {
	adults := max(q.Passengers, 1)
	return request{
		ReqID: strconv.FormatInt(time.Now().UnixMilli(), 10),
		Body: requestBody{
			RouteType:  "OW",
			AdultCount: strconv.Itoa(adults),
			ChildCount: "0",
			Type:       "spe",
			Segments: []requestSegment{{
				OrgAirport: org,
				DstAirport: dst,
				DepDate:    q.Date.Format(time.DateOnly),
				Airline:    carrier,
			}},
		},
	}
}

// request is the endpoint's request envelope.
type request struct {
	// OrgID is always null.
	OrgID *string `json:"orgId"`
	// ReqID is a client request id, the time in milliseconds.
	ReqID string `json:"reqId"`
	// ReqTime is always null.
	ReqTime *string `json:"reqTime"`
	// Body is the search.
	Body requestBody `json:"body"`
}

// requestBody is the search a request carries.
type requestBody struct {
	// RouteType is "OW" for one way.
	RouteType string `json:"routeType"`
	// AdultCount is how many adults travel.
	AdultCount string `json:"adultCount"`
	// ChildCount is how many children travel.
	ChildCount string `json:"childCount"`
	// PsgStyle is a passenger type, empty for a plain adult.
	PsgStyle string `json:"psgStyle"`
	// DxToken is a risk token the page may send, empty without one.
	DxToken string `json:"dxToken"`
	// TxnID is a risk transaction id the page may send, empty without one.
	TxnID string `json:"txnId"`
	// Type is "spe", the award search.
	Type string `json:"type"`
	// Segments are the legs searched, one for one way.
	Segments []requestSegment `json:"segments"`
}

// requestSegment is one leg of a search.
type requestSegment struct {
	// OrgAirport is the airport code it departs from.
	OrgAirport string `json:"orgAirport"`
	// DstAirport is the airport code it arrives at.
	DstAirport string `json:"dstAirport"`
	// DepDate is the departure date, YYYY-MM-DD.
	DepDate string `json:"depDate"`
	// Airline is the carrier searched.
	Airline string `json:"airline"`
	// Cabin narrows the cabin, empty for every cabin.
	Cabin string `json:"cabin"`
	// Sequence is the leg's index.
	Sequence int `json:"sequence"`
}

// response is the endpoint's answer envelope.
type response struct {
	// Status is 1 on success and an error code otherwise.
	Status int `json:"status"`
	// Message says why a search failed.
	Message string `json:"message"`
	// Body is the result, null when the search failed.
	Body *resultBody `json:"body"`
}

// resultBody is the result of a search.
type resultBody struct {
	// Segment holds the flights found.
	Segment struct {
		// Flights are the flights with award space.
		Flights []flight `json:"flights"`
	} `json:"segment"`
	// MileageFares are the miles per adult keyed by carrier and flight
	// number, with and without the booking class appended.
	MileageFares map[string]int `json:"mileageFares"`
}
