package acp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// Nullable is a field whose null means something other than its absence:
// in an update, an absent field leaves a value as it was, null clears it,
// and a value replaces it. The zero Nullable is absent.
type Nullable[T any] struct {
	// Value is the value, or nil for null.
	Value *T
	// Present is false when the field is absent.
	Present bool
}

// IsZero reports whether n is absent, so that `omitzero` leaves it out.
func (n Nullable[T]) IsZero() bool { return !n.Present }

// MarshalJSON encodes the value, or null.
func (n Nullable[T]) MarshalJSON() ([]byte, error) {
	if n.Value == nil {
		return []byte("null"), nil
	}
	return marshal(n.Value)
}

// UnmarshalJSON decodes a present value or null.
func (n *Nullable[T]) UnmarshalJSON(b []byte) error {
	*n = Nullable[T]{Present: true}
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		return nil
	}
	n.Value = new(T)
	return json.Unmarshal(b, n.Value)
}

// ElicitationProperties are a form's fields in order, which a Go map would
// lose: on the wire they are an object, a field's name to its schema.
type ElicitationProperties []ElicitationProperty

// ElicitationProperty is one field of a form: its name (the key in the
// answer's content) and what it takes.
type ElicitationProperty struct {
	Name   string
	Schema ElicitationPropertySchema
}

// MarshalJSON encodes the fields as an object, in order.
func (p ElicitationProperties) MarshalJSON() ([]byte, error) {
	b := []byte{'{'}
	for i, f := range p {
		if i > 0 {
			b = append(b, ',')
		}
		v, err := marshal(f.Schema)
		if err != nil {
			return nil, err
		}
		b = append(append(appendString(b, f.Name), ':'), v...)
	}
	return append(b, '}'), nil
}

// UnmarshalJSON decodes an object's fields, keeping their order.
func (p *ElicitationProperties) UnmarshalJSON(b []byte) error {
	*p = nil
	dec := json.NewDecoder(bytes.NewReader(b))
	t, err := dec.Token()
	if err != nil {
		return err
	}
	if t == nil {
		return nil
	}
	if t != json.Delim('{') {
		return errors.New("acp: a form's properties aren't an object")
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		name, _ := t.(string)
		f := ElicitationProperty{Name: name}
		if err := dec.Decode(&f.Schema); err != nil {
			return err
		}
		*p = append(*p, f)
	}
	return nil
}

// lenient decodes raw into dst, leaving dst as it is if raw is absent or
// isn't a T: the schema's x-deserialize-default-on-error.
func lenient[T any](raw json.RawMessage, dst *T) {
	if len(raw) == 0 {
		return
	}
	var v T
	if json.Unmarshal(raw, &v) == nil {
		*dst = v
	}
}

// skipInvalid decodes the items of the array raw into dst, dropping those
// that aren't a T: the schema's x-deserialize-skip-invalid-items. Anything
// but an array leaves dst as it is.
func skipInvalid[T any](raw json.RawMessage, dst *[]T) {
	var items []json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &items) != nil || items == nil {
		return
	}
	out := make([]T, 0, len(items))
	for _, item := range items {
		var v T
		if json.Unmarshal(item, &v) == nil {
			out = append(out, v)
		}
	}
	*dst = out
}

// skipInvalidNullable is skipInvalid for a Nullable list: null is present
// without a value, and anything but an array or null leaves dst as it is.
//
//nolint:unused // only the unstable schema has such a list
func skipInvalidNullable[T any](raw json.RawMessage, dst *Nullable[[]T]) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		*dst = Nullable[[]T]{Present: true}
		return
	}
	var list []T
	if skipInvalid(raw, &list); list != nil {
		*dst = Nullable[[]T]{Value: &list, Present: true}
	}
}

// dropKey is the JSON object obj without key, its other keys in order: obj
// as it is when it isn't an object.
//
//nolint:unused // only the unstable schema has a union in a union
func dropKey(obj json.RawMessage, key string) json.RawMessage {
	if obj == nil {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(obj))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return obj
	}
	out := []byte{'{'}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return obj
		}
		k, _ := t.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return obj
		}
		if k == key {
			continue
		}
		if len(out) > 1 {
			out = append(out, ',')
		}
		out = append(append(appendString(out, k), ':'), v...)
	}
	return append(out, '}')
}

