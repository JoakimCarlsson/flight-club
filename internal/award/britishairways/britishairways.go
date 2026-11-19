// Package britishairways searches British Airways Club award availability,
// priced in Avios plus taxes, fees and carrier charges in cash, on British
// Airways' own flights and on its partners' (American Airlines, Iberia,
// Qatar Airways, Japan Airlines and the rest of oneworld).
//
// britishairways.com only searches Reward Flights for a signed-in member,
// and its search is the classic "Book with Avios" form, which posts a
// one-time form token and answers with server-rendered HTML rather than
// JSON; a deep link to the results is refused as a second tab. Search
// therefore carries a saved session's cookies into the stealth browser,
// opens the form, signs in on accounts.britishairways.com when the site
// no longer knows the member (see session.go), fills the form in as a
// person would and reads the itineraries off the results page. The
// results list no prices, so every cabin of every itinerary is selected
// in turn and the total price the page then loads is read.
//
// The results show three cabins around the one asked for, so the form is
// sent twice: for economy (economy, premium economy and business) and for
// first (premium economy, business and first).
package britishairways

import (
	"context"
	"errors"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/joakimcarlsson/flight-club/internal/award"
	"github.com/joakimcarlsson/flight-club/internal/browser"
)

// Key is the program key British Airways offers carry.
const Key = "britishairways"

// formURL is the Book with Avios form a traveller searches and books
// from.
const formURL = "https://www.britishairways.com/travel/redeem/execclub/" +
	"_gf/en_gb?eId=106019&tab_selected=redeem&redemption_type=STD_RED"

// loadTimeout bounds each navigation.
const loadTimeout = 60 * time.Second

// errBlocked is returned when britishairways.com answers with a denial or
// a gateway error instead of a page.
var errBlocked = errors.New(
	"britishairways: blocked by the site or its gateway",
)

// Program searches britishairways.com through a stealth browser, signed
// in as one British Airways Club member.
type Program struct {
	browser *browser.Browser
	account Account
	session *session
}

// New builds a Program that loads britishairways.com in b, signed in as
// a, keeping its session in a.SessionFile.
func New(b *browser.Browser, a Account) *Program {
	return &Program{
		browser: b,
		account: a,
		session: &session{path: a.sessionPath()},
	}
}

// Key returns the program key, "britishairways".
func (p *Program) Key() string {
	return Key
}

// Search returns British Airways' award offers for q, one per cabin of
// every itinerary on British Airways or its partners. It returns no
// offers when the site has no award space, and an error when the browser
// cannot load the site, the site blocks it, the member cannot be signed
// in without help (errSignInNeeded, returned at once from then on until
// the session file changes), or the results do not load.
func (p *Program) Search(
	ctx context.Context,
	q award.Query,
) ([]award.Offer, error) {
	if p.session.isStuck() {
		return nil, errSignInNeeded
	}
	var rows []rawRow
	captured, err := p.browser.Collect(ctx, formURL, browser.CollectOpts{
		Timeout: loadTimeout,
		Cookies: p.session.load(),
		Accept:  func(string, string) bool { return false },
		Interact: func(ctx context.Context, page *browser.Page) error {
			if blocked(page) {
				return errBlocked
			}
			if !signedIn(page) {
				if err := signIn(ctx, page, p.account, nil); err != nil {
					return err
				}
				if !signedIn(page) {
					return errSignInNeeded
				}
			}
			var err error
			rows, err = searchPage(ctx, page, q)
			return err
		},
	})
	if errors.Is(err, errSignInNeeded) {
		p.session.markStuck()
	}
	if err != nil {
		return nil, shortError(err)
	}
	_ = p.session.save(captured.Cookies)
	return offers(rows, q), nil
}

// blocked reports whether page shows a denial or a gateway error.
func blocked(page *browser.Page) bool {
	v, err := page.Evaluate(`/Bad Gateway|Access Denied/` +
		`.test(document.body ? document.body.innerText : '')`)
	return err == nil && v == true
}

// shortError returns err, or an error with its message cut short and
// prefixed with the program when it is long or not the program's own.
func shortError(err error) error {
	msg := err.Error()
	if strings.HasPrefix(msg, "britishairways:") && len(msg) < 150 {
		return err
	}
	return errors.New("britishairways: " + shorten(msg, 120))
}

// offers turns every priced cabin of every itinerary in rows into an
// offer for q.
func offers(rows []rawRow, q award.Query) []award.Offer {
	out := []award.Offer{}
	passengers := max(q.Passengers, 1)
	for _, r := range rows {
		segs, ok := segments(r, q.Date)
		if !ok {
			continue
		}
		for _, c := range r.Cabins {
			if o, ok := offer(r, segs, c, passengers); ok {
				out = append(out, o)
			}
		}
	}
	return out
}

// columnCabins maps the results' cabin columns to cabins.
var columnCabins = map[string]award.Cabin{
	"M": award.Economy,
	"W": award.Premium,
	"C": award.Business,
	"F": award.First,
}

