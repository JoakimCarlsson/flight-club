package emirates

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"
)

// localLayout is the layout of the answer's local times, which carry no
// offset.
const localLayout = "2006-01-02T15:04:05"

// zones maps airport codes to their time zones, read from the site's own
// location taxonomy, which the results page loads. It is safe for
// concurrent use and keeps what it learnt between searches.
type zones struct {
	mu   sync.Mutex
	locs map[string]*time.Location
}

// known reports whether the zones have been learnt already, so the large
// taxonomy need not be read again.
func (z *zones) known() bool {
	z.mu.Lock()
	defer z.mu.Unlock()
	return len(z.locs) > 0
}

// learn reads the airport time zones out of body, a location taxonomy
// answer, and keeps them. A body that is not one changes nothing.
func (z *zones) learn(body []byte) {
	var tree any
	if json.Unmarshal(body, &tree) != nil {
		return
	}
	found := map[string]*time.Location{}
	walkZones(tree, found)
	if len(found) == 0 {
		return
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	if z.locs == nil {
		z.locs = map[string]*time.Location{}
	}
	for code, loc := range found {
		z.locs[code] = loc
	}
}

// lookup returns the time zone of the airport code, false when unknown.
func (z *zones) lookup(code string) (*time.Location, bool) {
	z.mu.Lock()
	defer z.mu.Unlock()
	loc, ok := z.locs[code]
	return loc, ok
}

// walkZones finds every object of tree whose metadata names a code and a
// time zone, and adds the zone to out under the code unless it is there.
func walkZones(tree any, out map[string]*time.Location) {
	switch v := tree.(type) {
	case map[string]any:
		if code, loc, ok := metadataZone(v); ok {
			if _, seen := out[code]; !seen {
				out[code] = loc
			}
		}
		for _, child := range v {
			walkZones(child, out)
		}
	case []any:
		for _, child := range v {
			walkZones(child, out)
		}
	}
}

// metadataZone reads the code and time zone of a taxonomy entry, whose
// metadata holds a code and a timezone with a taxonomyKey such as
// "Asia/Dubai" or "+03:00". It returns false when obj is no such entry.
func metadataZone(obj map[string]any) (string, *time.Location, bool) {
	meta, ok := obj["metadata"].(map[string]any)
	if !ok {
		return "", nil, false
	}
	code, ok := meta["code"].(string)
	if !ok || len(code) != 3 {
		return "", nil, false
	}
	tz, ok := meta["timezone"].(map[string]any)
	if !ok {
		return "", nil, false
	}
	key, _ := tz["taxonomyKey"].(string)
	loc, ok := parseZone(key)
	return code, loc, ok
}

// parseZone reads a zone name such as "Europe/London" or a fixed offset
// such as "-04:00". It returns false when key is neither.
func parseZone(key string) (*time.Location, bool) {
	if key == "" {
		return nil, false
	}
	if key[0] == '+' || key[0] == '-' {
		return fixedZone(key)
	}
	loc, err := time.LoadLocation(key)
	if err != nil {
		return nil, false
	}
	return loc, true
}

// fixedZone reads an offset such as "+05:30" into a fixed zone. It
// returns false when key is not such an offset.
func fixedZone(key string) (*time.Location, bool) {
	hh, mm, found := strings.Cut(key[1:], ":")
	if !found {
		mm = "0"
	}
	h, err1 := strconv.Atoi(hh)
	m, err2 := strconv.Atoi(mm)
	if err1 != nil || err2 != nil {
		return nil, false
	}
	secs := h*3600 + m*60
	if key[0] == '-' {
		secs = -secs
	}
	return time.FixedZone(key, secs), true
}

// flightTimes gives a flight's departure and arrival, read from the local
// times dep and arr at the airports from and to, with the airports'
// offsets. An airport whose zone is unknown takes the offset that makes
// the flight last minutes; with neither known the departure is read as
// UTC. It returns false when a time cannot be read.
func (z *zones) flightTimes(
	from, to, dep, arr string,
	minutes int,
) (time.Time, time.Time, bool) {
	depLocal, err1 := time.Parse(localLayout, dep)
	arrLocal, err2 := time.Parse(localLayout, arr)
	if err1 != nil || err2 != nil {
		return time.Time{}, time.Time{}, false
	}
	flight := time.Duration(minutes) * time.Minute
	depLoc, depOK := z.lookup(from)
	arrLoc, arrOK := z.lookup(to)
	switch {
	case depOK && arrOK:
		return inZone(depLocal, depLoc), inZone(arrLocal, arrLoc), true
	case depOK:
		d := inZone(depLocal, depLoc)
		return d, withLocal(d.Add(flight), arrLocal), true
	case arrOK:
		a := inZone(arrLocal, arrLoc)
		return withLocal(a.Add(-flight), depLocal), a, true
	}
	return depLocal, withLocal(depLocal.Add(flight), arrLocal), true
}

// inZone reads the wall clock of local, parsed as UTC, in loc.
func inZone(local time.Time, loc *time.Location) time.Time {
	return time.Date(local.Year(), local.Month(), local.Day(),
		local.Hour(), local.Minute(), local.Second(), 0, loc)
}

// withLocal returns the instant at, shown in the fixed zone whose wall
// clock reads local, the wall clock parsed as UTC.
func withLocal(at, local time.Time) time.Time {
	offset := int(local.Sub(at).Seconds())
	return at.In(time.FixedZone("", offset))
}
