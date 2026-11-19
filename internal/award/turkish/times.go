package turkish

import "time"

// leg is one flight's local times as the answer writes them, with no
// offset, and its time in the air.
type leg struct {
	from     string
	to       string
	dep      time.Time
	arr      time.Time
	duration time.Duration
}

// placed is a leg's departure and arrival in their airports' offsets.
type placed struct {
	dep time.Time
	arr time.Time
}

// zones maps the IATA codes of airports Turkish Airlines serves to their
// IANA time zones.
var zones = map[string]string{
	"IST": "Europe/Istanbul", "SAW": "Europe/Istanbul",
	"ESB": "Europe/Istanbul", "ADB": "Europe/Istanbul",
	"AYT": "Europe/Istanbul", "DLM": "Europe/Istanbul",
	"BJV": "Europe/Istanbul", "ADA": "Europe/Istanbul",
	"TZX": "Europe/Istanbul", "GZT": "Europe/Istanbul",
	"VAN": "Europe/Istanbul", "ERZ": "Europe/Istanbul",
	"DIY": "Europe/Istanbul", "KYA": "Europe/Istanbul",
	"ASR": "Europe/Istanbul", "SZF": "Europe/Istanbul",
	"MLX": "Europe/Istanbul", "EZS": "Europe/Istanbul",
	"NAV": "Europe/Istanbul",
	"JFK": "America/New_York", "EWR": "America/New_York",
	"LGA": "America/New_York", "BOS": "America/New_York",
	"IAD": "America/New_York", "DCA": "America/New_York",
	"ATL": "America/New_York", "MIA": "America/New_York",
	"FLL": "America/New_York", "MCO": "America/New_York",
	"TPA": "America/New_York", "PHL": "America/New_York",
	"CLT": "America/New_York", "DTW": "America/New_York",
	"CLE": "America/New_York", "PIT": "America/New_York",
	"BWI": "America/New_York", "RDU": "America/New_York",
	"ORD": "America/Chicago", "MDW": "America/Chicago",
	"IAH": "America/Chicago", "HOU": "America/Chicago",
	"DFW": "America/Chicago", "MSP": "America/Chicago",
	"MSY": "America/Chicago", "STL": "America/Chicago",
	"DEN": "America/Denver", "SLC": "America/Denver",
	"PHX": "America/Phoenix",
	"LAX": "America/Los_Angeles", "SFO": "America/Los_Angeles",
	"SEA": "America/Los_Angeles", "SAN": "America/Los_Angeles",
	"LAS": "America/Los_Angeles", "PDX": "America/Los_Angeles",
	"SJC": "America/Los_Angeles", "OAK": "America/Los_Angeles",
	"YYZ": "America/Toronto", "YOW": "America/Toronto",
	"YUL": "America/Montreal",
	"YVR": "America/Vancouver",
	"YYC": "America/Edmonton", "YEG": "America/Edmonton",
	"MEX": "America/Mexico_City",
	"CUN": "America/Cancun",
	"BOG": "America/Bogota",
	"PTY": "America/Panama",
	"CCS": "America/Caracas",
	"HAV": "America/Havana",
	"GRU": "America/Sao_Paulo", "GIG": "America/Sao_Paulo",
	"EZE": "America/Argentina/Buenos_Aires",
	"SCL": "America/Santiago",
	"LIM": "America/Lima",
	"PUJ": "America/Santo_Domingo",
	"LHR": "Europe/London", "LGW": "Europe/London",
	"STN": "Europe/London", "LTN": "Europe/London",
	"MAN": "Europe/London", "BHX": "Europe/London",
	"EDI": "Europe/London", "GLA": "Europe/London",
	"DUB": "Europe/Dublin",
	"CDG": "Europe/Paris", "ORY": "Europe/Paris", "NCE": "Europe/Paris",
	"LYS": "Europe/Paris", "MRS": "Europe/Paris", "TLS": "Europe/Paris",
	"BOD": "Europe/Paris", "NTE": "Europe/Paris",
	"BER": "Europe/Berlin", "FRA": "Europe/Berlin",
	"MUC": "Europe/Berlin", "DUS": "Europe/Berlin",
	"HAM": "Europe/Berlin", "STR": "Europe/Berlin",
	"CGN": "Europe/Berlin", "HAJ": "Europe/Berlin",
	"NUE": "Europe/Berlin", "LEJ": "Europe/Berlin",
	"AMS": "Europe/Amsterdam",
	"BRU": "Europe/Brussels",
	"LUX": "Europe/Luxembourg",
	"ZRH": "Europe/Zurich", "GVA": "Europe/Zurich",
	"BSL": "Europe/Zurich",
	"VIE": "Europe/Vienna",
	"FCO": "Europe/Rome", "MXP": "Europe/Rome", "LIN": "Europe/Rome",
	"VCE": "Europe/Rome", "BLQ": "Europe/Rome", "NAP": "Europe/Rome",
	"CTA": "Europe/Rome", "PSA": "Europe/Rome", "TRN": "Europe/Rome",
	"BRI": "Europe/Rome",
	"MAD": "Europe/Madrid", "BCN": "Europe/Madrid",
	"AGP": "Europe/Madrid", "VLC": "Europe/Madrid",
	"PMI": "Europe/Madrid", "BIO": "Europe/Madrid",
	"LIS": "Europe/Lisbon", "OPO": "Europe/Lisbon",
	"CPH": "Europe/Copenhagen",
	"ARN": "Europe/Stockholm", "GOT": "Europe/Stockholm",
	"OSL": "Europe/Oslo",
	"HEL": "Europe/Helsinki",
	"WAW": "Europe/Warsaw", "KRK": "Europe/Warsaw",
	"GDN": "Europe/Warsaw",
	"PRG": "Europe/Prague",
	"BUD": "Europe/Budapest",
	"OTP": "Europe/Bucharest", "CLJ": "Europe/Bucharest",
	"SOF": "Europe/Sofia",
	"ATH": "Europe/Athens", "SKG": "Europe/Athens",
	"BEG": "Europe/Belgrade",
	"ZAG": "Europe/Zagreb",
	"LJU": "Europe/Ljubljana",
	"SJJ": "Europe/Sarajevo",
	"SKP": "Europe/Skopje",
	"TIA": "Europe/Tirane",
	"TGD": "Europe/Podgorica",
	"KIV": "Europe/Chisinau",
	"KBP": "Europe/Kiev", "ODS": "Europe/Kiev",
	"MSQ": "Europe/Minsk",
	"RIX": "Europe/Riga",
	"VNO": "Europe/Vilnius",
	"TLL": "Europe/Tallinn",
	"SVO": "Europe/Moscow", "VKO": "Europe/Moscow",
	"DME": "Europe/Moscow", "LED": "Europe/Moscow",
	"KZN": "Europe/Moscow",
	"KEF": "Atlantic/Reykjavik",
	"MLA": "Europe/Malta",
	"LCA": "Asia/Nicosia",
	"ECN": "Asia/Famagusta",
	"TBS": "Asia/Tbilisi", "BUS": "Asia/Tbilisi",
	"EVN": "Asia/Yerevan",
	"GYD": "Asia/Baku",
	"TAS": "Asia/Tashkent", "SKD": "Asia/Tashkent",
	"ALA": "Asia/Almaty",
	"FRU": "Asia/Bishkek",
	"DYU": "Asia/Dushanbe",
	"ASB": "Asia/Ashgabat",
	"KBL": "Asia/Kabul",
	"DXB": "Asia/Dubai", "AUH": "Asia/Dubai",
	"DOH": "Asia/Qatar",
	"BAH": "Asia/Bahrain",
	"KWI": "Asia/Kuwait",
	"MCT": "Asia/Muscat",
	"RUH": "Asia/Riyadh", "JED": "Asia/Riyadh", "DMM": "Asia/Riyadh",
	"MED": "Asia/Riyadh",
	"BGW": "Asia/Baghdad", "EBL": "Asia/Baghdad", "BSR": "Asia/Baghdad",
	"IKA": "Asia/Tehran", "MHD": "Asia/Tehran",
	"AMM": "Asia/Amman",
	"BEY": "Asia/Beirut",
	"TLV": "Asia/Jerusalem",
	"DAM": "Asia/Damascus",
	"CAI": "Africa/Cairo", "HRG": "Africa/Cairo", "SSH": "Africa/Cairo",
	"KHI": "Asia/Karachi", "LHE": "Asia/Karachi", "ISB": "Asia/Karachi",
	"DEL": "Asia/Kolkata", "BOM": "Asia/Kolkata", "BLR": "Asia/Kolkata",
	"MAA": "Asia/Kolkata", "HYD": "Asia/Kolkata",
	"DAC": "Asia/Dhaka",
	"KTM": "Asia/Kathmandu",
	"CMB": "Asia/Colombo",
	"MLE": "Indian/Maldives",
	"BKK": "Asia/Bangkok", "HKT": "Asia/Bangkok",
	"SIN": "Asia/Singapore",
	"KUL": "Asia/Kuala_Lumpur",
	"CGK": "Asia/Jakarta",
	"DPS": "Asia/Makassar",
	"MNL": "Asia/Manila",
	"SGN": "Asia/Ho_Chi_Minh",
	"PEK": "Asia/Shanghai", "PKX": "Asia/Shanghai",
	"PVG": "Asia/Shanghai", "CAN": "Asia/Shanghai",
	"HKG": "Asia/Hong_Kong",
	"TPE": "Asia/Taipei",
	"ICN": "Asia/Seoul",
	"NRT": "Asia/Tokyo", "HND": "Asia/Tokyo", "KIX": "Asia/Tokyo",
	"SYD": "Australia/Sydney",
	"MEL": "Australia/Melbourne",
	"CMN": "Africa/Casablanca", "RAK": "Africa/Casablanca",
	"ALG": "Africa/Algiers",
	"TUN": "Africa/Tunis",
	"TIP": "Africa/Tripoli", "MJI": "Africa/Tripoli",
	"LOS": "Africa/Lagos", "ABV": "Africa/Lagos",
	"ACC": "Africa/Accra",
	"DSS": "Africa/Dakar",
	"ABJ": "Africa/Abidjan",
	"ADD": "Africa/Addis_Ababa",
	"NBO": "Africa/Nairobi",
	"DAR": "Africa/Dar_es_Salaam", "JRO": "Africa/Dar_es_Salaam",
	"ZNZ": "Africa/Dar_es_Salaam",
	"EBB": "Africa/Kampala",
	"KGL": "Africa/Kigali",
	"JNB": "Africa/Johannesburg", "CPT": "Africa/Johannesburg",
	"DUR": "Africa/Johannesburg",
	"KRT": "Africa/Khartoum",
	"MGQ": "Africa/Mogadishu",
	"JIB": "Africa/Djibouti",
	"MRU": "Indian/Mauritius",
	"LAD": "Africa/Luanda",
	"FIH": "Africa/Kinshasa",
	"DLA": "Africa/Douala",
	"MPM": "Africa/Maputo"}

