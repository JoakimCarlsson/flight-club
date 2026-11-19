// Package lufthansa searches Lufthansa Group award availability, priced in
// Miles & More miles.
//
// The award flight search on miles-and-more.com sends a traveller to log in
// before it shows live results, but its calendar asks the site's API gateway
// for the cheapest award of every day without a login. That answer is used
// here: one request per cabin asks for the cheapest award on the day of the
// query, and the flights it names are looked up in the answer's dictionary
// of segments. The prices come from a cache rather than a live search, so
// offers are Estimated.
package lufthansa

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/joakimcarlsson/flight-club/internal/award"
)

// Key is the program key Miles & More offers carry.
const Key = "lufthansa"

// bestByDayURL is the API gateway's cheapest-award-per-day endpoint.
const bestByDayURL = "https://api.miles-and-more.com/flights/v3/bestbyday"

// bookingURL is the award flight search a traveller books from.
const bookingURL = "https://www.miles-and-more.com/de/en/spend/flights/" +
	"flight-award.html"

// siteOrigin is the origin the API gateway allows requests from.
const siteOrigin = "https://www.miles-and-more.com"

// apiKey is the public key the site's own pages send to the API gateway.
const apiKey = "agGBZmuTGwFXWzVDg8ckGKGBytemE1nS"

// corporateCode is the award corporate code the site's search sends.
const corporateCode = 223293

// commencementCountry is the portal country the site's search sends.
const commencementCountry = "DE"

// taxCurrency is the currency taxes are asked for.
const taxCurrency = "EUR"

// userAgent is a current desktop Chrome, which the gateway answers like a
// browser.
const userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36"

// maxBody is the most of an answer that is read.
const maxBody = 8 << 20

// dateLayout is the layout of the API's dates.
const dateLayout = "2006-01-02"

// timeLayout is the layout of the API's segment times.
const timeLayout = "2006-01-02T15:04:05.000-07:00"

// rangeOfDeparture is how many days after the queried one are asked for;
// the gateway refuses 0, so the next day comes along and is dropped.
const rangeOfDeparture = 1

// cabinFamily pairs a cabin with the commercial fare family that asks for
// it.
type cabinFamily struct {
	cabin  award.Cabin
	family string
}

// cabinFamilies are the cabins searched, in the order they are asked.
var cabinFamilies = []cabinFamily{
	{award.Economy, "CFFECOINS2"},
	{award.Premium, "CFFPECOIN2"},
	{award.Business, "CFFBUSINS2"},
	{award.First, "CFFFIRSIN2"},
}

// Program searches Miles & More award prices.
type Program struct {
	client *http.Client
}

// New builds a Program that makes its requests with client.
func New(client *http.Client) *Program {
	return &Program{client: client}
}

// Key returns the program key, "lufthansa".
func (p *Program) Key() string {
	return Key
}

// Search returns the cheapest Miles & More award of each cabin on the day
// of q. A cabin the gateway fails to answer is skipped; Search returns an
// error only when every cabin fails.
func (p *Program) Search(
	ctx context.Context,
	q award.Query,
) ([]award.Offer, error) {
	var offers []award.Offer
	var errs []error
	seen := map[string]bool{}
	for _, cf := range cabinFamilies {
		ans, err := p.bestByDay(ctx, q, cf.family)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", cf.cabin, err))
			continue
		}
		for _, o := range ans.offers(q, cf.cabin) {
			id := offerID(o)
			if seen[id] {
				continue
			}
			seen[id] = true
			offers = append(offers, o)
		}
	}
	if len(errs) == len(cabinFamilies) {
		return nil, errors.Join(errs...)
	}
	return offers, nil
}

// offerID identifies an offer by its cabin, fare, day and flights, so the
// same award answered for two cabins is kept once.
func offerID(o award.Offer) string {
	parts := []string{
		string(o.Cabin),
		o.Fare,
		o.Departure.Format(time.RFC3339),
	}
	for _, s := range o.Segments {
		parts = append(parts, s.Flight)
	}
	return strings.Join(parts, "|")
}

