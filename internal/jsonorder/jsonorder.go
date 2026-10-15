// Package jsonorder decodes and encodes JSON objects without changing their
// key order, so that editing another tool's config file (for example
// ~/.claude/settings.json) changes only our own entries.
package jsonorder

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
)

// Map is a JSON object that keeps its key order.
type Map struct {
	Keys []string
	M    map[string]any
}

// NewMap returns an empty Map.
func NewMap() *Map { return &Map{M: map[string]any{}} }

// Get returns the value for k.
func (o *Map) Get(k string) (any, bool) { v, ok := o.M[k]; return v, ok }

// Set adds k at the end, or replaces its value in place.
func (o *Map) Set(k string, v any) {
	if _, ok := o.M[k]; !ok {
		o.Keys = append(o.Keys, k)
	}
	o.M[k] = v
}

// Delete removes k.
func (o *Map) Delete(k string) {
	if _, ok := o.M[k]; !ok {
		return
	}
	delete(o.M, k)
	if i := slices.Index(o.Keys, k); i >= 0 {
		o.Keys = slices.Delete(o.Keys, i, i+1)
	}
}

// MarshalJSON writes the keys in their order.
func (o *Map) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.Keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := Marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		vb, err := Marshal(o.M[k])
		if err != nil {
			return nil, err
		}
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// Marshal is json.Marshal without HTML escaping.
func Marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// MarshalIndent writes v with two-space indentation and no HTML escaping.
func MarshalIndent(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Decode parses JSON. Objects become *Map and numbers json.Number, so values
// are written back unchanged.
func Decode(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("unexpected data after the JSON value")
	}
	return v, nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return tok, nil
	}
	switch d {
	case '{':
		o := NewMap()
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			v, err := decodeValue(dec)
			if err != nil {
				return nil, err
			}
			k := kt.(string)
			if _, dup := o.M[k]; dup {
				// Tools disagree on which value wins; rewriting the file
				// would pick one silently.
				return nil, fmt.Errorf("duplicate key %q", k)
			}
			o.Set(k, v)
		}
		_, err := dec.Token()
		return o, err
	default: // '['
		arr := []any{}
		for dec.More() {
			v, err := decodeValue(dec)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		_, err := dec.Token()
		return arr, err
	}
}

// ReadFile reads a JSON object from path. A missing or empty file is an
// empty object.
func ReadFile(path string) (*Map, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) || (err == nil && len(bytes.TrimSpace(data)) == 0) {
		return NewMap(), nil
	}
	if err != nil {
		return nil, err
	}
	v, err := Decode(data)
	if err != nil {
		return nil, err
	}
	o, ok := v.(*Map)
	if !ok {
		return nil, errors.New("expected a JSON object")
	}
	return o, nil
}
