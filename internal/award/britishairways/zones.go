package britishairways

import (
	"strconv"
	"time"

	"github.com/joakimcarlsson/flight-club/internal/award"
)

// localLayout is the layout of a local day and time as the results give
// them, with the year added.
const localLayout = "2 Jan 2006 15:04"

// segments builds the flights of itinerary r, searched for date, with
// their local times placed in their airports' offsets. An airport whose
// zone is not known takes the offset that makes the itinerary's total
// time come out right when it is the destination, or else the offset of
// the airport before it. It returns false when r has no flights or a time
// cannot be read.
func segments(r rawRow, date time.Time) ([]award.Segment, bool) {
	if len(r.Segments) == 0 {
		return nil, false
	}
	out := make([]award.Segment, len(r.Segments))
	prev := 0
	for i, s := range r.Segments {
		dep, ok := parseLocal(s.DepDate, s.DepTime, date)
		if !ok {
			return nil, false
		}
		arr, ok := parseLocal(s.ArrDate, s.ArrTime, dep)
		if !ok {
			return nil, false
		}
		depOff, ok := zoneOffset(s.From, dep)
		if !ok {
			depOff = prev
		}
		dep = inOffset(dep, s.From, depOff)
		arrOff, ok := zoneOffset(s.To, arr)
		if !ok {
			arrOff = guessOffset(r, out, i, dep, arr)
		}
		arr = inOffset(arr, s.To, arrOff)
		prev = arrOff
		op := s.Operating
		if op == "" {
			op = s.Carrier
		}
		out[i] = award.Segment{
			Flight:           s.Flight,
			MarketingCarrier: s.Carrier,
			OperatingCarrier: op,
			Origin:           s.From,
			Destination:      s.To,
			Departure:        dep,
			Arrival:          arr,
			DurationMinutes:  int(arr.Sub(dep).Minutes()),
		}
		if i > 0 {
			out[i-1].LayoverMinutes = int(dep.Sub(out[i-1].Arrival).Minutes())
		}
	}
	return out, true
}

// parseLocal reads a local day such as "20 Jan" and a time such as
// "07:00", in the year that puts it nearest near. It returns false when
// they cannot be read.
func parseLocal(day, clock string, near time.Time) (time.Time, bool) {
	var best time.Time
	found := false
	for _, y := range []int{near.Year() - 1, near.Year(), near.Year() + 1} {
		t, err := time.Parse(localLayout,
			day+" "+strconv.Itoa(y)+" "+clock)
		if err != nil {
			continue
		}
		if !found || absDur(t.Sub(near)) < absDur(best.Sub(near)) {
			best, found = t, true
		}
	}
	return best, found
}

// absDur is the size of d.
func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// guessOffset is the offset, in seconds east of UTC, of the unknown
// arrival airport of segment i, which lands at local time arr after
// leaving at dep. For the last segment of an itinerary with a total time
// it is the offset that makes the itinerary last that long, rounded to a
// quarter hour; otherwise it is the departure's offset.
func guessOffset(
	r rawRow,
	done []award.Segment,
	i int,
	dep, arr time.Time,
) int {
	_, depOff := dep.Zone()
	total := parseDuration(r.Total)
	if i != len(r.Segments)-1 || total == 0 {
		return depOff
	}
	start := dep
	if i > 0 {
		start = done[0].Departure
	}
	land := start.Add(time.Duration(total) * time.Minute).UTC()
	wall := time.Date(arr.Year(), arr.Month(), arr.Day(), arr.Hour(),
		arr.Minute(), 0, 0, time.UTC)
	const quarter = 15 * 60
	off := int(wall.Sub(land).Seconds())
	rounded := (off + quarter/2) / quarter * quarter
	if off < 0 {
		rounded = (off - quarter/2) / quarter * quarter
	}
	if rounded < -12*3600 || rounded > 14*3600 {
		return depOff
	}
	return rounded
}

// inOffset reads the offset-less local time t as a time at airport, whose
// offset is offset seconds east of UTC.
func inOffset(t time.Time, airport string, offset int) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(),
		0, 0, time.FixedZone(airport, offset))
}

// zoneOffset returns the UTC offset in seconds of airport around local
// time t, and false when the airport's zone is not known or cannot be
// loaded.
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
		0, 0, loc)
	_, off := at.Zone()
	return off, true
}