// request is the body the bestbyday endpoint takes.
type request struct {
	CommercialFareFamilies []string    `json:"commercialFareFamilies"`
	CorporateCodes         []int       `json:"corporateCodes"`
	CountryOfCommencement  string      `json:"countryOfCommencement"`
	CurrencyCode           string      `json:"currencyCode"`
	Itineraries            []itinerary `json:"itineraries"`
	TripDetails            tripDetails `json:"tripDetails"`
}

// itinerary is one direction of a request.
type itinerary struct {
	DepartureDateTime       string `json:"departureDateTime"`
	OriginLocationCode      string `json:"originLocationCode"`
	DestinationLocationCode string `json:"destinationLocationCode"`
}

// tripDetails says how many days after the first are asked for.
type tripDetails struct {
	RangeOfDeparture int `json:"rangeOfDeparture"`
}

// bestByDay asks the gateway for the cheapest award in family on the day
// of q. It returns an error when the gateway cannot be reached, answers
// with a status other than 200, or answers with something that is not
// JSON.
func (p *Program) bestByDay(
	ctx context.Context,
	q award.Query,
	family string,
) (*answer, error) {
	body, err := json.Marshal(request{
		CommercialFareFamilies: []string{family},
		CorporateCodes:         []int{corporateCode},
		CountryOfCommencement:  commencementCountry,
		CurrencyCode:           taxCurrency,
		Itineraries: []itinerary{{
			DepartureDateTime: q.Date.Format(dateLayout) +
				"T00:00:00",
			OriginLocationCode:      strings.ToUpper(q.Origin),
			DestinationLocationCode: strings.ToUpper(q.Destination),
		}},
		TripDetails: tripDetails{RangeOfDeparture: rangeOfDeparture},
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		bestByDayURL,
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", siteOrigin)
	req.Header.Set("Referer", siteOrigin+"/")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("X-Api-Key", apiKey)
	req.Header.Set("Rtw", "true")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"bestbyday: status %d: %s",
			resp.StatusCode,
			snippet(raw),
		)
	}
	var ans answer
	if err := json.Unmarshal(raw, &ans); err != nil {
		return nil, fmt.Errorf("bestbyday: %w", err)
	}
	return &ans, nil
}

// snippet returns the start of raw, for an error message.
func snippet(raw []byte) string {
	const most = 200
	s := strings.TrimSpace(string(raw))
	if len(s) > most {
		s = s[:most]
	}
	return s
}

// answer is what the bestbyday endpoint answers.
type answer struct {
	Data         []day        `json:"data"`
	Dictionaries dictionaries `json:"dictionaries"`
}

// day is the cheapest award of one day.
type day struct {
	DepartureDate  string  `json:"departureDate"`
	Prices         prices  `json:"prices"`
	FareFamilyCode string  `json:"fareFamilyCode"`
	Bounds         []bound `json:"bounds"`
}

// prices is a price in miles and taxes for one passenger.
type prices struct {
	TotalPrices []struct {
		CurrencyCode string `json:"currencyCode"`
		TotalTaxes   int64  `json:"totalTaxes"`
	} `json:"totalPrices"`
	MilesConversion struct {
		ConvertedMiles struct {
			Base int `json:"base"`
		} `json:"convertedMiles"`
	} `json:"milesConversion"`
}

// bound is one direction of a day's award; its flights carry no detail
// beyond their count.
type bound struct {
	FareFamilyCode string            `json:"fareFamilyCode"`
	Flights        []json.RawMessage `json:"flights"`
}

// dictionaries are the details an answer's days refer to.
type dictionaries struct {
	Location map[string]struct {
		Type     string `json:"type"`
		CityCode string `json:"cityCode"`
	} `json:"location"`
	Aircraft map[string]string `json:"aircraft"`
	Flight   map[string]flight `json:"flight"`
	Currency map[string]struct {
		DecimalPlaces int `json:"decimalPlaces"`
	} `json:"currency"`
	FareFamilyWithServices map[string]struct {
		CommercialFareFamily string `json:"commercialFareFamily"`
	} `json:"fareFamilyWithServices"`
}

