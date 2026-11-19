package lufthansa

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// atlasURL is the gateway's list of countries, cities and airports that
// the site's airport picker reads.
const atlasURL = "https://api.miles-and-more.com/content/v3/atlas/" +
	"airport-atlas.json?lang=en"

// allPrefix marks the atlas's "all airports of a city" pseudo airports,
// which the gateway does not search.
const allPrefix = "ALL_"

// maxOriginAirports is the most airports of an origin city searched.
const maxOriginAirports = 4

// atlasDoc is the part of the airport atlas that is read.
type atlasDoc struct {
	Language struct {
		Countries []struct {
			Code   string `json:"code"`
			Cities []struct {
				Code     string `json:"code"`
				Airports []struct {
					Code string `json:"code"`
				} `json:"airports"`
			} `json:"cities"`
		} `json:"countries"`
	} `json:"language"`
}

// geo knows the country of every location and the airports of every
// city.
type geo struct {
	country  map[string]string
	airports map[string][]string
}

// origin is one airport a search departs from, with the country of
// commencement and currency the gateway wants for it.
type origin struct {
	code     string
	country  string
	currency string
}

// origins returns the airports to search from for location code: the
// airports of a city, or the location itself. A location the atlas does
// not know is searched as it is, from the default country.
func (g *geo) origins(code string) []origin {
	code = strings.ToUpper(code)
	country, ok := g.country[code]
	if !ok {
		return []origin{{code, commencementCountry, taxCurrency}}
	}
	currency := countryCurrencies[country]
	if currency == "" {
		currency = taxCurrency
	}
	codes := g.airports[code]
	if len(codes) == 0 {
		codes = []string{code}
	}
	if len(codes) > maxOriginAirports {
		codes = codes[:maxOriginAirports]
	}
	out := make([]origin, 0, len(codes))
	for _, c := range codes {
		out = append(out, origin{c, country, currency})
	}
	return out
}

// geoRetry is how long a failed atlas fetch is not tried again.
const geoRetry = 10 * time.Minute

// loadGeo returns the cached geo of p, fetching the airport atlas on first
// use. When the atlas cannot be fetched it returns an empty geo, which
// searches every location as given from the default country, and tries
// the atlas again after geoRetry.
func (p *Program) loadGeo(ctx context.Context) *geo {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.geo != nil {
		return p.geo
	}
	if time.Now().Before(p.retry) {
		return &geo{}
	}
	g, err := p.fetchGeo(ctx)
	if err != nil {
		p.retry = time.Now().Add(geoRetry)
		return &geo{}
	}
	p.geo = g
	return g
}

// fetchGeo fetches the airport atlas. It fails when the atlas cannot be
// fetched or read.
func (p *Program) fetchGeo(ctx context.Context) (*geo, error) {
	raw, err := p.get(ctx, atlasURL)
	if err != nil {
		return nil, fmt.Errorf("airport atlas: %w", err)
	}
	var doc atlasDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("airport atlas: %w", err)
	}
	g := &geo{
		country:  map[string]string{},
		airports: map[string][]string{},
	}
	for _, c := range doc.Language.Countries {
		cc := strings.ToUpper(c.Code)
		for _, city := range c.Cities {
			code := strings.ToUpper(city.Code)
			g.country[code] = cc
			for _, a := range city.Airports {
				ac := strings.ToUpper(a.Code)
				if strings.HasPrefix(ac, allPrefix) {
					continue
				}
				g.country[ac] = cc
				g.airports[code] = append(g.airports[code], ac)
			}
		}
	}
	return g, nil
}

// get fetches url from the gateway. It fails when the request fails or
// the answer is not 200.
func (p *Program) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Origin", siteOrigin)
	req.Header.Set("X-Api-Key", apiKey)
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
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return raw, nil
}
