package delta

import (
	"time"
)

// localLayouts are the layouts of the answer's offset-less local times.
var localLayouts = []string{
	"2006-01-02T15:04:05",
	"2006-01-02T15:04",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
}

// segmentTimes are a segment's departure and arrival with their offsets.
type segmentTimes struct {
	departure time.Time
	arrival   time.Time
}

// parseLocal reads a local time of the answer. It reports whether s has an
// offset of its own, and returns false when s cannot be read.
func parseLocal(s string) (time.Time, bool, bool) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true, true
	}
	for _, l := range localLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, false, true
		}
	}
	return time.Time{}, false, false
}

// tripTimes gives every segment of t its departure and arrival with the
// airports' offsets. An airport missing from zones takes the offset that
// makes the trip's total time come out right when it is the destination,
// or else the offset of the airport before it. It returns false when a
// time cannot be read or no offset can be found for the origin.
func tripTimes(t trip) ([]segmentTimes, bool) {
	n := len(t.FlightSegment)
	out := make([]segmentTimes, n)
	prev, havePrev := 0, false
	for i, s := range t.FlightSegment {
		dep, depHas, ok := parseLocal(s.ScheduledDepartureLocalTs)
		if !ok {
			return nil, false
		}
		arr, arrHas, ok := parseLocal(s.ScheduledArrivalLocalTs)
		if !ok {
			return nil, false
		}
		if !depHas {
			off, ok := zoneOffset(s.OriginAirportCode, dep)
			switch {
			case ok:
			case havePrev:
				off = prev
			default:
				off = 0
			}
			dep = inOffset(dep, s.OriginAirportCode, off)
		}
		if !arrHas {
			off, ok := zoneOffset(s.DestinationAirportCode, arr)
			if !ok {
				off = guessArrivalOffset(t, out, i, dep, arr)
			}
			arr = inOffset(arr, s.DestinationAirportCode, off)
		}
		out[i] = segmentTimes{departure: dep, arrival: arr}
		_, prev = arr.Zone()
		havePrev = true
	}
	return out, true
}

// guessArrivalOffset is the offset, in seconds east of UTC, of the
// unknown arrival airport of segment i, which lands at local time arr after
// leaving at dep. For the last segment of a trip with a total time, it is
// the offset that makes the trip last that long, rounded to a quarter hour;
// otherwise it is the departure's offset.
func guessArrivalOffset(
	t trip,
	done []segmentTimes,
	i int,
	dep, arr time.Time,
) int {
	_, depOff := dep.Zone()
	if i != len(t.FlightSegment)-1 || t.TotalTripTime == nil {
		return depOff
	}
	start := dep
	if i > 0 {
		start = done[0].departure
	}
	total := time.Duration(
		t.TotalTripTime.HourCnt*60+t.TotalTripTime.MinuteCnt,
	) * time.Minute
	landUTC := start.Add(total).UTC()
	wall := time.Date(arr.Year(), arr.Month(), arr.Day(), arr.Hour(),
		arr.Minute(), arr.Second(), 0, time.UTC)
	off := wall.Sub(landUTC).Seconds()
	quarter := 15 * 60.0
	rounded := int(off/quarter+0.5*sign(off)) * int(quarter)
	if rounded < -12*3600 || rounded > 14*3600 {
		return depOff
	}
	return rounded
}

// sign is 1 for a positive v, -1 for a negative one and 0 for zero.
func sign(v float64) float64 {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}

// inOffset reads the offset-less local time t as a time at airport, whose
// offset is offset seconds east of UTC.
func inOffset(t time.Time, airport string, offset int) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(),
		t.Second(), 0, time.FixedZone(airport, offset))
}

// zoneOffset returns the UTC offset in seconds of airport around local time
// t, and false when the airport's zone is not known or cannot be loaded.
func zoneOffset(airport string, t time.Time) (int, bool) {
	name, ok := zones[airport]
	if !ok {
		return 0, false
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return 0, false
	}
	at := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(),
		t.Second(), 0, loc)
	_, off := at.Zone()
	return off, true
}

