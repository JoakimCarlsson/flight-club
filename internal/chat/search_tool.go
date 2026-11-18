package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/joakimcarlsson/ai/tool"

	"github.com/joakimcarlsson/flight-club/internal/award"
)

// topOffers is how many offers per direction the model is shown. The
// traveller sees every offer in the panel; the model only needs the best to
// write its summary, and a long list invites it to repeat the list in its
// reply.
const topOffers = 3

// searchTool is search_awards: one award search across the programs, its
// progress and results reported to the panel as they happen.
type searchTool struct {
	awards *award.Searcher
	emit   Emit
}

// searchArgs are the arguments the model fills in.
type searchArgs struct {
	Origin      string   `json:"origin"               desc:"IATA city or airport code to depart from, e.g. STO or ARN."`
	Destination string   `json:"destination"          desc:"IATA city or airport code to arrive at, e.g. NYC or JFK."`
	DepartFrom  string   `json:"departFrom"           desc:"First departure date, YYYY-MM-DD."`
	DepartTo    string   `json:"departTo,omitempty"   desc:"Last departure date, YYYY-MM-DD. At most 7 days after departFrom. Leave out for one day."`
	ReturnFrom  string   `json:"returnFrom,omitempty" desc:"First return date, YYYY-MM-DD. Leave out for one-way."`
	ReturnTo    string   `json:"returnTo,omitempty"   desc:"Last return date, YYYY-MM-DD. Leave out for one day."`
	Passengers  int      `json:"passengers,omitempty" desc:"Adult passengers, 1 to 9. Defaults to 1."`
	Cabins      []string `json:"cabins,omitempty"     desc:"Cabins to keep: economy, premium, business, first. Leave out for every cabin."`
	Programs    []string `json:"programs,omitempty"   desc:"Program keys to ask. Leave out for every program."`
}

// SearchView is a search as the panel shows it: what the assistant
// understood the traveller to want.
type SearchView struct {
	// Origin is the IATA code departed from.
	Origin string `json:"origin"`
	// Destination is the IATA code arrived at.
	Destination string `json:"destination"`
	// DepartFrom is the first departure date.
	DepartFrom string `json:"departFrom"`
	// DepartTo is the last departure date.
	DepartTo string `json:"departTo"`
	// ReturnFrom is the first return date, empty for one-way.
	ReturnFrom string `json:"returnFrom,omitempty"`
	// ReturnTo is the last return date, empty for one-way.
	ReturnTo string `json:"returnTo,omitempty"`
	// Passengers is how many adults travel.
	Passengers int `json:"passengers"`
	// Cabins are the cabins kept, empty for every cabin.
	Cabins []award.Cabin `json:"cabins"`
	// Programs are the programs asked.
	Programs []string `json:"programs"`
}

// Info describes the tool to the model.
func (t *searchTool) Info() tool.Info {
	keys := strings.Join(t.awards.Keys(), ", ")
	return tool.NewInfo(
		"search_awards",
		"Search live award availability, priced in loyalty points, across "+
			"airline loyalty programs ("+keys+"). Searches every day of the "+
			"departure range and, when given, of the return range. Returns the "+
			"cheapest offers per direction and how many there were in all; "+
			"the traveller sees every offer in a panel.",
		searchArgs{},
	)
}

// Run runs the search the model asked for, reporting it, its progress and
// its results to the panel, and answers the model with a summary. A search
// that cannot be run is answered as a tool error the model can correct.
func (t *searchTool) Run(
	ctx context.Context,
	call tool.Call,
) (tool.Response, error) {
	var args searchArgs
	if err := json.Unmarshal([]byte(call.Input), &args); err != nil {
		return tool.NewTextErrorResponse("arguments are not valid JSON: " +
			err.Error()), nil
	}
	search, err := args.search()
	if err != nil {
		return tool.NewTextErrorResponse(err.Error()), nil
	}
	if err := search.Validate(); err != nil {
		return tool.NewTextErrorResponse(err.Error()), nil
	}

	view := t.view(search)
	t.emit(Event{Kind: EventSearch, CallID: call.ID, Search: &view})

	res, err := t.awards.Run(ctx, search, func(st award.Step) {
		t.emit(Event{Kind: EventStep, CallID: call.ID, Step: &st})
	})
	if errors.Is(err, award.ErrInvalidSearch) {
		return tool.NewTextErrorResponse(err.Error()), nil
	}
	if err != nil {
		return tool.Response{}, err
	}
	t.emit(Event{Kind: EventResults, CallID: call.ID, Results: &res})

	b, err := json.Marshal(summarise(res))
	if err != nil {
		return tool.Response{}, fmt.Errorf("chat: summarising: %w", err)
	}
	return tool.NewTextResponse(string(b)), nil
}

// search turns the model's arguments into a search, applying defaults. It
// returns an error naming the argument that does not parse.
func (a searchArgs) search() (award.Search, error) {
	depart, err := dates("departFrom", a.DepartFrom, "departTo", a.DepartTo)
	if err != nil {
		return award.Search{}, err
	}
	var ret *award.DateRange
	if a.ReturnFrom != "" {
		r, err := dates("returnFrom", a.ReturnFrom, "returnTo", a.ReturnTo)
		if err != nil {
			return award.Search{}, err
		}
		ret = &r
	}
	cabins := make([]award.Cabin, 0, len(a.Cabins))
	for _, name := range a.Cabins {
		c, ok := award.ParseCabin(name)
		if !ok {
			return award.Search{}, fmt.Errorf(
				"unknown cabin %q; use economy, premium, business or first",
				name,
			)
		}
		cabins = append(cabins, c)
	}
	programs := make([]string, 0, len(a.Programs))
	for _, p := range a.Programs {
		programs = append(programs, strings.ToLower(strings.TrimSpace(p)))
	}
	return award.Search{
		Origin:      strings.ToUpper(strings.TrimSpace(a.Origin)),
		Destination: strings.ToUpper(strings.TrimSpace(a.Destination)),
		Depart:      depart,
		Return:      ret,
		Passengers:  max(a.Passengers, 1),
		Cabins:      cabins,
		Programs:    programs,
	}, nil
}