// requireKeys returns an error if the JSON object b lacks a property its
// type requires: nonNull ones may not be null either, mayBeNull ones may.
// b has decoded, so it is valid JSON; null, for the whole object, is left
// to whatever holds it.
func requireKeys(b []byte, typ string, nonNull, mayBeNull []string) error {
	var found, null uint64 // bit i: the ith of nonNull then mayBeNull
	n := len(nonNull) + len(mayBeNull)
	i := skipSpace(b, 0)
	if i == len(b) || b[i] != '{' {
		return nil
	}
	for i++; ; {
		i = skipSpace(b, i)
		if i >= len(b) || b[i] == '}' {
			break
		}
		if b[i] == ',' {
			i++
			continue
		}
		end := endOfString(b, i)
		key := b[i+1 : end-1]
		if bytes.IndexByte(key, '\\') >= 0 {
			var k string
			_ = json.Unmarshal(b[i:end], &k)
			key = []byte(k)
		}
		i = skipSpace(b, end) + 1 // the colon
		i = skipSpace(b, i)
		isNull := i < len(b) && b[i] == 'n'
		i = endOfValue(b, i)
		for k := range n {
			// string(key) == s doesn't allocate
			if k < len(nonNull) && nonNull[k] == string(key) || k >= len(nonNull) && mayBeNull[k-len(nonNull)] == string(key) {
				found |= 1 << k
				if isNull {
					null |= 1 << k
				}
			}
		}
	}
	for k, key := range nonNull {
		switch {
		case found&(1<<k) == 0:
			return fmt.Errorf("acp: a %s without %q", typ, key)
		case null&(1<<k) != 0:
			return fmt.Errorf("acp: a %s with %q null", typ, key)
		}
	}
	for k, key := range mayBeNull {
		if found&(1<<(k+len(nonNull))) == 0 {
			return fmt.Errorf("acp: a %s without %q", typ, key)
		}
	}
	return nil
}

func skipSpace(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	return i
}

// endOfString is the index just past the JSON string at b[i].
func endOfString(b []byte, i int) int {
	for j := i + 1; j < len(b); j++ {
		switch b[j] {
		case '\\':
			j++
		case '"':
			return j + 1
		}
	}
	return len(b)
}

// endOfValue is the index just past the JSON value at b[i].
func endOfValue(b []byte, i int) int {
	if i >= len(b) {
		return i
	}
	switch b[i] {
	case '"':
		return endOfString(b, i)
	case '{', '[':
		depth := 0
		for j := i; j < len(b); j++ {
			switch b[j] {
			case '"':
				j = endOfString(b, j) - 1
			case '{', '[':
				depth++
			case '}', ']':
				if depth--; depth == 0 {
					return j + 1
				}
			}
		}
		return len(b)
	}
	for i < len(b) && b[i] != ',' && b[i] != '}' && b[i] != ']' && b[i] != ' ' && b[i] != '\t' && b[i] != '\n' && b[i] != '\r' {
		i++
	}
	return i
}

// marshalUnion encodes a union's tag and the fields beside it (head), and
// its variant's fields (payload, nil for none) as one object.
func marshalUnion(head, payload any) ([]byte, error) {
	h, err := marshal(head)
	if err != nil {
		return nil, err
	}
	return appendPayload(h[:len(h)-1], payload, len(h) > 2)
}

// marshalTagged encodes a union with nothing beside its tag: the tag, then
// its variant's fields (payload, nil for none).
func marshalTagged(key, value string, payload any) ([]byte, error) {
	b := make([]byte, 0, 64)
	b = append(appendString(append(b, '{'), key), ':')
	return appendPayload(appendString(b, value), payload, true)
}

// appendPayload ends the object b (its fields so far, without the closing
// brace; comma says whether there are any) with payload's fields.
func appendPayload(b []byte, payload any, comma bool) ([]byte, error) {
	if payload == nil {
		return append(b, '}'), nil
	}
	p, err := marshal(payload)
	if err != nil {
		return nil, err
	}
	if len(p) <= 2 { // {}
		return append(b, '}'), nil
	}
	if comma {
		b = append(b, ',')
	}
	return append(b, p[1:]...), nil
}

func variantError(union string, n int) error {
	if n == 0 {
		return fmt.Errorf("acp: a %s with no variant set", union)
	}
	return fmt.Errorf("acp: a %s with %d variants set", union, n)
}

func missingVariant(union, tag string) error {
	return fmt.Errorf("acp: a %s tagged %q without its variant", union, tag)
}

// hasKeys reports whether an object has every one of keys.
func hasKeys(obj map[string]json.RawMessage, keys ...string) bool {
	for _, k := range keys {
		if _, ok := obj[k]; !ok {
			return false
		}
	}
	return true
}

// jsonKind is the kind of JSON value b holds: string, number, boolean,
// array, object, null, or "" when it is none.
func jsonKind(b []byte) string {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return ""
	}
	switch c := b[0]; {
	case c == '"':
		return "string"
	case c == '-' || (c >= '0' && c <= '9'):
		return "number"
	case c == 't' || c == 'f':
		return "boolean"
	case c == '[':
		return "array"
	case c == '{':
		return "object"
	case c == 'n':
		return "null"
	}
	return ""
}

func kindError(union string, b []byte) error {
	return fmt.Errorf("acp: a %s can't be a JSON %s", union, jsonKind(b))
}
