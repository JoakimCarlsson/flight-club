package qantas

import (
	"math"
	"regexp"
	"strconv"
	"time"
)

// localLayout is the layout of the answer's local times, which carry no
// offset.
const localLayout = "2006-01-02T15:04:05"

// legTime is a leg's departure and arrival in their airports' offsets.
type legTime struct {
	departure time.Time
	arrival   time.Time
}

// point is a departure or an arrival on an itinerary's timeline.
type point struct {
	airport string
	local   time.Time
	minutes int
}

// legTimes places every leg of legs in time. The answer gives local times
// without offsets, so the timeline is built from the legs' flying and
// layover times, anchored at the first airport whose zone is known, and an
// airport missing from zones takes the offset that its local time shows
// against that timeline. It returns false when a time or duration cannot be
// read or no airport of the itinerary has a known zone.
func legTimes(legs []leg) ([]legTime, bool) {
	points := make([]point, 0, 2*len(legs))
	at := 0
	for i, l := range legs {
		dep, err := time.Parse(localLayout, l.DepartsAt)
		if err != nil {
			return nil, false
		}
		arr, err := time.Parse(localLayout, l.ArrivesAt)
		if err != nil {
			return nil, false
		}
		fly, ok := parseDuration(l.Duration)
		if !ok {
			return nil, false
		}
		points = append(points, point{l.Origin.Code, dep, at})
		at += fly
		points = append(points, point{l.Destination.Code, arr, at})
		if i+1 < len(legs) {
			wait, ok := parseDuration(l.LayoverDuration)
			if !ok {
				return nil, false
			}
			at += wait
		}
	}
	base, ok := anchor(points)
	if !ok {
		return nil, false
	}
	placed := make([]time.Time, len(points))
	for i, p := range points {
		instant := base.Add(time.Duration(p.minutes) * time.Minute)
		placed[i] = instant.In(time.FixedZone(p.airport, offsetAt(p, instant)))
	}
	out := make([]legTime, len(legs))
	for i := range out {
		out[i] = legTime{departure: placed[2*i], arrival: placed[2*i+1]}
	}
	return out, true
}

// anchor is the instant the first point of points happens, found from the
// first point whose airport's zone is known. It returns false when none is.
func anchor(points []point) (time.Time, bool) {
	for _, p := range points {
		loc, ok := location(p.airport)
		if !ok {
			continue
		}
		l := p.local
		instant := time.Date(l.Year(), l.Month(), l.Day(), l.Hour(),
			l.Minute(), l.Second(), 0, loc)
		return instant.Add(-time.Duration(p.minutes) * time.Minute), true
	}
	return time.Time{}, false
}

// offsetAt is the UTC offset in seconds of p's airport at instant: its
// zone's offset when the zone is known, or else the difference between
// p's local clock and instant's UTC clock, rounded to a quarter hour.
func offsetAt(p point, instant time.Time) int {
	if loc, ok := location(p.airport); ok {
		_, off := instant.In(loc).Zone()
		return off
	}
	utc := instant.UTC()
	diff := p.local.Hour()*60 + p.local.Minute() -
		(utc.Hour()*60 + utc.Minute())
	diff = ((diff % 1440) + 1440) % 1440
	if diff > 14*60 {
		diff -= 1440
	}
	return int(math.Round(float64(diff)/15)) * 15 * 60
}

// location is the time zone of airport, and false when it is not known or
// cannot be loaded.
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

// durationPattern reads the answer's durations, such as "14h 00m" or
// "1h 5m".
var durationPattern = regexp.MustCompile(`^\s*(\d+)h\s*(\d+)m\s*$`)

// parseDuration reads a duration of the answer as minutes, and returns
// false when s is not one.
func parseDuration(s string) (int, bool) {
	m := durationPattern.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	h, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	mins, err := strconv.Atoi(m[2])
	if err != nil {
		return 0, false
	}
	return h*60 + mins, true
}