// dates parses the dates of the arguments named fromName and toName, with to
// defaulting to from. It returns an error naming the argument that is not
// YYYY-MM-DD.
func dates(fromName, from, toName, to string) (award.DateRange, error) {
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

// view is search as the panel shows it.
func (t *searchTool) view(s award.Search) SearchView {
	programs := s.Programs
	if len(programs) == 0 {
		programs = t.awards.Keys()
	}
	v := SearchView{
		Origin:      s.Origin,
		Destination: s.Destination,
		DepartFrom:  s.Depart.From.Format(time.DateOnly),
		DepartTo:    s.Depart.To.Format(time.DateOnly),
		Passengers:  s.Passengers,
		Cabins:      s.Cabins,
		Programs:    programs,
	}
	if s.Return != nil {
		v.ReturnFrom = s.Return.From.Format(time.DateOnly)
		v.ReturnTo = s.Return.To.Format(time.DateOnly)
	}
	return v
}

// summary is what the model reads back from a search.
type summary struct {
	// Total is how many offers were found in all.
	Total int `json:"total"`
	// Outbound are the cheapest outbound offers.
	Outbound []brief `json:"outbound"`
	// Return are the cheapest return offers.
	Return []brief `json:"return,omitempty"`
	// CheapestByDay is the lowest points per passenger on each departure
	// date, keyed by direction and then date.
	CheapestByDay map[award.Direction]map[string]int `json:"cheapestByDay"`
	// Note tells the model how to use the summary.
	Note string `json:"note"`
	// Errors are the program-days that failed.
	Errors []award.ProgramError `json:"errors,omitempty"`
}

// summaryNote is the instruction carried with every summary, where the
// model reads it right before it writes.
const summaryNote = "The traveller already sees every offer in the panel " +
	"and the best one pinned in the chat. Reply in at most three short " +
	"sentences. Do not list flights or flight numbers."

// brief is one offer as the model reads it.
type brief struct {
	Program        string      `json:"program"`
	Date           string      `json:"date"`
	Route          string      `json:"route"`
	Flights        string      `json:"flights"`
	Departure      string      `json:"departure"`
	Arrival        string      `json:"arrival"`
	Stops          int         `json:"stops"`
	Cabin          award.Cabin `json:"cabin"`
	PointsPerPax   int         `json:"pointsPerPassenger"`
	TaxesPerPax    string      `json:"taxesPerPassenger"`
	Seats          *int        `json:"seats"`
	Availability   string      `json:"availability"`
	DurationMinute int         `json:"durationMinutes"`
}

// summarise keeps the cheapest offers of each direction of res.
func summarise(res award.Result) summary {
	out := summary{
		Total:         len(res.Offers),
		Outbound:      []brief{},
		CheapestByDay: map[award.Direction]map[string]int{},
		Note:          summaryNote,
	}
	for _, o := range res.Offers {
		days := out.CheapestByDay[o.Direction]
		if days == nil {
			days = map[string]int{}
			out.CheapestByDay[o.Direction] = days
		}
		d := o.Departure.Format(time.DateOnly)
		if p, ok := days[d]; !ok || o.Price.PointsPerPassenger < p {
			days[d] = o.Price.PointsPerPassenger
		}
	}
	for _, dir := range []award.Direction{award.Outbound, award.Inbound} {
		var offers []award.Offer
		for _, o := range res.Offers {
			if o.Direction == dir {
				offers = append(offers, o)
			}
		}
		slices.SortStableFunc(offers, func(a, b award.Offer) int {
			return a.Price.PointsPerPassenger - b.Price.PointsPerPassenger
		})
		briefs := make([]brief, 0, min(len(offers), topOffers))
		for _, o := range offers[:min(len(offers), topOffers)] {
			briefs = append(briefs, briefOf(o))
		}
		if dir == award.Outbound {
			out.Outbound = briefs
		} else {
			out.Return = briefs
		}
	}
	out.Errors = res.Errors
	return out
}

// briefOf is o as the model reads it.
func briefOf(o award.Offer) brief {
	flights := make([]string, 0, len(o.Segments))
	route := []string{o.Origin}
	for _, s := range o.Segments {
		flights = append(flights, s.Flight)
		route = append(route, s.Destination)
	}
	return brief{
		Program:      o.Program,
		Date:         o.Departure.Format(time.DateOnly),
		Route:        strings.Join(route, "-"),
		Flights:      strings.Join(flights, " "),
		Departure:    o.Departure.Format("15:04"),
		Arrival:      o.Arrival.Format("2006-01-02 15:04"),
		Stops:        o.Stops,
		Cabin:        o.Cabin,
		PointsPerPax: o.Price.PointsPerPassenger,
		TaxesPerPax: fmt.Sprintf(
			"%.2f %s",
			o.Price.TaxesPerPassenger.Amount,
			o.Price.TaxesPerPassenger.Currency,
		),
		Seats:          o.Seats,
		Availability:   string(o.Availability),
		DurationMinute: o.DurationMinutes,
	}
}
