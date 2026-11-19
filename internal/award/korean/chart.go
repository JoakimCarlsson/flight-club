package korean

import (
	"time"

	"github.com/joakimcarlsson/flight-club/internal/award"
)

// chartURL is the SKYPASS redemption table the miles come from.
const chartURL = "https://www.koreanair.com/contents/skypass/use-miles/" +
	"award-ticket/redemption"

// region is a region of the SKYPASS redemption table for Korean Air
// flights.
type region int

// The regions of the redemption table. korea is Korea itself; a route
// between Korea and another region is priced by that region, and a route
// within Korea as domestic.
const (
	unknown region = iota
	korea
	northeastAsia
	southeastAsia
	southwestAsia
	longHaul
)

// offPeak is the one-way off-peak miles of a Korean Air award between
// Korea and each region, per cabin. A cabin missing from a region has no
// award there.
var offPeak = map[region]map[award.Cabin]int{
	korea: {
		award.Economy:  5000,
		award.Business: 6000,
	},
	northeastAsia: {
		award.Economy:  15000,
		award.Business: 22500,
		award.First:    32500,
	},
	southeastAsia: {
		award.Economy:  20000,
		award.Business: 35000,
		award.First:    45000,
	},
	southwestAsia: {
		award.Economy:  25000,
		award.Business: 45000,
		award.First:    57500,
	},
	longHaul: {
		award.Economy:  35000,
		award.Business: 62500,
		award.First:    80000,
	},
}

// airport is what an award needs to know about an airport: its time zone
// and its region of the redemption table.
type airport struct {
	zone   string
	region region
}

