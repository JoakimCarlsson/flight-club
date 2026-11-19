package copa

import (
	"regexp"
	"strconv"
	"time"
)

// localLayout is the layout of a date and time joined by a space.
const localLayout = "2006-01-02 15:04"

// segmentTimes are a segment's departure and arrival with their offsets.
type segmentTimes struct {
	departure time.Time
	arrival   time.Time
}

// journeyPattern reads an ISO 8601 duration such as "PT03H06M".
var journeyPattern = regexp.MustCompile(
	`^P(?:(\d+)D)?T?(?:(\d+)H)?(?:(\d+)M)?`,
)

// tripTimes gives every segment of s its departure and arrival with the
// airports' offsets. An airport missing from zones takes the offset that
// makes the journey time come out right when it is the destination, or
// else the offset of the airport before it. It returns false when a time
// cannot be read.
func tripTimes(s solution) ([]segmentTimes, bool) {
	out := make([]segmentTimes, len(s.Flights))
	prev, havePrev := 0, false
	for i, f := range s.Flights {
		dep, err := time.Parse(
			localLayout, f.Departure.FlightDate+" "+f.Departure.FlightTime,
		)
		if err != nil {
			return nil, false
		}
		arr, err := time.Parse(
			localLayout, f.Arrival.FlightDate+" "+f.Arrival.FlightTime,
		)
		if err != nil {
			return nil, false
		}
		off, ok := zoneOffset(f.Departure.AirportCode, dep)
		if !ok && havePrev {
			off = prev
		}
		dep = inOffset(dep, f.Departure.AirportCode, off)
		aoff, ok := zoneOffset(f.Arrival.AirportCode, arr)
		if !ok {
			aoff = guessArrivalOffset(s, out, i, dep, arr)
		}
		arr = inOffset(arr, f.Arrival.AirportCode, aoff)
		out[i] = segmentTimes{departure: dep, arrival: arr}
		prev, havePrev = aoff, true
	}
	return out, true
}

// guessArrivalOffset is the offset, in seconds east of UTC, of the unknown
// arrival airport of segment i of s, which lands at local time arr after
// leaving at dep. For the last segment it is the offset that makes the
// journey last as long as s says, rounded to a quarter hour; otherwise, or
// when that is out of range, it is the departure's offset.
func guessArrivalOffset(
	s solution,
	done []segmentTimes,
	i int,
	dep, arr time.Time,
) int {
	_, depOff := dep.Zone()
	total, ok := journey(s.JourneyTime)
	if i != len(s.Flights)-1 || !ok {
		return depOff
	}
	start := dep
	if i > 0 {
		start = done[0].departure
	}
	landUTC := start.Add(total).UTC()
	wall := time.Date(arr.Year(), arr.Month(), arr.Day(), arr.Hour(),
		arr.Minute(), 0, 0, time.UTC)
	quarter := 15 * time.Minute
	rounded := int(wall.Sub(landUTC).Round(quarter).Seconds())
	if rounded < -12*3600 || rounded > 14*3600 {
		return depOff
	}
	return rounded
}