// cabinOf maps a cabin name the results give a segment, such as "World
// Traveller Plus" or "Club World", to a cabin. It returns false for a
// name it does not know.
func cabinOf(name string) (award.Cabin, bool) {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "first"):
		return award.First, true
	case strings.Contains(n, "plus"), strings.Contains(n, "premium"):
		return award.Premium, true
	case strings.Contains(n, "club"), strings.Contains(n, "business"):
		return award.Business, true
	case strings.Contains(n, "traveller"), strings.Contains(n, "economy"):
		return award.Economy, true
	}
	return "", false
}

// rank orders cabins from economy up.
var rank = map[award.Cabin]int{
	award.Economy:  0,
	award.Premium:  1,
	award.Business: 2,
	award.First:    3,
}

// offer builds the offer for cabin c of itinerary r, whose flights are
// segs, for passengers. It returns false when c's price cannot be read.
func offer(
	r rawRow,
	segs []award.Segment,
	c rawCabin,
	passengers int,
) (award.Offer, bool) {
	points, taxes, ok := parsePrice(c.Price)
	if !ok {
		return award.Offer{}, false
	}
	cabin, ok := columnCabins[c.Code]
	if !ok {
		return award.Offer{}, false
	}
	booked := slices.Clone(segs)
	mixed := false
	var airlines []string
	for i := range booked {
		booked[i].Cabin = cabin
		if i < len(c.SegCabins) {
			if sc, ok := cabinOf(c.SegCabins[i]); ok {
				booked[i].Cabin = sc
			}
		}
		if booked[i].Cabin != cabin {
			mixed = true
		}
		if i < len(c.Classes) && len(c.Classes) == len(booked) {
			booked[i].BookingCode = c.Classes[i : i+1]
		}
		if !slices.Contains(airlines, booked[i].OperatingCarrier) {
			airlines = append(airlines, booked[i].OperatingCarrier)
		}
	}
	last := len(booked) - 1
	duration := parseDuration(r.Total)
	if duration == 0 {
		duration = int(booked[last].Arrival.Sub(booked[0].Departure).Minutes())
	}
	perPoints := points / passengers
	perTaxes := roundCents(taxes.Amount / float64(passengers))
	return award.Offer{
		Program:         Key,
		Availability:    award.Confirmed,
		Origin:          booked[0].Origin,
		Destination:     booked[last].Destination,
		Departure:       booked[0].Departure,
		Arrival:         booked[last].Arrival,
		DurationMinutes: duration,
		Stops:           last,
		Airlines:        airlines,
		Cabin:           cabin,
		MixedCabin:      mixed,
		Fare:            fareName(c.Code, r.OffPeak),
		Price: award.Price{
			PointsPerPassenger: perPoints,
			PointsTotal:        points,
			TaxesPerPassenger: award.Money{
				Amount:   perTaxes,
				Currency: taxes.Currency,
			},
			TaxesTotal: taxes,
		},
		Seats:      c.Seats,
		BookingURL: formURL,
		Notes:      notes(booked, r.OffPeak),
		Segments:   booked,
	}, true
}

// fareName names the fare of cabin column code, marking off-peak days.
func fareName(code string, offPeak bool) string {
	name := "REWARD_" + string(columnCabins[code])
	if offPeak {
		name += "_OFFPEAK"
	}
	return strings.ToUpper(name)
}

// notes are what the results say about an itinerary beyond its price:
// partner flights and off-peak pricing.
func notes(segs []award.Segment, offPeak bool) []string {
	var out []string
	for _, s := range segs {
		if s.OperatingCarrier != "BA" {
			out = append(out, s.Flight+" operated by partner "+
				s.OperatingCarrier)
		}
	}
	if offPeak {
		out = append(out, "Off-peak day")
	}
	return out
}

// pricePattern matches a total price such as "66000 Avios + 5,737.05 SEK".
var pricePattern = regexp.MustCompile(
	`([\d,.]+)\s*Avios\s*\+\s*([\d,.]+)\s*([A-Z]{3})`,
)

// parsePrice reads a total price such as "66000 Avios + 5,737.05 SEK"
// into its Avios and cash. It returns false when s is not such a price.
func parsePrice(s string) (int, award.Money, bool) {
	m := pricePattern.FindStringSubmatch(s)
	if m == nil {
		return 0, award.Money{}, false
	}
	points, err := strconv.Atoi(strings.NewReplacer(",", "", ".", "").
		Replace(m[1]))
	if err != nil || points <= 0 {
		return 0, award.Money{}, false
	}
	cash, err := strconv.ParseFloat(strings.ReplaceAll(m[2], ",", ""), 64)
	if err != nil {
		return 0, award.Money{}, false
	}
	return points, award.Money{Amount: roundCents(cash), Currency: m[3]},
		true
}

// roundCents rounds v to two decimals.
func roundCents(v float64) float64 {
	return math.Round(v*100) / 100
}

// durationPattern matches a duration such as "6 h 23m" or "8h 05m".
var durationPattern = regexp.MustCompile(`(\d+)\s*h\s*(\d+)\s*m`)

// parseDuration reads a duration such as "6 h 23m" in minutes, 0 when s
// is not one.
func parseDuration(s string) int {
	m := durationPattern.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	h, _ := strconv.Atoi(m[1])
	mins, _ := strconv.Atoi(m[2])
	return h*60 + mins
}