// knownOffset returns the UTC offset, in seconds, of airport code at the
// local wall time t, and false when the airport's zone is not known.
func knownOffset(code string, t time.Time) (int, bool) {
	name, ok := zones[code]
	if !ok {
		return 0, false
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return 0, false
	}
	local := time.Date(
		t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, loc,
	)
	_, off := local.Zone()
	return off, true
}

// placeLegs places every leg's times in its airports' offsets. Offsets
// come from the zone table; an airport missing from it gets the offset
// its flights imply, the gap between local arrival and departure less
// the time in the air, measured from a neighbouring airport whose offset
// is known. When no airport is known the first is taken to be UTC.
func placeLegs(legs []leg) []placed {
	n := len(legs)
	depOff := make([]*int, n)
	arrOff := make([]*int, n)
	for i, l := range legs {
		if off, ok := knownOffset(l.from, l.dep); ok {
			depOff[i] = &off
		}
		if off, ok := knownOffset(l.to, l.arr); ok {
			arrOff[i] = &off
		}
	}
	for changed := true; changed; {
		changed = fillOffsets(legs, depOff, arrOff)
	}
	if depOff[0] == nil {
		zero := 0
		depOff[0] = &zero
		for changed := true; changed; {
			changed = fillOffsets(legs, depOff, arrOff)
		}
	}
	out := make([]placed, n)
	for i, l := range legs {
		out[i] = placed{
			dep: inOffset(l.dep, l.from, depOff[i]),
			arr: inOffset(l.arr, l.to, arrOff[i]),
		}
	}
	return out
}

