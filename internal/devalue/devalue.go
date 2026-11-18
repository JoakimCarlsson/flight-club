// Package devalue decodes the devalue format SvelteKit uses for the data it
// streams to the browser: a flat JSON array whose first element is the root
// value and in which every array element and object property is the index of
// another element of the array.
package devalue

import (
	"encoding/json"
	"errors"
	"fmt"
)

// The negative indexes devalue writes in place of a reference for values JSON
// cannot hold.
const (
	undefined        = -1
	hole             = -2
	notANumber       = -3
	positiveInfinity = -4
	negativeInfinity = -5
	negativeZero     = -6
)

// Unmarshal decodes the devalue array data into v, as encoding/json would
// decode the same value written as plain JSON. Values JSON cannot hold
// (undefined, NaN and the infinities) decode as null, a Date or a BigInt as
// its string, a Set as an array and a Map with string keys as an object. It
// returns an error when data is not a devalue array or v cannot hold the
// decoded value.
func Unmarshal(data []byte, v any) error {
	value, err := Decode(data)
	if err != nil {
		return err
	}
	plain, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("devalue: re-encoding: %w", err)
	}
	return json.Unmarshal(plain, v)
}

// Decode decodes the devalue array data into the plain Go value it holds:
// maps, slices, strings, float64s, bools and nils, as encoding/json decodes
// into an any. It returns an error when data is not a devalue array or an
// index points outside it.
func Decode(data []byte) (any, error) {
	var elements []json.RawMessage
	if err := json.Unmarshal(data, &elements); err != nil {
		return nil, fmt.Errorf("devalue: %w", err)
	}
	if len(elements) == 0 {
		return nil, errors.New("devalue: empty array")
	}
	d := &decoder{elements: elements, done: map[int]any{}}
	return d.hydrate(0)
}

// decoder holds the flat array being decoded and the elements it has already
// hydrated, so a value referenced from several places is decoded once.
type decoder struct {
	elements []json.RawMessage
	done     map[int]any
}

// hydrate returns the value of element index with every reference in it
// resolved. It returns an error when index or any index it references is
// outside the array, or an element is not valid JSON.
func (d *decoder) hydrate(index int) (any, error) {
	switch index {
	case undefined, hole, notANumber, positiveInfinity, negativeInfinity:
		return nil, nil
	case negativeZero:
		return 0.0, nil
	}
	if index < 0 || index >= len(d.elements) {
		return nil, fmt.Errorf("devalue: index %d out of range", index)
	}
	if v, ok := d.done[index]; ok {
		return v, nil
	}
	v, err := d.element(d.elements[index])
	if err != nil {
		return nil, err
	}
	d.done[index] = v
	return v, nil
}

// element decodes one raw element: an array of references, a tagged array
// such as ["Date", "..."], an object of references or a primitive. It
// returns an error when the element or a value it references cannot be
// decoded.
func (d *decoder) element(raw json.RawMessage) (any, error) {
	switch firstByte(raw) {
	case '[':
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, fmt.Errorf("devalue: %w", err)
		}
		if len(items) > 0 && firstByte(items[0]) == '"' {
			return d.tagged(items)
		}
		return d.references(items)
	case '{':
		var props map[string]int
		if err := json.Unmarshal(raw, &props); err != nil {
			return nil, fmt.Errorf("devalue: %w", err)
		}
		out := make(map[string]any, len(props))
		for key, index := range props {
			v, err := d.hydrate(index)
			if err != nil {
				return nil, err
			}
			out[key] = v
		}
		return out, nil
	default:
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("devalue: %w", err)
		}
		return v, nil
	}
}

// references hydrates every index in items, in order. It returns an error
// when an item is not an index or does not decode.
func (d *decoder) references(items []json.RawMessage) ([]any, error) {
	out := make([]any, 0, len(items))
	for _, item := range items {
		var index int
		if err := json.Unmarshal(item, &index); err != nil {
			return nil, fmt.Errorf("devalue: reference: %w", err)
		}
		v, err := d.hydrate(index)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// tagged decodes an array whose first item names a type: Date and BigInt
// become their string, Set an array, Map and null-prototype objects an
// object. Any other tag decodes as null. It returns an error when the items
// do not have the shape their tag calls for.
func (d *decoder) tagged(items []json.RawMessage) (any, error) {
	var tag string
	if err := json.Unmarshal(items[0], &tag); err != nil {
		return nil, fmt.Errorf("devalue: tag: %w", err)
	}
	switch tag {
	case "Date", "BigInt":
		if len(items) < 2 {
			return nil, fmt.Errorf("devalue: %s without a value", tag)
		}
		var s string
		if err := json.Unmarshal(items[1], &s); err != nil {
			return nil, fmt.Errorf("devalue: %s: %w", tag, err)
		}
		return s, nil
	case "Set":
		return d.references(items[1:])
	case "Map", "null":
		return d.pairs(items[1:], tag == "null")
	default:
		return nil, nil
	}
}

// pairs builds an object from alternating keys and values. With literalKeys
// the keys are the strings written in place; otherwise they are references,
// as in a Map, and a key that is not a string is written with %v. It returns
// an error when a key or value does not decode.
func (d *decoder) pairs(
	items []json.RawMessage,
	literalKeys bool,
) (any, error) {
	out := make(map[string]any, len(items)/2)
	for i := 0; i+1 < len(items); i += 2 {
		var key string
		if literalKeys {
			if err := json.Unmarshal(items[i], &key); err != nil {
				return nil, fmt.Errorf("devalue: key: %w", err)
			}
		} else {
			k, err := d.references(items[i : i+1])
			if err != nil {
				return nil, err
			}
			key = fmt.Sprint(k[0])
		}
		var index int
		if err := json.Unmarshal(items[i+1], &index); err != nil {
			return nil, fmt.Errorf("devalue: value: %w", err)
		}
		v, err := d.hydrate(index)
		if err != nil {
			return nil, err
		}
		out[key] = v
	}
	return out, nil
}

// firstByte returns the first non-space byte of raw, or 0 when there is none.
func firstByte(raw []byte) byte {
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		}
		return b
	}
	return 0
}