// airports are the airports of Korean Air's network.
var airports = map[string]airport{
	"ICN": {"Asia/Seoul", korea},
	"GMP": {"Asia/Seoul", korea},
	"PUS": {"Asia/Seoul", korea},
	"CJU": {"Asia/Seoul", korea},
	"TAE": {"Asia/Seoul", korea},
	"KWJ": {"Asia/Seoul", korea},
	"CJJ": {"Asia/Seoul", korea},
	"RSU": {"Asia/Seoul", korea},
	"USN": {"Asia/Seoul", korea},
	"KPO": {"Asia/Seoul", korea},
	"MWX": {"Asia/Seoul", korea},
	"HIN": {"Asia/Seoul", korea},
	"YNY": {"Asia/Seoul", korea},
	"WJU": {"Asia/Seoul", korea},
	"NRT": {"Asia/Tokyo", northeastAsia},
	"HND": {"Asia/Tokyo", northeastAsia},
	"KIX": {"Asia/Tokyo", northeastAsia},
	"NGO": {"Asia/Tokyo", northeastAsia},
	"FUK": {"Asia/Tokyo", northeastAsia},
	"CTS": {"Asia/Tokyo", northeastAsia},
	"OKA": {"Asia/Tokyo", northeastAsia},
	"KOJ": {"Asia/Tokyo", northeastAsia},
	"KMQ": {"Asia/Tokyo", northeastAsia},
	"OIT": {"Asia/Tokyo", northeastAsia},
	"AOJ": {"Asia/Tokyo", northeastAsia},
	"NGS": {"Asia/Tokyo", northeastAsia},
	"KIJ": {"Asia/Tokyo", northeastAsia},
	"PEK": {"Asia/Shanghai", northeastAsia},
	"PKX": {"Asia/Shanghai", northeastAsia},
	"PVG": {"Asia/Shanghai", northeastAsia},
	"SHA": {"Asia/Shanghai", northeastAsia},
	"CAN": {"Asia/Shanghai", northeastAsia},
	"SZX": {"Asia/Shanghai", northeastAsia},
	"TAO": {"Asia/Shanghai", northeastAsia},
	"SHE": {"Asia/Shanghai", northeastAsia},
	"DLC": {"Asia/Shanghai", northeastAsia},
	"TSN": {"Asia/Shanghai", northeastAsia},
	"CGO": {"Asia/Shanghai", northeastAsia},
	"CSX": {"Asia/Shanghai", northeastAsia},
	"XMN": {"Asia/Shanghai", northeastAsia},
	"KMG": {"Asia/Shanghai", northeastAsia},
	"WUH": {"Asia/Shanghai", northeastAsia},
	"YNJ": {"Asia/Shanghai", northeastAsia},
	"HGH": {"Asia/Shanghai", northeastAsia},
	"NKG": {"Asia/Shanghai", northeastAsia},
	"CKG": {"Asia/Shanghai", northeastAsia},
	"TFU": {"Asia/Shanghai", northeastAsia},
	"CTU": {"Asia/Shanghai", northeastAsia},
	"XIY": {"Asia/Shanghai", northeastAsia},
	"YNT": {"Asia/Shanghai", northeastAsia},
	"WEH": {"Asia/Shanghai", northeastAsia},
	"MDG": {"Asia/Shanghai", northeastAsia},
	"HKG": {"Asia/Hong_Kong", northeastAsia},
	"MFM": {"Asia/Macau", northeastAsia},
	"TPE": {"Asia/Taipei", northeastAsia},
	"KHH": {"Asia/Taipei", northeastAsia},
	"UBN": {"Asia/Ulaanbaatar", northeastAsia},
	"VVO": {"Asia/Vladivostok", northeastAsia},
	"BKK": {"Asia/Bangkok", southeastAsia},
	"HKT": {"Asia/Bangkok", southeastAsia},
	"CNX": {"Asia/Bangkok", southeastAsia},
	"SGN": {"Asia/Ho_Chi_Minh", southeastAsia},
	"HAN": {"Asia/Ho_Chi_Minh", southeastAsia},
	"DAD": {"Asia/Ho_Chi_Minh", southeastAsia},
	"CXR": {"Asia/Ho_Chi_Minh", southeastAsia},
	"PQC": {"Asia/Ho_Chi_Minh", southeastAsia},
	"MNL": {"Asia/Manila", southeastAsia},
	"CEB": {"Asia/Manila", southeastAsia},
	"CRK": {"Asia/Manila", southeastAsia},
	"SIN": {"Asia/Singapore", southeastAsia},
	"KUL": {"Asia/Kuala_Lumpur", southeastAsia},
	"BKI": {"Asia/Kuching", southeastAsia},
	"CGK": {"Asia/Jakarta", southeastAsia},
	"DPS": {"Asia/Makassar", southeastAsia},
	"PNH": {"Asia/Phnom_Penh", southeastAsia},
	"RGN": {"Asia/Yangon", southeastAsia},
	"VTE": {"Asia/Vientiane", southeastAsia},
	"GUM": {"Pacific/Guam", southeastAsia},
	"SPN": {"Pacific/Saipan", southeastAsia},
	"ROR": {"Pacific/Palau", southeastAsia},
	"DEL": {"Asia/Kolkata", southwestAsia},
	"BOM": {"Asia/Kolkata", southwestAsia},
	"MLE": {"Indian/Maldives", southwestAsia},
	"KTM": {"Asia/Kathmandu", southwestAsia},
	"CMB": {"Asia/Colombo", southwestAsia},
	"TAS": {"Asia/Tashkent", southwestAsia},
	"NVI": {"Asia/Samarkand", southwestAsia},
	"ALA": {"Asia/Almaty", southwestAsia},
	"LAX": {"America/Los_Angeles", longHaul},
	"SFO": {"America/Los_Angeles", longHaul},
	"SEA": {"America/Los_Angeles", longHaul},
	"LAS": {"America/Los_Angeles", longHaul},
	"YVR": {"America/Vancouver", longHaul},
	"YYZ": {"America/Toronto", longHaul},
	"JFK": {"America/New_York", longHaul},
	"EWR": {"America/New_York", longHaul},
	"BOS": {"America/New_York", longHaul},
	"IAD": {"America/New_York", longHaul},
	"ATL": {"America/New_York", longHaul},
	"DTW": {"America/Detroit", longHaul},
	"ORD": {"America/Chicago", longHaul},
	"DFW": {"America/Chicago", longHaul},
	"MSP": {"America/Chicago", longHaul},
	"SLC": {"America/Denver", longHaul},
	"ANC": {"America/Anchorage", longHaul},
	"HNL": {"Pacific/Honolulu", longHaul},
	"LHR": {"Europe/London", longHaul},
	"CDG": {"Europe/Paris", longHaul},
	"FRA": {"Europe/Berlin", longHaul},
	"AMS": {"Europe/Amsterdam", longHaul},
	"FCO": {"Europe/Rome", longHaul},
	"MXP": {"Europe/Rome", longHaul},
	"MAD": {"Europe/Madrid", longHaul},
	"BCN": {"Europe/Madrid", longHaul},
	"LIS": {"Europe/Lisbon", longHaul},
	"PRG": {"Europe/Prague", longHaul},
	"VIE": {"Europe/Vienna", longHaul},
	"ZRH": {"Europe/Zurich", longHaul},
	"BUD": {"Europe/Budapest", longHaul},
	"ZAG": {"Europe/Zagreb", longHaul},
	"CPH": {"Europe/Copenhagen", longHaul},
	"IST": {"Europe/Istanbul", longHaul},
	"SVO": {"Europe/Moscow", longHaul},
	"DXB": {"Asia/Dubai", longHaul},
	"TLV": {"Asia/Jerusalem", longHaul},
	"RUH": {"Asia/Riyadh", longHaul},
	"SYD": {"Australia/Sydney", longHaul},
	"BNE": {"Australia/Brisbane", longHaul},
	"AKL": {"Pacific/Auckland", longHaul},
	"NAN": {"Pacific/Fiji", longHaul},
}