// zones are the IANA time zones of British Airways' and its oneworld
// partners' hubs, gateways and main destinations.
var zones = map[string]string{
	"LHR": "Europe/London", "LGW": "Europe/London",
	"LCY": "Europe/London", "STN": "Europe/London",
	"MAN": "Europe/London", "EDI": "Europe/London",
	"GLA": "Europe/London", "ABZ": "Europe/London",
	"BHD": "Europe/London", "NCL": "Europe/London",
	"JER": "Europe/Jersey", "INV": "Europe/London",
	"DUB": "Europe/Dublin", "CDG": "Europe/Paris",
	"ORY": "Europe/Paris", "NCE": "Europe/Paris",
	"LYS": "Europe/Paris", "AMS": "Europe/Amsterdam",
	"BRU": "Europe/Brussels", "FRA": "Europe/Berlin",
	"MUC": "Europe/Berlin", "BER": "Europe/Berlin",
	"DUS": "Europe/Berlin", "HAM": "Europe/Berlin",
	"ZRH": "Europe/Zurich", "GVA": "Europe/Zurich",
	"VIE": "Europe/Vienna", "PRG": "Europe/Prague",
	"BUD": "Europe/Budapest", "WAW": "Europe/Warsaw",
	"CPH": "Europe/Copenhagen", "ARN": "Europe/Stockholm",
	"GOT": "Europe/Stockholm", "OSL": "Europe/Oslo",
	"HEL": "Europe/Helsinki", "KEF": "Atlantic/Reykjavik",
	"MAD": "Europe/Madrid", "BCN": "Europe/Madrid",
	"AGP": "Europe/Madrid", "PMI": "Europe/Madrid",
	"BIO": "Europe/Madrid", "SVQ": "Europe/Madrid",
	"VLC": "Europe/Madrid", "LIS": "Europe/Lisbon",
	"OPO": "Europe/Lisbon", "FAO": "Europe/Lisbon",
	"FCO": "Europe/Rome", "MXP": "Europe/Rome",
	"LIN": "Europe/Rome", "VCE": "Europe/Rome",
	"NAP": "Europe/Rome", "ATH": "Europe/Athens",
	"IST": "Europe/Istanbul", "TLV": "Asia/Jerusalem",
	"AMM": "Asia/Amman", "BEY": "Asia/Beirut",
	"DOH": "Asia/Qatar", "DXB": "Asia/Dubai",
	"AUH": "Asia/Dubai", "BAH": "Asia/Bahrain",
	"KWI": "Asia/Kuwait", "MCT": "Asia/Muscat",
	"RUH": "Asia/Riyadh", "JED": "Asia/Riyadh",
	"CAI": "Africa/Cairo", "CMN": "Africa/Casablanca",
	"RAK": "Africa/Casablanca", "LOS": "Africa/Lagos",
	"ABV": "Africa/Lagos", "ACC": "Africa/Accra",
	"JNB": "Africa/Johannesburg", "CPT": "Africa/Johannesburg",
	"DUR": "Africa/Johannesburg", "NBO": "Africa/Nairobi",
	"MRU": "Indian/Mauritius", "SEZ": "Indian/Mahe",
	"MLE": "Indian/Maldives", "DEL": "Asia/Kolkata",
	"BOM": "Asia/Kolkata", "BLR": "Asia/Kolkata",
	"MAA": "Asia/Kolkata", "HYD": "Asia/Kolkata",
	"CMB": "Asia/Colombo", "KTM": "Asia/Kathmandu",
	"HND": "Asia/Tokyo", "NRT": "Asia/Tokyo",
	"KIX": "Asia/Tokyo", "ITM": "Asia/Tokyo",
	"NGO": "Asia/Tokyo", "FUK": "Asia/Tokyo",
	"CTS": "Asia/Tokyo", "OKA": "Asia/Tokyo",
	"ICN": "Asia/Seoul", "PVG": "Asia/Shanghai",
	"PEK": "Asia/Shanghai", "PKX": "Asia/Shanghai",
	"CAN": "Asia/Shanghai", "TPE": "Asia/Taipei",
	"HKG": "Asia/Hong_Kong", "MNL": "Asia/Manila",
	"SGN": "Asia/Ho_Chi_Minh", "HAN": "Asia/Bangkok",
	"BKK": "Asia/Bangkok", "HKT": "Asia/Bangkok",
	"SIN": "Asia/Singapore", "KUL": "Asia/Kuala_Lumpur",
	"CGK": "Asia/Jakarta", "DPS": "Asia/Makassar",
	"SYD": "Australia/Sydney", "MEL": "Australia/Melbourne",
	"BNE": "Australia/Brisbane", "PER": "Australia/Perth",
	"ADL": "Australia/Adelaide", "CNS": "Australia/Brisbane",
	"OOL": "Australia/Brisbane", "CBR": "Australia/Sydney",
	"AKL": "Pacific/Auckland", "CHC": "Pacific/Auckland",
	"NAN": "Pacific/Fiji", "PPT": "Pacific/Tahiti",
	"JFK": "America/New_York", "LGA": "America/New_York",
	"EWR": "America/New_York", "BOS": "America/New_York",
	"PHL": "America/New_York", "DCA": "America/New_York",
	"IAD": "America/New_York", "BWI": "America/New_York",
	"CLT": "America/New_York", "RDU": "America/New_York",
	"ATL": "America/New_York", "MIA": "America/New_York",
	"FLL": "America/New_York", "MCO": "America/New_York",
	"TPA": "America/New_York", "PBI": "America/New_York",
	"RSW": "America/New_York", "JAX": "America/New_York",
	"PIT": "America/New_York", "CLE": "America/New_York",
	"CMH": "America/New_York", "CVG": "America/New_York",
	"DTW": "America/Detroit", "IND": "America/Indiana/Indianapolis",
	"ORD": "America/Chicago", "MDW": "America/Chicago",
	"DFW": "America/Chicago", "DAL": "America/Chicago",
	"IAH": "America/Chicago", "HOU": "America/Chicago",
	"AUS": "America/Chicago", "SAT": "America/Chicago",
	"MSY": "America/Chicago", "BNA": "America/Chicago",
	"MEM": "America/Chicago", "STL": "America/Chicago",
	"MCI": "America/Chicago", "MSP": "America/Chicago",
	"MKE": "America/Chicago", "OKC": "America/Chicago",
	"TUL": "America/Chicago", "DEN": "America/Denver",
	"SLC": "America/Denver", "ABQ": "America/Denver",
	"PHX": "America/Phoenix", "TUS": "America/Phoenix",
	"LAS": "America/Los_Angeles", "LAX": "America/Los_Angeles",
	"SAN": "America/Los_Angeles", "SFO": "America/Los_Angeles",
	"SJC": "America/Los_Angeles", "OAK": "America/Los_Angeles",
	"SMF": "America/Los_Angeles", "SEA": "America/Los_Angeles",
	"PDX": "America/Los_Angeles", "ANC": "America/Anchorage",
	"HNL": "Pacific/Honolulu", "OGG": "Pacific/Honolulu",
	"KOA": "Pacific/Honolulu", "LIH": "Pacific/Honolulu",
	"YYZ": "America/Toronto", "YUL": "America/Toronto",
	"YOW": "America/Toronto", "YVR": "America/Vancouver",
	"YYC": "America/Edmonton", "YEG": "America/Edmonton",
	"MEX": "America/Mexico_City", "CUN": "America/Cancun",
	"GDL": "America/Mexico_City", "SJD": "America/Mazatlan",
	"SJO": "America/Costa_Rica", "PTY": "America/Panama",
	"BOG": "America/Bogota", "MDE": "America/Bogota",
	"LIM": "America/Lima", "UIO": "America/Guayaquil",
	"GRU": "America/Sao_Paulo", "GIG": "America/Sao_Paulo",
	"EZE": "America/Argentina/Buenos_Aires",
	"SCL": "America/Santiago", "MVD": "America/Montevideo",
	"NAS": "America/Nassau", "MBJ": "America/Jamaica",
	"KIN": "America/Jamaica", "BGI": "America/Barbados",
	"ANU": "America/Antigua", "UVF": "America/St_Lucia",
	"POS": "America/Port_of_Spain", "GCM": "America/Cayman",
	"PLS": "America/Grand_Turk", "BDA": "Atlantic/Bermuda",
	"PUJ": "America/Santo_Domingo", "SJU": "America/Puerto_Rico",
	"HAV": "America/Havana", "AUA": "America/Aruba",
}
