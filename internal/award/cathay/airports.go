package cathay

import (
	"math"
	"time"
)

// airport is what pricing and timing an award needs to know about an
// airport: where it is, its time zone and the country it is in.
type airport struct {
	lat     float64
	lon     float64
	zone    string
	country string
}

// airports are the airports Cathay Pacific flies to nonstop, which are the
// only routes its award calendar answers for.
var airports = map[string]airport{
	"HKG": {22.308, 113.918, "Asia/Hong_Kong", "HK"},
	"JFK": {40.640, -73.779, "America/New_York", "US"},
	"BOS": {42.366, -71.010, "America/New_York", "US"},
	"ORD": {41.978, -87.905, "America/Chicago", "US"},
	"DFW": {32.897, -97.038, "America/Chicago", "US"},
	"LAX": {33.942, -118.408, "America/Los_Angeles", "US"},
	"SFO": {37.619, -122.375, "America/Los_Angeles", "US"},
	"SEA": {47.450, -122.309, "America/Los_Angeles", "US"},
	"YVR": {49.195, -123.184, "America/Vancouver", "CA"},
	"YYZ": {43.677, -79.631, "America/Toronto", "CA"},
	"LHR": {51.470, -0.454, "Europe/London", "GB"},
	"LGW": {51.148, -0.190, "Europe/London", "GB"},
	"MAN": {53.354, -2.275, "Europe/London", "GB"},
	"DUB": {53.421, -6.270, "Europe/Dublin", "IE"},
	"CDG": {49.010, 2.548, "Europe/Paris", "FR"},
	"FRA": {50.038, 8.562, "Europe/Berlin", "DE"},
	"MUC": {48.354, 11.786, "Europe/Berlin", "DE"},
	"ZRH": {47.458, 8.548, "Europe/Zurich", "CH"},
	"AMS": {52.310, 4.768, "Europe/Amsterdam", "NL"},
	"BRU": {50.901, 4.484, "Europe/Brussels", "BE"},
	"CPH": {55.618, 12.656, "Europe/Copenhagen", "DK"},
	"BCN": {41.297, 2.078, "Europe/Madrid", "ES"},
	"MAD": {40.472, -3.561, "Europe/Madrid", "ES"},
	"FCO": {41.800, 12.239, "Europe/Rome", "IT"},
	"MXP": {45.630, 8.723, "Europe/Rome", "IT"},
	"IST": {41.262, 28.742, "Europe/Istanbul", "TR"},
	"DXB": {25.253, 55.366, "Asia/Dubai", "AE"},
	"RUH": {24.958, 46.699, "Asia/Riyadh", "SA"},
	"TLV": {32.011, 34.887, "Asia/Jerusalem", "IL"},
	"JNB": {-26.139, 28.246, "Africa/Johannesburg", "ZA"},
	"DEL": {28.556, 77.100, "Asia/Kolkata", "IN"},
	"BOM": {19.090, 72.866, "Asia/Kolkata", "IN"},
	"MAA": {12.994, 80.171, "Asia/Kolkata", "IN"},
	"BLR": {13.199, 77.706, "Asia/Kolkata", "IN"},
	"HYD": {17.240, 78.429, "Asia/Kolkata", "IN"},
	"KTM": {27.697, 85.359, "Asia/Kathmandu", "NP"},
	"CMB": {7.181, 79.884, "Asia/Colombo", "LK"},
	"DAC": {23.843, 90.398, "Asia/Dhaka", "BD"},
	"SYD": {-33.946, 151.177, "Australia/Sydney", "AU"},
	"MEL": {-37.669, 144.841, "Australia/Melbourne", "AU"},
	"BNE": {-27.384, 153.117, "Australia/Brisbane", "AU"},
	"PER": {-31.940, 115.967, "Australia/Perth", "AU"},
	"ADL": {-34.945, 138.531, "Australia/Adelaide", "AU"},
	"CNS": {-16.886, 145.755, "Australia/Brisbane", "AU"},
	"AKL": {-37.008, 174.792, "Pacific/Auckland", "NZ"},
	"CHC": {-43.489, 172.532, "Pacific/Auckland", "NZ"},
	"NRT": {35.765, 140.386, "Asia/Tokyo", "JP"},
	"HND": {35.549, 139.780, "Asia/Tokyo", "JP"},
	"KIX": {34.427, 135.244, "Asia/Tokyo", "JP"},
	"NGO": {34.858, 136.805, "Asia/Tokyo", "JP"},
	"FUK": {33.586, 130.451, "Asia/Tokyo", "JP"},
	"CTS": {42.775, 141.692, "Asia/Tokyo", "JP"},
	"OKA": {26.196, 127.646, "Asia/Tokyo", "JP"},
	"ICN": {37.460, 126.441, "Asia/Seoul", "KR"},
	"PUS": {35.180, 128.938, "Asia/Seoul", "KR"},
	"TPE": {25.078, 121.233, "Asia/Taipei", "TW"},
	"KHH": {22.577, 120.350, "Asia/Taipei", "TW"},
	"PEK": {40.080, 116.585, "Asia/Shanghai", "CN"},
	"PKX": {39.509, 116.411, "Asia/Shanghai", "CN"},
	"PVG": {31.143, 121.805, "Asia/Shanghai", "CN"},
	"SHA": {31.198, 121.336, "Asia/Shanghai", "CN"},
	"CAN": {23.392, 113.299, "Asia/Shanghai", "CN"},
	"XMN": {24.544, 118.128, "Asia/Shanghai", "CN"},
	"FOC": {25.935, 119.663, "Asia/Shanghai", "CN"},
	"HGH": {30.229, 120.434, "Asia/Shanghai", "CN"},
	"NKG": {31.742, 118.862, "Asia/Shanghai", "CN"},
	"WUH": {30.784, 114.208, "Asia/Shanghai", "CN"},
	"CSX": {28.189, 113.220, "Asia/Shanghai", "CN"},
	"CGO": {34.520, 113.841, "Asia/Shanghai", "CN"},
	"CTU": {30.578, 103.947, "Asia/Shanghai", "CN"},
	"TFU": {30.319, 104.441, "Asia/Shanghai", "CN"},
	"CKG": {29.719, 106.642, "Asia/Shanghai", "CN"},
	"KMG": {25.102, 102.929, "Asia/Shanghai", "CN"},
	"XIY": {34.447, 108.752, "Asia/Shanghai", "CN"},
	"TAO": {36.362, 120.088, "Asia/Shanghai", "CN"},
	"DLC": {38.966, 121.539, "Asia/Shanghai", "CN"},
	"SYX": {18.303, 109.412, "Asia/Shanghai", "CN"},
	"SIN": {1.364, 103.991, "Asia/Singapore", "SG"},
	"KUL": {2.746, 101.710, "Asia/Kuala_Lumpur", "MY"},
	"PEN": {5.297, 100.277, "Asia/Kuala_Lumpur", "MY"},
	"BKK": {13.690, 100.750, "Asia/Bangkok", "TH"},
	"HKT": {8.113, 98.317, "Asia/Bangkok", "TH"},
	"CNX": {18.767, 98.963, "Asia/Bangkok", "TH"},
	"SGN": {10.819, 106.652, "Asia/Ho_Chi_Minh", "VN"},
	"HAN": {21.221, 105.807, "Asia/Bangkok", "VN"},
	"DAD": {16.044, 108.199, "Asia/Ho_Chi_Minh", "VN"},
	"MNL": {14.509, 121.020, "Asia/Manila", "PH"},
	"CEB": {10.307, 123.979, "Asia/Manila", "PH"},
	"CGK": {-6.126, 106.656, "Asia/Jakarta", "ID"},
	"DPS": {-8.748, 115.167, "Asia/Makassar", "ID"},
	"SUB": {-7.380, 112.787, "Asia/Jakarta", "ID"},
	"RGN": {16.907, 96.133, "Asia/Yangon", "MM"},
	"PNH": {11.552, 104.844, "Asia/Phnom_Penh", "KH"},
	"MLE": {4.192, 73.529, "Indian/Maldives", "MV"},
}

