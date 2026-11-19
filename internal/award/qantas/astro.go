package qantas

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"regexp"
)

// islandProps finds the props attribute of every Astro island of a page.
var islandProps = regexp.MustCompile(`<astro-island[^>]*?\sprops="([^"]*)"`)

// astroPlain, astroArray and astroSet are the Astro prop encoding's type
// tags for a plain value or object, an array and a set.
const (
	astroPlain = 0
	astroArray = 1
	astroSet   = 5
)

// initialResult is the search answer the finder's server renders into its
// page, as the search API's result: the answer, or why there is none.
type initialResult struct {
	IsOk  bool            `json:"isOk"`
	Ok    answer          `json:"ok"`
	Error json.RawMessage `json:"error"`
}

// initialData reads the search answer the finder's server rendered into
// markup as the initialData prop of its search app's island. It reports
// false when no island carries one, and returns an error when the prop
// cannot be read or says the search failed.
func initialData(markup string) (answer, bool, error) {
	for _, m := range islandProps.FindAllStringSubmatch(markup, -1) {
		var props map[string]json.RawMessage
		if json.Unmarshal([]byte(html.UnescapeString(m[1])), &props) != nil {
			continue
		}
		raw, ok := props["initialData"]
		if !ok {
			continue
		}
		res, err := decodeInitial(raw)
		if err != nil {
			return answer{}, true, err
		}
		return res, true, nil
	}
	return answer{}, false, nil
}

// decodeInitial turns the Astro-encoded initialData prop raw into the
// search answer. It returns an error when raw cannot be read or the
// search failed.
func decodeInitial(raw json.RawMessage) (answer, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var encoded any
	if err := dec.Decode(&encoded); err != nil {
		return answer{}, fmt.Errorf("read search answer: %w", err)
	}
	plain, err := json.Marshal(unwrap(encoded))
	if err != nil {
		return answer{}, fmt.Errorf("read search answer: %w", err)
	}
	var res initialResult
	if err := json.Unmarshal(plain, &res); err != nil {
		return answer{}, fmt.Errorf("read search answer: %w", err)
	}
	if !res.IsOk {
		return answer{}, errors.New("search failed: " + string(res.Error))
	}
	if res.Ok.Flights == nil {
		res.Ok.Flights = []flight{}
	}
	return res.Ok, nil
}

// unwrap decodes a value in Astro's prop encoding, in which every value is
// a [type, value] pair and an object's fields and an array's items are
// encoded the same way, into the plain value it stands for. A value not in
// that form is given back as it is.
func unwrap(v any) any {
	pair, ok := v.([]any)
	if !ok || len(pair) != 2 {
		return v
	}
	tag, ok := pair[0].(json.Number)
	if !ok {
		return v
	}
	kind, err := tag.Int64()
	if err != nil {
		return v
	}
	switch kind {
	case astroPlain:
		obj, ok := pair[1].(map[string]any)
		if !ok {
			return pair[1]
		}
		out := make(map[string]any, len(obj))
		for k, field := range obj {
			out[k] = unwrap(field)
		}
		return out
	case astroArray, astroSet:
		items, ok := pair[1].([]any)
		if !ok {
			return pair[1]
		}
		out := make([]any, len(items))
		for i, item := range items {
			out[i] = unwrap(item)
		}
		return out
	}
	return pair[1]
}