// journey reads an ISO 8601 duration such as "PT03H06M", and returns false
// when s is not one.
func journey(s string) (time.Duration, bool) {
	m := journeyPattern.FindStringSubmatch(s)
	if m == nil || m[0] == "P" || m[0] == "PT" {
		return 0, false
	}
	var d time.Duration
	for i, unit := range []time.Duration{24 * time.Hour, time.Hour,
		time.Minute} {
		if m[i+1] == "" {
			continue
		}
		v, err := strconv.Atoi(m[i+1])
		if err != nil {
			return 0, false
		}
		d += time.Duration(v) * unit
	}
	return d, true
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

// zones are the IANA time zones of the airports Copa flies to and of the
// main connecting points of its partners in the Americas.
var zones = map[string]string{
	"PTY": "America/Panama", "DAV": "America/Panama",
	"BLB": "America/Panama", "SJO": "America/Costa_Rica",
	"LIR": "America/Costa_Rica", "MGA": "America/Managua",
	"SAL": "America/El_Salvador", "GUA": "America/Guatemala",
	"FRS": "America/Guatemala", "TGU": "America/Tegucigalpa",
	"XPL": "America/Tegucigalpa", "SAP": "America/Tegucigalpa",
	"RTB": "America/Tegucigalpa", "BZE": "America/Belize",
	"MEX": "America/Mexico_City", "NLU": "America/Mexico_City",
	"GDL": "America/Mexico_City", "MTY": "America/Monterrey",
	"CUN": "America/Cancun", "PVR": "America/Mexico_City",
	"SJD": "America/Mazatlan", "HAV": "America/Havana",
	"SCU": "America/Havana", "VRA": "America/Havana",
	"HOG": "America/Havana", "SDQ": "America/Santo_Domingo",
	"STI": "America/Santo_Domingo", "PUJ": "America/Santo_Domingo",
	"PAP": "America/Port-au-Prince", "KIN": "America/Jamaica",
	"MBJ": "America/Jamaica", "SJU": "America/Puerto_Rico",
	"NAS": "America/Nassau", "GCM": "America/Cayman",
	"AUA": "America/Aruba", "CUR": "America/Curacao",
	"SXM": "America/Lower_Princes", "POS": "America/Port_of_Spain",
	"BGI": "America/Barbados", "GEO": "America/Guyana",
	"PBM": "America/Paramaribo", "BOG": "America/Bogota",
	"MDE": "America/Bogota", "CLO": "America/Bogota",
	"CTG": "America/Bogota", "BAQ": "America/Bogota",
	"BGA": "America/Bogota", "PEI": "America/Bogota",
	"SMR": "America/Bogota", "ADZ": "America/Bogota",
	"CUC": "America/Bogota", "CCS": "America/Caracas",
	"MAR": "America/Caracas", "VLN": "America/Caracas",
	"UIO": "America/Guayaquil", "GYE": "America/Guayaquil",
	"LIM": "America/Lima", "CUZ": "America/Lima",
	"VVI": "America/La_Paz", "LPB": "America/La_Paz",
	"ASU": "America/Asuncion", "MVD": "America/Montevideo",
	"EZE": "America/Argentina/Buenos_Aires",
	"AEP": "America/Argentina/Buenos_Aires",
	"COR": "America/Argentina/Cordoba",
	"MDZ": "America/Argentina/Mendoza",
	"ROS": "America/Argentina/Cordoba",
	"SCL": "America/Santiago", "GRU": "America/Sao_Paulo",
	"GIG": "America/Sao_Paulo", "BSB": "America/Sao_Paulo",
	"CNF": "America/Sao_Paulo", "POA": "America/Sao_Paulo",
	"FLN": "America/Sao_Paulo", "SSA": "America/Bahia",
	"REC": "America/Recife", "FOR": "America/Fortaleza",
	"MAO": "America/Manaus", "BEL": "America/Belem",
	"MIA": "America/New_York", "FLL": "America/New_York",
	"MCO": "America/New_York", "TPA": "America/New_York",
	"JFK": "America/New_York", "EWR": "America/New_York",
	"LGA": "America/New_York", "BOS": "America/New_York",
	"IAD": "America/New_York", "DCA": "America/New_York",
	"BWI": "America/New_York", "PHL": "America/New_York",
	"ATL": "America/New_York", "CLT": "America/New_York",
	"RDU": "America/New_York", "BDL": "America/New_York",
	"BTV": "America/New_York", "DTW": "America/Detroit",
	"ORD": "America/Chicago", "IAH": "America/Chicago",
	"DFW": "America/Chicago", "AUS": "America/Chicago",
	"MSY": "America/Chicago", "MSP": "America/Chicago",
	"BNA": "America/Chicago", "MCI": "America/Chicago",
	"DEN": "America/Denver", "SLC": "America/Denver",
	"PHX": "America/Phoenix", "LAS": "America/Los_Angeles",
	"LAX": "America/Los_Angeles", "SFO": "America/Los_Angeles",
	"SAN": "America/Los_Angeles", "SEA": "America/Los_Angeles",
	"YYZ": "America/Toronto", "YUL": "America/Toronto",
	"YVR": "America/Vancouver", "YYC": "America/Edmonton",
	"LHR": "Europe/London", "FRA": "Europe/Berlin",
	"MUC": "Europe/Berlin", "MAD": "Europe/Madrid",
	"BCN": "Europe/Madrid", "LIS": "Europe/Lisbon",
	"CDG": "Europe/Paris", "AMS": "Europe/Amsterdam",
	"ZRH": "Europe/Zurich", "IST": "Europe/Istanbul",
}
