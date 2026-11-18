package ana

import (
	"time"

	"github.com/joakimcarlsson/flight-club/internal/award"
)

// chartURL is ANA's international award chart the miles come from.
const chartURL = "https://www.ana.co.jp/en/jp/guide/amc/award/" +
	"international/terms/"

// The seasons of ANA's award chart, as indexes into a chart row.
const (
	low = iota
	regular
	high
)

// seasonNames names the seasons for notes.
var seasonNames = [...]string{"low season", "regular season", "high season"}

// oneWayMiles is the one-way miles between Japan and each zone per cabin,
// [low, regular, high] season, for awards issued from 2025-06-24.
var oneWayMiles = map[string]map[award.Cabin][3]int{
	"Z2": {
		award.Economy:  {6000, 7500, 12000},
		award.Business: {18000, 20500, 25000},
	},
	"Z3": {
		award.Economy:  {8500, 10000, 15000},
		award.Premium:  {15000, 16500, 23500},
		award.Business: {24000, 26500, 32500},
	},
	"Z4": {
		award.Economy:  {15000, 17500, 25000},
		award.Premium:  {23000, 25500, 35500},
		award.Business: {40000, 42500, 47500},
		award.First:    {57500, 60000, 85500},
	},
	"Z5": {
		award.Economy:  {17500, 20000, 32500},
		award.Premium:  {26500, 29000, 44000},
		award.Business: {40000, 42500, 67500},
		award.First:    {60000, 70000, 120000},
	},
	"Z6": {
		award.Economy:  {20000, 25000, 36000},
		award.Premium:  {31000, 36000, 50500},
		award.Business: {50000, 52500, 82500},
		award.First:    {75000, 85000, 150000},
	},
	"Z7": {
		award.Economy:  {22500, 27500, 39000},
		award.Premium:  {33500, 38500, 53500},
		award.Business: {55000, 57500, 90000},
		award.First:    {82500, 95000, 165000},
	},
	"ZA": {
		award.Economy:  {18500, 22500, 32500},
		award.Premium:  {27000, 31000, 44000},
		award.Business: {40000, 45000, 67500},
	},
}

// span is an inclusive range of days of a year, as "MM-DD".
type span [2]string

// seasonYear is one year of a seasonality chart: its low and high spans,
// every other day being regular, and the last day it covers, empty for the
// whole year.
type seasonYear struct {
	low, high []span
	until     string
}

// zoneGroup maps each zone to the seasonality chart it follows.
var zoneGroup = map[string]string{
	"Z2": "asia", "Z3": "asia", "Z4": "asia",
	"Z6": "longhaul", "Z7": "longhaul",
	"Z5": "resort", "ZA": "resort",
}

// seasons are ANA's seasonality charts by group and year.
var seasons = map[string]map[int]seasonYear{
	"asia": {
		2026: {
			low: []span{
				{"01-05", "02-13"}, {"04-01", "04-28"}, {"05-11", "06-30"},
			},
			high: []span{
				{"01-01", "01-04"}, {"04-29", "05-10"},
				{"07-18", "08-23"}, {"12-21", "12-31"},
			},
		},
		2027: {
			low: []span{
				{"01-05", "02-03"}, {"04-12", "04-28"},
				{"05-10", "06-30"}, {"12-01", "12-19"},
			},
			high: []span{
				{"01-01", "01-04"}, {"02-04", "02-06"}, {"04-29", "05-09"},
				{"07-16", "08-23"}, {"10-01", "10-07"}, {"12-20", "12-31"},
			},
		},
		2028: {
			low:   []span{{"01-05", "01-24"}},
			high:  []span{{"01-01", "01-04"}, {"01-25", "01-31"}},
			until: "03-31",
		},
	},
	"longhaul": {
		2026: {
			low: []span{{"01-06", "02-28"}, {"04-01", "04-28"}},
			high: []span{
				{"01-01", "01-03"}, {"04-29", "05-09"},
				{"07-16", "08-23"}, {"12-19", "12-31"},
			},
		},
		2027: {
			low: []span{{"01-06", "02-28"}},
			high: []span{
				{"01-01", "01-03"}, {"04-29", "05-09"},
				{"07-16", "08-22"}, {"12-20", "12-31"},
			},
		},
		2028: {
			low:   []span{{"01-06", "02-29"}},
			high:  []span{{"01-01", "01-03"}},
			until: "03-31",
		},
	},
	"resort": {
		2026: {
			low: []span{
				{"01-07", "02-28"}, {"04-01", "04-27"},
				{"05-10", "05-31"}, {"07-01", "07-15"},
			},
			high: []span{
				{"01-01", "01-03"}, {"04-28", "05-09"},
				{"07-16", "08-23"}, {"12-19", "12-31"},
			},
		},
		2027: {
			low: []span{
				{"01-06", "02-28"}, {"04-01", "04-28"}, {"05-10", "05-31"},
			},
			high: []span{
				{"01-01", "01-03"}, {"04-29", "05-09"},
				{"07-16", "08-22"}, {"12-20", "12-31"},
			},
		},
		2028: {
			low:   []span{{"01-06", "02-29"}},
			high:  []span{{"01-01", "01-03"}},
			until: "03-31",
		},
	},
}

// season returns the season of a flight on day between Japan and zone,
// false past the published charts.
func season(zone string, day time.Time) (int, bool) {
	year, ok := seasons[zoneGroup[zone]][day.Year()]
	md := day.Format("01-02")
	if !ok || (year.until != "" && md > year.until) {
		return 0, false
	}
	if inSpans(year.low, md) {
		return low, true
	}
	if inSpans(year.high, md) {
		return high, true
	}
	return regular, true
}

// inSpans reports whether md falls in one of spans.
func inSpans(spans []span, md string) bool {
	for _, s := range spans {
		if md >= s[0] && md <= s[1] {
			return true
		}
	}
	return false
}

// chartMiles returns the one-way miles of cabin on day between Japan and
// zone, with a note naming the season and the chart. The miles are 0 when
// the chart or the season is not known.
func chartMiles(zone string, cabin award.Cabin, day time.Time) (int, string) {
	s, ok := season(zone, day)
	row, known := oneWayMiles[zone][cabin]
	if !ok || !known {
		return 0, "price not known for this date; see " + chartURL
	}
	return row[s], seasonNames[s] + " (" + chartURL + ")"
}