// cityAirports are the airports of the city codes that name more than one
// airport, or that differ from the airport code.
var cityAirports = map[string][]string{
	"SEL": {"ICN", "GMP"},
	"TYO": {"NRT", "HND"},
	"OSA": {"KIX"},
	"SPK": {"CTS"},
	"BJS": {"PEK", "PKX"},
	"SHA": {"PVG", "SHA"},
	"JKT": {"CGK"},
	"NYC": {"JFK", "EWR"},
	"WAS": {"IAD"},
	"CHI": {"ORD"},
	"YTO": {"YYZ"},
	"LON": {"LHR"},
	"PAR": {"CDG"},
	"ROM": {"FCO"},
	"MIL": {"MXP"},
}

// airportsOf returns the airports to search for code: those of a city
// code it knows, or code itself.
func airportsOf(code string) []string {
	if a, ok := cityAirports[code]; ok {
		return a
	}
	return []string{code}
}

// location returns the time zone of the airport code, or UTC when the
// airport is unknown or its zone cannot be loaded.
func location(code string) *time.Location {
	a, ok := airports[code]
	if !ok {
		return time.UTC
	}
	loc, err := time.LoadLocation(a.zone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// routeRegion returns the region of the redemption table that prices the
// route from to to: the region of the end that is not in Korea, or korea
// for a domestic route. It returns unknown when an airport is not known
// or neither end is in Korea.
func routeRegion(from, to string) region {
	a, okA := airports[from]
	b, okB := airports[to]
	switch {
	case !okA || !okB:
		return unknown
	case a.region == korea:
		return b.region
	case b.region == korea:
		return a.region
	}
	return unknown
}

// chartMiles returns the one-way off-peak miles of an award in cabin on
// the route from to to, with a note on where they come
// from. Miles are 0, with a note saying so, when the route is not in the
// redemption table. It returns false when the table has no award in cabin
// on the route.
func chartMiles(
	from, to string,
	cabin award.Cabin,
) (int, string, bool) {
	r := routeRegion(from, to)
	if r == unknown {
		return 0, "Miles unknown: route not in the redemption table " +
			"(" + chartURL + ")", true
	}
	miles, ok := offPeak[r][cabin]
	if !ok {
		return 0, "", false
	}
	return miles, "Off-peak miles from the SKYPASS redemption table; " +
		"peak dates cost about 50% more", true
}
