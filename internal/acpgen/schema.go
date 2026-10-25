package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// node is a JSON Schema node, as far as ACP's schema uses JSON Schema.
type node struct {
	Ref                  string   `json:"$ref"`
	Description          string   `json:"description"`
	Title                string   `json:"title"`
	Type                 typeList `json:"type"`
	Format               string   `json:"format"`
	Const                any      `json:"const"`
	Properties           props    `json:"properties"`
	Required             []string `json:"required"`
	Items                *node    `json:"items"`
	AllOf                []*node  `json:"allOf"`
	AnyOf                []*node  `json:"anyOf"`
	OneOf                []*node  `json:"oneOf"`
	AdditionalProperties *node    `json:"-"`
	DocsIgnore           bool     `json:"x-docs-ignore"`
	Lenient              bool     `json:"x-deserialize-default-on-error"`
	SkipInvalid          bool     `json:"x-deserialize-skip-invalid-items"`
	Method               string   `json:"x-method"`
	Side                 string   `json:"x-side"`
}

func (n *node) UnmarshalJSON(b []byte) error {
	type plain node
	var v struct {
		*plain
		AdditionalProperties json.RawMessage `json:"additionalProperties"`
	}
	v.plain = (*plain)(n)
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	// additionalProperties is a schema, or a boolean (true: any value).
	if len(v.AdditionalProperties) > 0 && v.AdditionalProperties[0] == '{' {
		n.AdditionalProperties = new(node)
		return json.Unmarshal(v.AdditionalProperties, n.AdditionalProperties)
	}
	return nil
}

// typeList is a node's "type": one name, or a list of them.
type typeList []string

func (t *typeList) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*t = typeList{s}
		return nil
	}
	return json.Unmarshal(b, (*[]string)(t))
}

// nonNull is the node's type other than "null", or "".
func (t typeList) nonNull() string {
	for _, s := range t {
		if s != "null" {
			return s
		}
	}
	return ""
}

func (t typeList) nullable() bool { return slices.Contains(t, "null") }

// props are an object's properties in the schema's order.
type props struct {
	keys []string
	m    map[string]*node
}

func (p *props) UnmarshalJSON(b []byte) error {
	keys, values, err := orderedObject(b)
	if err != nil {
		return err
	}
	p.keys, p.m = keys, map[string]*node{}
	for i, k := range keys {
		n := new(node)
		if err := json.Unmarshal(values[i], n); err != nil {
			return fmt.Errorf("property %s: %w", k, err)
		}
		p.m[k] = n
	}
	return nil
}

func (p props) get(k string) *node { return p.m[k] }

// orderedObject splits a JSON object into its keys, in order, and values.
func orderedObject(b []byte) (keys []string, values []json.RawMessage, err error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, nil, errors.New("not an object")
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, nil, err
		}
		k, _ := t.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, nil, err
		}
		keys = append(keys, k)
		values = append(values, v)
	}
	if _, err := dec.Token(); err != nil && !errors.Is(err, io.EOF) {
		return nil, nil, err
	}
	return keys, values, nil
}

// schema is a loaded schema file.
type schema struct {
	names []string // definitions, in the file's order
	defs  map[string]*node
}

func loadSchema(b []byte) (*schema, error) {
	var top struct {
		Defs json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(b, &top); err != nil {
		return nil, err
	}
	keys, values, err := orderedObject(top.Defs)
	if err != nil {
		return nil, err
	}
	s := &schema{names: keys, defs: map[string]*node{}}
	for i, k := range keys {
		n := new(node)
		if err := json.Unmarshal(values[i], n); err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		s.defs[k] = n
	}
	return s, nil
}

// refName is the definition a node stands for: its $ref, or the one $ref of
// an allOf with nothing beside it.
func refName(n *node) string {
	if n.Ref != "" {
		return strings.TrimPrefix(n.Ref, "#/$defs/")
	}
	if len(n.AllOf) == 1 && n.Properties.m == nil && n.Type == nil {
		return refName(n.AllOf[0])
	}
	return ""
}

func variants(n *node) []*node {
	if n.OneOf != nil {
		return n.OneOf
	}
	return n.AnyOf
}
