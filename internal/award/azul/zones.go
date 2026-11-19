package azul

import (
	"sync"
	"time"
)

// brasilia is the offset of Brasília time, the zone most of Azul's network
// keeps, in seconds east of UTC.
const brasilia = -3 * 60 * 60

// zones are the time zones of the airports Azul flies to outside Brasília
// time. Brazilian airports missing here keep Brasília time.
var zones = map[string]string{
	"MAO": "America/Manaus", "TFF": "America/Manaus",
	"PIN": "America/Manaus", "TBT": "America/Manaus",
	"BVB": "America/Boa_Vista", "PVH": "America/Porto_Velho",
	"JPR": "America/Porto_Velho", "VLP": "America/Porto_Velho",
	"CGB": "America/Cuiaba", "ROO": "America/Cuiaba",
	"SNP": "America/Cuiaba", "OPS": "America/Cuiaba",
	"AFL": "America/Cuiaba", "BPG": "America/Cuiaba",
	"CGR": "America/Campo_Grande", "DOU": "America/Campo_Grande",
	"CMG": "America/Campo_Grande", "TJL": "America/Campo_Grande",
	"RBR": "America/Rio_Branco", "CZS": "America/Rio_Branco",
	"FEN": "America/Noronha",
	"FLL": "America/New_York", "MCO": "America/New_York",
	"MIA": "America/New_York", "JFK": "America/New_York",
	"EWR": "America/New_York", "ATL": "America/New_York",
	"LIS": "Europe/Lisbon", "OPO": "Europe/Lisbon",
	"ORY": "Europe/Paris", "CDG": "Europe/Paris",
	"MAD": "Europe/Madrid", "BCN": "Europe/Madrid",
	"FCO": "Europe/Rome", "MXP": "Europe/Rome",
	"EZE": "America/Argentina/Buenos_Aires",
	"AEP": "America/Argentina/Buenos_Aires",
	"COR": "America/Argentina/Cordoba",
	"MDZ": "America/Argentina/Mendoza",
	"BRC": "America/Argentina/Salta",
	"ROS": "America/Argentina/Cordoba",
	"MVD": "America/Montevideo", "PDP": "America/Montevideo",
	"ASU": "America/Asuncion", "SCL": "America/Santiago",
	"VVI": "America/La_Paz", "LIM": "America/Lima",
	"CAY": "America/Cayenne", "CUR": "America/Curacao",
	"AUA": "America/Aruba", "PUJ": "America/Santo_Domingo",
	"CUN": "America/Cancun", "BOG": "America/Bogota",
}

// zoneCache holds the locations loaded from zones, by name.
var zoneCache sync.Map

// zoneOffset returns the UTC offset in seconds of airport at its local
// time local. Brazilian airports not in zones are not known here, so it
// returns false for any airport missing from zones or whose zone cannot
// be loaded.
func zoneOffset(airport string, local time.Time) (int, bool) {
	name, ok := zones[airport]
	if !ok {
		return 0, false
	}
	loc, ok := location(name)
	if !ok {
		return 0, false
	}
	t := time.Date(local.Year(), local.Month(), local.Day(),
		local.Hour(), local.Minute(), local.Second(), 0, loc)
	_, off := t.Zone()
	return off, true
}

// location loads the zone name once, reporting false when it cannot be.
func location(name string) (*time.Location, bool) {
	if v, ok := zoneCache.Load(name); ok {
		loc, ok := v.(*time.Location)
		return loc, ok
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, false
	}
	zoneCache.Store(name, loc)
	return loc, true
}