// flight is one segment in the dictionary.
type flight struct {
	MarketingAirlineCode  string   `json:"marketingAirlineCode"`
	OperatingAirlineCode  string   `json:"operatingAirlineCode"`
	MarketingFlightNumber string   `json:"marketingFlightNumber"`
	Departure             endpoint `json:"departure"`
	Arrival               endpoint `json:"arrival"`
	AircraftCode          string   `json:"aircraftCode"`
}

// endpoint is where and when a segment leaves or lands.
type endpoint struct {
	LocationCode string `json:"locationCode"`
	DateTime     string `json:"dateTime"`
}

// offers turns the days of a that fall on the date of q into offers, with
// cabin asked when the answer does not name the cabin of a fare.
func (a *answer) offers(q award.Query, asked award.Cabin) []award.Offer {
	want := q.Date.Format(dateLayout)
	var out []award.Offer
	for _, d := range a.Data {
		if d.DepartureDate != want || len(d.Bounds) == 0 {
			continue
		}
		o, ok := a.offer(q, d, asked)
		if ok {
			out = append(out, o)
		}
	}
	return out
}

// offer builds the offer of day d. It returns false when the day has no
// price in miles.
func (a *answer) offer(
	q award.Query,
	d day,
	asked award.Cabin,
) (award.Offer, bool) {
	miles := d.Prices.MilesConversion.ConvertedMiles.Base
	if miles <= 0 {
		return award.Offer{}, false
	}
	pax := max(q.Passengers, 1)
	taxes := a.taxes(d.Prices)
	cabin := a.cabin(d.FareFamilyCode, asked)
	o := award.Offer{
		Program:      Key,
		Availability: award.Estimated,
		Origin:       strings.ToUpper(q.Origin),
		Destination:  strings.ToUpper(q.Destination),
		Cabin:        cabin,
		Fare:         d.FareFamilyCode,
		Price: award.Price{
			PointsPerPassenger: miles,
			PointsTotal:        miles * pax,
			TaxesPerPassenger:  taxes,
			TaxesTotal: award.Money{
				Amount:   round2(taxes.Amount * float64(pax)),
				Currency: taxes.Currency,
			},
		},
		BookingURL: bookingURL,
		Notes: []string{
			"cheapest award of the day from the Miles & More calendar",
		},
	}
	segs := a.segments(q, d.DepartureDate, len(d.Bounds[0].Flights), cabin)
	if len(segs) == 0 {
		o.Departure = q.Date
		o.Notes = append(o.Notes, "flights not given")
		return o, true
	}
	first, last := segs[0], segs[len(segs)-1]
	o.Origin = first.Origin
	o.Destination = last.Destination
	o.Departure = first.Departure
	o.Arrival = last.Arrival
	o.DurationMinutes = int(last.Arrival.Sub(first.Departure).Minutes())
	o.Stops = len(segs) - 1
	o.Segments = segs
	for _, s := range segs {
		if !containsString(o.Airlines, s.OperatingCarrier) {
			o.Airlines = append(o.Airlines, s.OperatingCarrier)
		}
	}
	return o, true
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

// round2 rounds v to two decimals.
func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// taxes returns the taxes of p in the currency's major unit.
func (a *answer) taxes(p prices) award.Money {
	if len(p.TotalPrices) == 0 {
		return award.Money{Currency: taxCurrency}
	}
	t := p.TotalPrices[0]
	places := 2
	if c, ok := a.Dictionaries.Currency[t.CurrencyCode]; ok {
		places = c.DecimalPlaces
	}
	return award.Money{
		Amount:   float64(t.TotalTaxes) / math.Pow10(places),
		Currency: t.CurrencyCode,
	}
}

// cabin returns the cabin of fare family code, or asked when the answer
// does not say.
func (a *answer) cabin(code string, asked award.Cabin) award.Cabin {
	ff, ok := a.Dictionaries.FareFamilyWithServices[code]
	if !ok {
		return asked
	}
	for _, cf := range cabinFamilies {
		if cf.family == ff.CommercialFareFamily {
			return cf.cabin
		}
	}
	return asked
}

// matches reports whether code is the location want, or an airport of the
// city want.
func (a *answer) matches(code, want string) bool {
	want = strings.ToUpper(want)
	if code == want {
		return true
	}
	loc, ok := a.Dictionaries.Location[code]
	return ok && loc.CityCode == want
}

// segments rebuilds the n flights of the award leaving on date from the
// dictionary: a chain that leaves the origin of q on date, connects at each
// arrival airport and ends at the destination of q. It returns nil when no
// single such chain exists.
func (a *answer) segments(
	q award.Query,
	date string,
	n int,
	cabin award.Cabin,
) []award.Segment {
	if n < 1 {
		return nil
	}
	var chains [][]flight
	for _, f := range a.Dictionaries.Flight {
		if !strings.HasPrefix(f.Departure.DateTime, date) ||
			!a.matches(f.Departure.LocationCode, q.Origin) {
			continue
		}
		chains = append(chains, a.extend([]flight{f}, n, q.Destination)...)
	}
	if len(chains) != 1 {
		return nil
	}
	return a.toSegments(chains[0], cabin)
}

// extend returns every chain of n flights that starts with chain and ends
// at dest, each flight leaving where and after the one before lands.
func (a *answer) extend(chain []flight, n int, dest string) [][]flight {
	last := chain[len(chain)-1]
	if len(chain) == n {
		if a.matches(last.Arrival.LocationCode, dest) {
			return [][]flight{chain}
		}
		return nil
	}
	landed, err := time.Parse(timeLayout, last.Arrival.DateTime)
	if err != nil {
		return nil
	}
	var out [][]flight
	for _, f := range a.Dictionaries.Flight {
		if f.Departure.LocationCode != last.Arrival.LocationCode {
			continue
		}
		left, err := time.Parse(timeLayout, f.Departure.DateTime)
		if err != nil || !left.After(landed) ||
			left.Sub(landed) > 24*time.Hour {
			continue
		}
		next := append(append([]flight{}, chain...), f)
		out = append(out, a.extend(next, n, dest)...)
	}
	return out
}

// toSegments converts a chain of dictionary flights to segments. It returns
// nil when a time cannot be parsed.
func (a *answer) toSegments(chain []flight, cabin award.Cabin) []award.Segment {
	segs := make([]award.Segment, 0, len(chain))
	for _, f := range chain {
		dep, err := time.Parse(timeLayout, f.Departure.DateTime)
		if err != nil {
			return nil
		}
		arr, err := time.Parse(timeLayout, f.Arrival.DateTime)
		if err != nil {
			return nil
		}
		aircraft := a.Dictionaries.Aircraft[f.AircraftCode]
		if aircraft == "" {
			aircraft = f.AircraftCode
		}
		operating := f.OperatingAirlineCode
		if operating == "" {
			operating = f.MarketingAirlineCode
		}
		segs = append(segs, award.Segment{
			Flight:           f.MarketingAirlineCode + f.MarketingFlightNumber,
			MarketingCarrier: f.MarketingAirlineCode,
			OperatingCarrier: operating,
			Origin:           f.Departure.LocationCode,
			Destination:      f.Arrival.LocationCode,
			Departure:        dep,
			Arrival:          arr,
			DurationMinutes:  int(arr.Sub(dep).Minutes()),
			Aircraft:         aircraft,
			Cabin:            cabin,
		})
	}
	for i := 0; i < len(segs)-1; i++ {
		segs[i].LayoverMinutes = int(
			segs[i+1].Departure.Sub(segs[i].Arrival).Minutes(),
		)
	}
	return segs
}
