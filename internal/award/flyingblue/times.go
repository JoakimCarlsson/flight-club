package flyingblue

import (
	"strings"
	"time"
)

// segmentTimes are a segment's departure and arrival with their offsets.
type segmentTimes struct {
	departure time.Time
	arrival   time.Time
}

// localLayouts are the layouts the endpoint's times come in, with an offset
// first and the offset-less local time last.
var localLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02T15:04",
}

// parseTime reads s, reporting whether it carried its own offset. It
// returns false as its last result when s cannot be read.
func parseTime(s string) (time.Time, bool, bool) {
	for i, layout := range localLayouts {
		if t, err := time.Parse(layout, strings.TrimSpace(s)); err == nil {
			return t, i == 0, true
		}
	}
	return time.Time{}, false, false
}

// itineraryTimes gives every segment of segs its departure and arrival with
// the airports' offsets. Times that carry an offset keep it; an offset-less
// time takes its airport's offset from zones, or else one derived from the
// other end of the segment and its flying time. It returns false when a
// time cannot be parsed or no offset can be found for one.
func itineraryTimes(segs []segment) ([]segmentTimes, bool) {
	out := make([]segmentTimes, len(segs))
	for i, s := range segs {
		dep, depOK := placeTime(s.DepartureDateTime, s.Origin.Code)
		arr, arrOK := placeTime(s.ArrivalDateTime, s.Destination.Code)
		fly := time.Duration(s.Duration) * time.Minute
		switch {
		case depOK && arrOK:
		case depOK && fly > 0:
			arr, arrOK = derive(s.ArrivalDateTime, dep.Add(fly),
				s.Destination.Code)
		case arrOK && fly > 0:
			dep, depOK = derive(s.DepartureDateTime, arr.Add(-fly),
				s.Origin.Code)
		}
		if !depOK || !arrOK {
			return nil, false
		}
		out[i] = segmentTimes{departure: dep, arrival: arr}
	}
	return out, true
}

// placeTime reads s as a time at airport: in its own offset when it has
// one, else in the airport's zone. It returns false when s cannot be read
// or has no offset and the airport's zone is unknown.
func placeTime(s, airport string) (time.Time, bool) {
	t, zoned, ok := parseTime(s)
	if !ok {
		return time.Time{}, false
	}
	if zoned {
		return t, true
	}
	name, ok := zones[airport]
	if !ok {
		return time.Time{}, false
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.Time{}, false
	}
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(),
		t.Second(), 0, loc), true
}

// derive reads the offset-less local time s as the instant at, at airport,
// giving it the offset between the two rounded to a quarter hour. It
// returns false when s cannot be read.
func derive(s string, at time.Time, airport string) (time.Time, bool) {
	local, _, ok := parseTime(s)
	if !ok {
		return time.Time{}, false
	}
	wall := time.Date(local.Year(), local.Month(), local.Day(),
		local.Hour(), local.Minute(), local.Second(), 0, time.UTC)
	offset := wall.Sub(at.UTC()).Round(15 * time.Minute)
	zone := time.FixedZone(airport, int(offset.Seconds()))
	return at.In(zone), true
}

// zones are the IANA time zones of the hubs and main airports Air France,
// KLM and their partners fly to.
var zones = map[string]string{
	"CDG": "Europe/Paris", "ORY": "Europe/Paris", "NCE": "Europe/Paris",
	"LYS": "Europe/Paris", "MRS": "Europe/Paris", "TLS": "Europe/Paris",
	"BOD": "Europe/Paris", "NTE": "Europe/Paris",
	"AMS": "Europe/Amsterdam", "LHR": "Europe/London",
	"LGW": "Europe/London", "MAN": "Europe/London",
	"EDI": "Europe/London", "DUB": "Europe/Dublin",
	"FRA": "Europe/Berlin", "MUC": "Europe/Berlin",
	"BER": "Europe/Berlin", "HAM": "Europe/Berlin",
	"MAD": "Europe/Madrid", "BCN": "Europe/Madrid",
	"LIS": "Europe/Lisbon", "FCO": "Europe/Rome", "MXP": "Europe/Rome",
	"VCE": "Europe/Rome", "ZRH": "Europe/Zurich", "GVA": "Europe/Zurich",
	"VIE": "Europe/Vienna", "BRU": "Europe/Brussels",
	"CPH": "Europe/Copenhagen", "ARN": "Europe/Stockholm",
	"OSL": "Europe/Oslo", "HEL": "Europe/Helsinki",
	"WAW": "Europe/Warsaw", "PRG": "Europe/Prague",
	"BUD": "Europe/Budapest", "ATH": "Europe/Athens",
	"IST": "Europe/Istanbul",
	"JFK": "America/New_York", "EWR": "America/New_York",
	"LGA": "America/New_York", "BOS": "America/New_York",
	"IAD": "America/New_York", "DCA": "America/New_York",
	"ATL": "America/New_York", "MIA": "America/New_York",
	"MCO": "America/New_York", "DTW": "America/Detroit",
	"PHL": "America/New_York", "CLT": "America/New_York",
	"RDU": "America/New_York", "ORD": "America/Chicago",
	"MSP": "America/Chicago", "DFW": "America/Chicago",
	"IAH": "America/Chicago", "AUS": "America/Chicago",
	"MSY": "America/Chicago", "DEN": "America/Denver",
	"SLC": "America/Denver", "PHX": "America/Phoenix",
	"LAX": "America/Los_Angeles", "SFO": "America/Los_Angeles",
	"SEA": "America/Los_Angeles", "SAN": "America/Los_Angeles",
	"LAS": "America/Los_Angeles", "PDX": "America/Los_Angeles",
	"YYZ": "America/Toronto", "YUL": "America/Toronto",
	"YVR": "America/Vancouver", "MEX": "America/Mexico_City",
	"CUN": "America/Cancun", "GRU": "America/Sao_Paulo",
	"GIG": "America/Sao_Paulo", "EZE": "America/Argentina/Buenos_Aires",
	"SCL": "America/Santiago", "LIM": "America/Lima",
	"BOG": "America/Bogota", "PTY": "America/Panama",
	"NRT": "Asia/Tokyo", "HND": "Asia/Tokyo", "KIX": "Asia/Tokyo",
	"ICN": "Asia/Seoul", "PVG": "Asia/Shanghai", "PEK": "Asia/Shanghai",
	"HKG": "Asia/Hong_Kong", "TPE": "Asia/Taipei",
	"SIN": "Asia/Singapore", "BKK": "Asia/Bangkok",
	"KUL": "Asia/Kuala_Lumpur", "CGK": "Asia/Jakarta",
	"DEL": "Asia/Kolkata", "BOM": "Asia/Kolkata", "BLR": "Asia/Kolkata",
	"DXB": "Asia/Dubai", "DOH": "Asia/Qatar", "TLV": "Asia/Jerusalem",
	"JNB": "Africa/Johannesburg", "CPT": "Africa/Johannesburg",
	"CAI": "Africa/Cairo", "CMN": "Africa/Casablanca",
	"DSS": "Africa/Dakar", "ABJ": "Africa/Abidjan",
	"LOS": "Africa/Lagos", "NBO": "Africa/Nairobi",
	"SYD": "Australia/Sydney", "MEL": "Australia/Melbourne",
	"PPT": "Pacific/Tahiti", "RUN": "Indian/Reunion",
	"PTP": "America/Guadeloupe", "FDF": "America/Martinique",
	"CUR": "America/Curacao", "AUA": "America/Aruba",
}