// fillOffsets derives each missing offset from a known one once: across
// a flight from its time in the air, and across a connection, where the
// arriving and departing airport are the same. It reports whether any
// offset was filled.
func fillOffsets(legs []leg, depOff, arrOff []*int) bool {
	changed := false
	for i, l := range legs {
		shift := int(l.arr.Sub(l.dep).Seconds() - l.duration.Seconds())
		if l.duration > 0 && depOff[i] != nil && arrOff[i] == nil {
			v := *depOff[i] + shift
			arrOff[i], changed = &v, true
		}
		if l.duration > 0 && arrOff[i] != nil && depOff[i] == nil {
			v := *arrOff[i] - shift
			depOff[i], changed = &v, true
		}
		if i+1 < len(legs) && legs[i+1].from == l.to {
			if arrOff[i] != nil && depOff[i+1] == nil {
				depOff[i+1], changed = arrOff[i], true
			}
			if depOff[i+1] != nil && arrOff[i] == nil {
				arrOff[i], changed = depOff[i+1], true
			}
		}
	}
	return changed
}

// inOffset returns the wall time t at airport code in the fixed offset
// off, named for the airport, or t unchanged when off is nil.
func inOffset(t time.Time, code string, off *int) time.Time {
	if off == nil {
		return t
	}
	zone := time.FixedZone(code, *off)
	return time.Date(
		t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, zone,
	)
}