// zones are the IANA time zones of Delta's hubs, focus cities and main
// gateways, and of its SkyTeam partners' hubs.
var zones = map[string]string{
	"ATL": "America/New_York", "JFK": "America/New_York",
	"LGA": "America/New_York", "EWR": "America/New_York",
	"BOS": "America/New_York", "DTW": "America/Detroit",
	"DCA": "America/New_York", "IAD": "America/New_York",
	"BWI": "America/New_York", "PHL": "America/New_York",
	"PIT": "America/New_York", "CLT": "America/New_York",
	"RDU": "America/New_York", "MCO": "America/New_York",
	"TPA": "America/New_York", "MIA": "America/New_York",
	"FLL": "America/New_York", "PBI": "America/New_York",
	"RSW": "America/New_York", "JAX": "America/New_York",
	"CHS": "America/New_York", "SAV": "America/New_York",
	"CVG": "America/New_York", "CMH": "America/New_York",
	"CLE": "America/New_York", "IND": "America/Indiana/Indianapolis",
	"BNA": "America/Chicago", "MEM": "America/Chicago",
	"MSY": "America/Chicago", "ORD": "America/Chicago",
	"MDW": "America/Chicago", "MSP": "America/Chicago",
	"STL": "America/Chicago", "MCI": "America/Chicago",
	"DFW": "America/Chicago", "DAL": "America/Chicago",
	"IAH": "America/Chicago", "HOU": "America/Chicago",
	"AUS": "America/Chicago", "SAT": "America/Chicago",
	"MKE": "America/Chicago", "DEN": "America/Denver",
	"SLC": "America/Denver", "PHX": "America/Phoenix",
	"LAS": "America/Los_Angeles", "LAX": "America/Los_Angeles",
	"SAN": "America/Los_Angeles", "SFO": "America/Los_Angeles",
	"SJC": "America/Los_Angeles", "OAK": "America/Los_Angeles",
	"SMF": "America/Los_Angeles", "SEA": "America/Los_Angeles",
	"PDX": "America/Los_Angeles", "ANC": "America/Anchorage",
	"HNL": "Pacific/Honolulu", "OGG": "Pacific/Honolulu",
	"KOA": "Pacific/Honolulu", "LIH": "Pacific/Honolulu",
	"SJU": "America/Puerto_Rico", "YYZ": "America/Toronto",
	"YUL": "America/Toronto", "YVR": "America/Vancouver",
	"YYC": "America/Edmonton", "MEX": "America/Mexico_City",
	"CUN": "America/Cancun", "GDL": "America/Mexico_City",
	"MTY": "America/Monterrey", "PVR": "America/Mexico_City",
	"SJD": "America/Mazatlan", "SJO": "America/Costa_Rica",
	"LIR": "America/Costa_Rica", "PTY": "America/Panama",
	"BOG": "America/Bogota", "LIM": "America/Lima",
	"GRU": "America/Sao_Paulo", "GIG": "America/Sao_Paulo",
	"EZE": "America/Argentina/Buenos_Aires",
	"SCL": "America/Santiago", "UIO": "America/Guayaquil",
	"NAS": "America/Nassau", "MBJ": "America/Jamaica",
	"PUJ": "America/Santo_Domingo", "SDQ": "America/Santo_Domingo",
	"AUA": "America/Aruba", "STT": "America/St_Thomas",
	"LHR": "Europe/London", "LGW": "Europe/London",
	"MAN": "Europe/London", "EDI": "Europe/London",
	"DUB": "Europe/Dublin", "SNN": "Europe/Dublin",
	"CDG": "Europe/Paris", "ORY": "Europe/Paris",
	"NCE": "Europe/Paris", "LYS": "Europe/Paris",
	"AMS": "Europe/Amsterdam", "BRU": "Europe/Brussels",
	"FRA": "Europe/Berlin", "MUC": "Europe/Berlin",
	"BER": "Europe/Berlin", "DUS": "Europe/Berlin",
	"HAM": "Europe/Berlin", "STR": "Europe/Berlin",
	"ZRH": "Europe/Zurich", "GVA": "Europe/Zurich",
	"VIE": "Europe/Vienna", "PRG": "Europe/Prague",
	"BUD": "Europe/Budapest", "WAW": "Europe/Warsaw",
	"CPH": "Europe/Copenhagen", "ARN": "Europe/Stockholm",
	"OSL": "Europe/Oslo", "HEL": "Europe/Helsinki",
	"KEF": "Atlantic/Reykjavik", "MAD": "Europe/Madrid",
	"BCN": "Europe/Madrid", "LIS": "Europe/Lisbon",
	"FCO": "Europe/Rome", "MXP": "Europe/Rome",
	"VCE": "Europe/Rome", "NAP": "Europe/Rome",
	"ATH": "Europe/Athens", "IST": "Europe/Istanbul",
	"TLV": "Asia/Jerusalem", "DXB": "Asia/Dubai",
	"RUH": "Asia/Riyadh", "JED": "Asia/Riyadh",
	"CAI": "Africa/Cairo", "CMN": "Africa/Casablanca",
	"LOS": "Africa/Lagos", "ACC": "Africa/Accra",
	"DSS": "Africa/Dakar", "JNB": "Africa/Johannesburg",
	"CPT": "Africa/Johannesburg", "NBO": "Africa/Nairobi",
	"DEL": "Asia/Kolkata", "BOM": "Asia/Kolkata",
	"HND": "Asia/Tokyo", "NRT": "Asia/Tokyo",
	"KIX": "Asia/Tokyo", "ICN": "Asia/Seoul",
	"PVG": "Asia/Shanghai", "PEK": "Asia/Shanghai",
	"PKX": "Asia/Shanghai", "CAN": "Asia/Shanghai",
	"TPE": "Asia/Taipei", "HKG": "Asia/Hong_Kong",
	"MNL": "Asia/Manila", "SGN": "Asia/Ho_Chi_Minh",
	"HAN": "Asia/Bangkok", "BKK": "Asia/Bangkok",
	"SIN": "Asia/Singapore", "KUL": "Asia/Kuala_Lumpur",
	"CGK": "Asia/Jakarta", "SYD": "Australia/Sydney",
	"MEL": "Australia/Melbourne", "BNE": "Australia/Brisbane",
	"AKL": "Pacific/Auckland", "PPT": "Pacific/Tahiti",
}