// cityAirports are the airports Cathay serves of the city codes that name
// more than one airport, or that differ from the airport code.
var cityAirports = map[string][]string{
	"NYC": {"JFK"},
	"CHI": {"ORD"},
	"YTO": {"YYZ"},
	"LON": {"LHR", "LGW"},
	"PAR": {"CDG"},
	"MIL": {"MXP"},
	"ROM": {"FCO"},
	"TYO": {"NRT", "HND"},
	"OSA": {"KIX"},
	"SPK": {"CTS"},
	"SEL": {"ICN"},
	"BJS": {"PEK", "PKX"},
	"SHA": {"PVG", "SHA"},
	"JKT": {"CGK"},
}

// cityAirportsOf returns the airports to search for code: those of a city
// code it knows, or code itself.
func cityAirportsOf(code string) []string {
	if a, ok := cityAirports[code]; ok {
		return a
	}
	return []string{code}
}

// earthRadiusMiles is the mean radius of the earth in statute miles, the
// unit Asia Miles distance bands are drawn in.
const earthRadiusMiles = 3958.8

// distance returns the great-circle distance in statute miles between a
// and b.
func distance(a, b airport) float64 {
	rad := func(d float64) float64 { return d * math.Pi / 180 }
	dLat := rad(b.lat - a.lat)
	dLon := rad(b.lon - a.lon)
	h := math.Pow(math.Sin(dLat/2), 2) +
		math.Cos(rad(a.lat))*math.Cos(rad(b.lat))*
			math.Pow(math.Sin(dLon/2), 2)
	return 2 * earthRadiusMiles * math.Asin(math.Sqrt(h))
}

// location returns the time zone of a, or UTC when it cannot be loaded.
func (a airport) location() *time.Location {
	loc, err := time.LoadLocation(a.zone)
	if err != nil {
		return time.UTC
	}
	return loc
}
