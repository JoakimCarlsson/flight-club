package qatar

import "time"

// zones are the IANA time zones of Qatar Airways' hub and the main
// airports it and its partners fly to. The answer gives local times
// without offsets, so one end of each flight is placed in its zone and
// the other derived from the flight's duration.
var zones = map[string]string{
	"DOH": "Asia/Qatar",
	"LHR": "Europe/London", "LGW": "Europe/London",
	"MAN": "Europe/London", "EDI": "Europe/London",
	"BHX": "Europe/London", "DUB": "Europe/Dublin",
	"CDG": "Europe/Paris", "NCE": "Europe/Paris",
	"AMS": "Europe/Amsterdam", "BRU": "Europe/Brussels",
	"FRA": "Europe/Berlin", "MUC": "Europe/Berlin",
	"BER": "Europe/Berlin", "DUS": "Europe/Berlin",
	"HAM": "Europe/Berlin", "MAD": "Europe/Madrid",
	"BCN": "Europe/Madrid", "LIS": "Europe/Lisbon",
	"FCO": "Europe/Rome", "MXP": "Europe/Rome", "VCE": "Europe/Rome",
	"ZRH": "Europe/Zurich", "GVA": "Europe/Zurich",
	"VIE": "Europe/Vienna", "CPH": "Europe/Copenhagen",
	"ARN": "Europe/Stockholm", "OSL": "Europe/Oslo",
	"HEL": "Europe/Helsinki", "WAW": "Europe/Warsaw",
	"PRG": "Europe/Prague", "BUD": "Europe/Budapest",
	"ATH": "Europe/Athens", "IST": "Europe/Istanbul",
	"SAW": "Europe/Istanbul", "OTP": "Europe/Bucharest",
	"SOF": "Europe/Sofia",
	"JFK": "America/New_York", "EWR": "America/New_York",
	"BOS": "America/New_York", "IAD": "America/New_York",
	"PHL": "America/New_York", "MIA": "America/New_York",
	"ATL": "America/New_York", "ORD": "America/Chicago",
	"DFW": "America/Chicago", "IAH": "America/Chicago",
	"LAX": "America/Los_Angeles", "SFO": "America/Los_Angeles",
	"SEA": "America/Los_Angeles", "YUL": "America/Toronto",
	"YYZ": "America/Toronto", "GRU": "America/Sao_Paulo",
	"EZE": "America/Argentina/Buenos_Aires", "BOG": "America/Bogota",
	"DXB": "Asia/Dubai", "AUH": "Asia/Dubai", "MCT": "Asia/Muscat",
	"BAH": "Asia/Bahrain", "KWI": "Asia/Kuwait", "RUH": "Asia/Riyadh",
	"JED": "Asia/Riyadh", "DMM": "Asia/Riyadh", "AMM": "Asia/Amman",
	"BEY": "Asia/Beirut", "CAI": "Africa/Cairo", "TLV": "Asia/Jerusalem",
	"IKA": "Asia/Tehran", "BGW": "Asia/Baghdad", "EBL": "Asia/Baghdad",
	"DEL": "Asia/Kolkata", "BOM": "Asia/Kolkata", "BLR": "Asia/Kolkata",
	"MAA": "Asia/Kolkata", "HYD": "Asia/Kolkata", "COK": "Asia/Kolkata",
	"CCU": "Asia/Kolkata", "AMD": "Asia/Kolkata", "GOI": "Asia/Kolkata",
	"KHI": "Asia/Karachi", "LHE": "Asia/Karachi", "ISB": "Asia/Karachi",
	"CMB": "Asia/Colombo", "DAC": "Asia/Dhaka", "KTM": "Asia/Kathmandu",
	"MLE": "Indian/Maldives",
	"BKK": "Asia/Bangkok", "HKT": "Asia/Bangkok", "SIN": "Asia/Singapore",
	"KUL": "Asia/Kuala_Lumpur", "CGK": "Asia/Jakarta",
	"DPS": "Asia/Makassar", "MNL": "Asia/Manila", "SGN": "Asia/Ho_Chi_Minh",
	"HAN": "Asia/Ho_Chi_Minh", "HKG": "Asia/Hong_Kong",
	"PEK": "Asia/Shanghai", "PKX": "Asia/Shanghai", "PVG": "Asia/Shanghai",
	"CAN": "Asia/Shanghai", "TPE": "Asia/Taipei", "ICN": "Asia/Seoul",
	"NRT": "Asia/Tokyo", "HND": "Asia/Tokyo", "KIX": "Asia/Tokyo",
	"SYD": "Australia/Sydney", "MEL": "Australia/Melbourne",
	"BNE": "Australia/Brisbane", "PER": "Australia/Perth",
	"ADL": "Australia/Adelaide", "AKL": "Pacific/Auckland",
	"JNB": "Africa/Johannesburg", "CPT": "Africa/Johannesburg",
	"NBO": "Africa/Nairobi", "ADD": "Africa/Addis_Ababa",
	"LOS": "Africa/Lagos", "ACC": "Africa/Accra", "CMN": "Africa/Casablanca",
	"DAR": "Africa/Dar_es_Salaam", "EBB": "Africa/Kampala",
	"KGL": "Africa/Kigali", "MRU": "Indian/Mauritius",
	"SEZ": "Indian/Mahe", "TUN": "Africa/Tunis", "ALG": "Africa/Algiers",
}

// location is the time zone of airport, and false when it is not known
// or cannot be loaded.
func location(airport string) (*time.Location, bool) {
	name, ok := zones[airport]
	if !ok {
		return nil, false
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, false
	}
	return loc, true
}

// localLayout is the layout of the answer's local times, which carry no
// offset.
const localLayout = "2006-01-02T15:04:05"

// placeFlight gives a flight's local departure dep at origin and local
// arrival arr at destination their offsets, using seconds, the flight's
// duration. The end whose airport's zone is known is placed in it and
// the other end is derived from the duration; when neither zone is known
// both are given in UTC. It returns false when a time cannot be read.
func placeFlight(
	dep, arr, origin, destination string,
	seconds int,
) (time.Time, time.Time, bool) {
	d, err1 := time.Parse(localLayout, dep)
	a, err2 := time.Parse(localLayout, arr)
	if err1 != nil || err2 != nil {
		return time.Time{}, time.Time{}, false
	}
	flight := time.Duration(seconds) * time.Second
	if loc, ok := location(origin); ok {
		at := inZone(d, loc)
		return at, derive(a, at.Add(flight), destination), true
	}
	if loc, ok := location(destination); ok {
		at := inZone(a, loc)
		return derive(d, at.Add(-flight), origin), at, true
	}
	return d, a, true
}

// inZone reads the wall time of t, parsed as UTC, as a time in loc.
func inZone(t time.Time, loc *time.Location) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(),
		t.Second(), 0, loc)
}

// derive gives instant the offset that makes its wall time wall, parsed
// as UTC, rounded to a quarter hour and named after airport.
func derive(wall, instant time.Time, airport string) time.Time {
	offset := wall.Sub(instant.UTC()).Round(15 * time.Minute)
	return instant.In(time.FixedZone(airport, int(offset.Seconds())))
}
