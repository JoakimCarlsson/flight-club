package httpx

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/joakimcarlsson/flight-club/internal/award"
	"github.com/joakimcarlsson/minmux/openapi"
	"github.com/joakimcarlsson/minmux/outputcache"
	"github.com/joakimcarlsson/minmux/router"
)

// awardCacheDuration is how long a search answer is served from the cache.
// Award space moves slowly enough that a few minutes old is still useful,
// and every program hit is a request a program may rate limit.
const awardCacheDuration = 5 * time.Minute

// awardSearchParams binds an award search from the query string.
type awardSearchParams struct {
	From       string `query:"from,required"       desc:"IATA airport or city code to depart from, e.g. SEA or NYC."`
	To         string `query:"to,required"         desc:"IATA airport or city code to arrive at."`
	DepartFrom string `query:"departFrom,required" desc:"First departure date, YYYY-MM-DD."`
	DepartTo   string `query:"departTo"            desc:"Last departure date, YYYY-MM-DD. Defaults to departFrom. At most 7 days after it."`
	ReturnFrom string `query:"returnFrom"          desc:"First return date, YYYY-MM-DD. Leave out for a one-way search."`
	ReturnTo   string `query:"returnTo"            desc:"Last return date, YYYY-MM-DD. Defaults to returnFrom."`
	Passengers int    `query:"passengers"          desc:"Adult passengers, 1 to 9. Defaults to 1."`
	Cabins     string `query:"cabins"              desc:"Comma-separated cabins to keep: economy, premium, business, first. Defaults to every cabin."`
	Programs   string `query:"programs"            desc:"Comma-separated program keys to ask, e.g. alaska. Defaults to every program."`
}

// registerAwards registers the award search endpoint together with its
// OpenAPI description.
func (s *Server) registerAwards() {
	s.router.Get("/api/awards", s.searchAwards,
		openapi.Tags("Awards"),
		openapi.Summary("Search award availability"),
		openapi.Description(
			"Asks every loyalty program, or the ones named, for award availability on every day of the departure range and, for a return search, of the return range. Each direction is priced separately, as award seats are. A program that fails on a day is listed under errors and does not fail the search. Answers are cached for five minutes.",
		),
		openapi.ReturnsBody[award.Result](
			http.StatusOK, "The offers found."),
		openapi.ReturnsBody[router.ProblemDetails](
			http.StatusBadRequest, "The search is not one that can be run."),
		outputcache.WithOutputCache(awardCacheDuration,
			outputcache.VaryByQuery(
				"from", "to", "departFrom", "departTo", "returnFrom",
				"returnTo", "passengers", "cabins", "programs",
			),
		),
	)
}

// searchAwards handles GET /api/awards: it runs the search p describes
// across the programs and answers with every offer found.
func (s *Server) searchAwards(c *router.Context, p awardSearchParams) {
	search, err := p.search()
	if err != nil {
		c.JSON(http.StatusBadRequest, router.BadRequest(err.Error()))
		return
	}
	res, err := s.awards.Run(c.Ctx(), search, nil)
	if errors.Is(err, award.ErrInvalidSearch) {
		c.JSON(http.StatusBadRequest, router.BadRequest(err.Error()))
		return
	}
	if err != nil {
		slog.ErrorContext(c.Ctx(), "award search failed", "error", err)
		c.JSON(http.StatusInternalServerError,
			router.InternalServerError("the search failed"))
		return
	}
	c.JSON(http.StatusOK, res)
}

// search turns the parameters into a search, applying the defaults. It
// returns an error naming the parameter that does not parse.
func (p awardSearchParams) search() (award.Search, error) {
	depart, err := dateRange("departFrom", p.DepartFrom, "departTo", p.DepartTo)
	if err != nil {
		return award.Search{}, err
	}
	var ret *award.DateRange
	if p.ReturnFrom != "" {
		r, err := dateRange("returnFrom", p.ReturnFrom, "returnTo", p.ReturnTo)
		if err != nil {
			return award.Search{}, err
		}
		ret = &r
	} else if p.ReturnTo != "" {
		return award.Search{}, errors.New("returnTo needs returnFrom")
	}
	var cabins []award.Cabin
	for _, name := range list(p.Cabins) {
		cabin, ok := award.ParseCabin(name)
		if !ok {
			return award.Search{}, fmt.Errorf("unknown cabin %q", name)
		}
		cabins = append(cabins, cabin)
	}
	passengers := p.Passengers
	if passengers == 0 {
		passengers = 1
	}
	return award.Search{
		Origin:      strings.ToUpper(strings.TrimSpace(p.From)),
		Destination: strings.ToUpper(strings.TrimSpace(p.To)),
		Depart:      depart,
		Return:      ret,
		Passengers:  passengers,
		Cabins:      cabins,
		Programs:    list(p.Programs),
	}, nil
}

// dateRange parses the dates of the parameters named fromName and toName,
// with to defaulting to from. It returns an error naming the parameter that
// is not YYYY-MM-DD.
func dateRange(fromName, from, toName, to string) (award.DateRange, error) {
	first, err := time.Parse(time.DateOnly, from)
	if err != nil {
		return award.DateRange{}, fmt.Errorf("%s must be YYYY-MM-DD", fromName)
	}
	if to == "" {
		return award.DateRange{From: first, To: first}, nil
	}
	last, err := time.Parse(time.DateOnly, to)
	if err != nil {
		return award.DateRange{}, fmt.Errorf("%s must be YYYY-MM-DD", toName)
	}
	return award.DateRange{From: first, To: last}, nil
}

// list splits a comma-separated parameter, lower-casing and dropping empty
// entries.
func list(raw string) []string {
	var out []string
	for k := range strings.SplitSeq(raw, ",") {
		if k = strings.ToLower(strings.TrimSpace(k)); k != "" {
			out = append(out, k)
		}
	}
	return out
}
