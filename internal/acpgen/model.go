package main

import (
	"fmt"
	"slices"
	"strings"
)

// kind is what a definition becomes in Go.
type kind int

const (
	kAlias      kind = iota // a named scalar, map or raw JSON
	kEnum                   // a string or integer type with constants
	kStruct                 // an object
	kTagged                 // a union told apart by a tag property
	kUntagged               // a union of objects told apart by their required properties
	kValue                  // a union of JSON value kinds (string, number, ...)
	kArrayUnion             // a union of arrays told apart by their items
)

// def is a definition as Go sees it.
type def struct {
	schemaName string
	name       string
	doc        string
	kind       kind

	under string // kAlias: the Go type; kEnum: string or an integer type
	alias bool   // kAlias: a type alias (json.RawMessage keeps its methods so)

	consts []*enumConst // kEnum
	open   bool         // kEnum, kTagged: values beyond the constants are allowed

	fields []*field // kStruct; kTagged: the properties beside the tag

	tag      string     // kTagged: the tag property
	tagField string     // kTagged: its Go field
	vars     []*variant // kTagged, kUntagged, kValue, kArrayUnion

	synthetic bool // made for a variant's inline properties

	unknownToDefault bool // kTagged: an unknown tag decodes as the default variant if it can
}

type enumConst struct {
	name, doc string
	value     any
}

type field struct {
	json, name, typ, doc string
	required             bool
	ptr                  bool // optional and held by pointer
	nullable             bool // Nullable[T]: absent, null and a value differ
	lenient              bool // a bad value decodes as absent
	skipInvalid          bool // bad items of an array are dropped
}

// variant is one case of a union.
type variant struct {
	tag      string // kTagged: the tag's value ("" for the default variant)
	def      bool   // kTagged: the variant without a tag
	name     string // its Go field
	typ      string // its Go type: a payload type, or "bool" for a variant without one
	doc      string
	required []string // kUntagged: the properties that pick it
	jsonKind string   // kValue: string, integer, number, boolean, array
	item     string   // kArrayUnion: the items' definition
}

// model is every definition of a schema, as Go types.
type model struct {
	s       *schema
	pkg     string
	defs    map[string]*def // by schema name
	order   []string        // schema names, sorted by Go name
	byGo    map[string]*def
	ordered map[string]string // "Def.prop" -> a hand-written ordered type
	null    map[string]bool   // "Def.prop": Nullable
	rawDefs map[string]bool   // definitions kept as raw JSON
}

func newModel(s *schema, pkg string) (*model, error) {
	m := &model{
		s: s, pkg: pkg, defs: map[string]*def{}, byGo: map[string]*def{},
		// JSON objects whose order matters: a form's fields.
		ordered: map[string]string{"ElicitationSchema.properties": "ElicitationProperties"},
		// Fields whose null clears a value that absence leaves as it is.
		null: map[string]bool{
			"SessionInfoUpdate.title": true, "SessionInfoUpdate.updatedAt": true,
			"SubagentUpdate.title": true, "SubagentUpdate.description": true, "SubagentUpdate.capabilities": true, "SubagentUpdate.state": true,
			"SessionMessage.content": true,
		},
		// A JSON-RPC id is passed on as it came.
		rawDefs: map[string]bool{"RequestId": true},
	}
	// Unions whose unknown tags the schema says decode as the variant
	// without one ("Unknown `type` values with string payloads also
	// gracefully deserialize into this variant").
	unknownToDefault := map[string]bool{"SetSessionConfigOptionRequest": true}
	for _, n := range s.names {
		if s.defs[n].DocsIgnore {
			continue
		}
		d := &def{schemaName: n, name: goName(n), doc: s.defs[n].Description, unknownToDefault: unknownToDefault[n]}
		m.defs[n] = d
	}
	for _, n := range s.names {
		if d := m.defs[n]; d != nil {
			d.kind = m.classify(d, s.defs[n])
		}
	}
	for _, n := range s.names {
		if d := m.defs[n]; d != nil {
			if err := m.build(d, s.defs[n]); err != nil {
				return nil, fmt.Errorf("%s: %w", n, err)
			}
		}
	}
	for n, d := range m.defs {
		if prev := m.byGo[d.name]; prev != nil {
			return nil, fmt.Errorf("%s and %s are both %s in Go", prev.schemaName, n, d.name)
		}
		m.byGo[d.name] = d
		m.order = append(m.order, n)
	}
	slices.SortFunc(m.order, func(a, b string) int { return strings.Compare(m.defs[a].name, m.defs[b].name) })
	return m, nil
}

// classify says what a definition becomes, before any is filled in (a
// field's type depends on the kind of the definition it refers to).
func (m *model) classify(d *def, n *node) kind {
	vs := variants(n)
	switch {
	case m.rawDefs[d.schemaName]:
		return kAlias
	case vs != nil && (allConst(vs, "string") || allConst(vs, "integer")):
		return kEnum
	case vs != nil && tagOf(vs) != "":
		return kTagged
	case vs != nil && n.Properties.m != nil:
		return kStruct
	case vs != nil && allObjects(m, vs):
		return kUntagged
	case vs != nil && allType(vs, "array"):
		return kArrayUnion
	case vs != nil:
		return kValue
	case n.Properties.m != nil, n.Type.nonNull() == "object" && n.AdditionalProperties == nil:
		return kStruct
	}
	return kAlias
}

// build fills in a definition.
func (m *model) build(d *def, n *node) error {
	vs := variants(n)
	switch {
	case m.rawDefs[d.schemaName]:
		d.kind, d.under, d.alias = kAlias, "json.RawMessage", true
	case vs != nil && allConst(vs, "string"):
		d.kind, d.under = kEnum, "string"
		m.enum(d, vs)
	case vs != nil && allConst(vs, "integer"):
		d.kind, d.under = kEnum, intType(vs[0].Format)
		m.enum(d, vs)
	case vs != nil && tagOf(vs) != "":
		return m.tagged(d, n, vs)
	case vs != nil && n.Properties.m != nil:
		d.kind = kStruct // properties and mixins (an elicitation's scope)
		m.fields(d, n, true)
		for _, v := range vs {
			m.fields(d, m.target(v), false)
		}
	case vs != nil && allObjects(m, vs):
		d.kind = kUntagged
		return m.untagged(d, vs)
	case vs != nil && allType(vs, "array"):
		d.kind = kArrayUnion
		for _, v := range vs {
			item := refName(v.Items)
			d.vars = append(d.vars, &variant{name: goName(v.Title), typ: "[]" + goName(item), doc: v.Description, item: item})
		}
	case vs != nil:
		d.kind = kValue
		for _, v := range vs {
			k := v.Type.nonNull()
			typ := m.scalar(v)
			if k != "array" {
				typ = "*" + typ
			}
			d.vars = append(d.vars, &variant{name: goName(v.Title), typ: typ, doc: v.Description, jsonKind: k})
		}
	case n.Properties.m != nil:
		d.kind = kStruct
		m.fields(d, n, true)
	case n.Type.nonNull() == "object" && n.AdditionalProperties == nil:
		d.kind = kStruct // an object with nothing in it yet, such as a capability: {} turns it on
	case n.Type.nonNull() != "":
		d.kind, d.under = kAlias, m.scalar(n)
	default:
		d.kind, d.under, d.alias = kAlias, "json.RawMessage", true
	}
	return nil
}

func (m *model) enum(d *def, vs []*node) {
	for _, v := range vs {
		if v.Const == nil {
			d.open = true // "other": any value
			continue
		}
		label := fmt.Sprint(v.Const)
		if v.Type.nonNull() == "integer" {
			label = v.Title
			v.Const = int64(v.Const.(float64)) //nolint:forcetypeassert // an integer const decodes as float64
		}
		d.consts = append(d.consts, &enumConst{name: d.name + goName(label), doc: v.Description, value: v.Const})
	}
}

// tagged fills in a union told apart by a const property.
func (m *model) tagged(d *def, n *node, vs []*node) error {
	d.kind = kTagged
	d.tag = tagOf(vs)
	d.tagField = goName(d.tag)
	if n.Properties.m != nil {
		m.fields(d, n, true)
	}
	for _, v := range vs {
		tp := v.Properties.get(d.tag)
		vr := &variant{doc: v.Description}
		switch {
		case tp == nil:
			vr.def = true
			vr.name = goName(v.Title)
		case tp.Const == nil:
			d.open = true // "other": its tag is any string
			continue
		default:
			vr.tag = tp.Const.(string) //nolint:forcetypeassert // tags are strings
			vr.name = goName(vr.tag)
		}
		payload := ""
		for _, a := range v.AllOf {
			if r := refName(a); r != "" {
				payload = goName(r)
			}
		}
		if payload == "" {
			if inline := withoutKey(v.Properties, d.tag); len(inline.keys) > 0 {
				s := &def{schemaName: d.schemaName + "." + vr.name, name: d.name + vr.name, kind: kStruct, synthetic: true,
					doc: fmt.Sprintf("%s is the %s variant of [%s].", d.name+vr.name, quoteTag(vr), d.name)}
				m.fields(s, &node{Properties: inline, Required: v.Required}, true)
				m.defs[s.schemaName] = s
				payload = s.name
			}
		}
		if payload == "" {
			payload = "bool"
		} else {
			payload = "*" + payload
		}
		vr.typ = payload
		d.vars = append(d.vars, vr)
	}
	for _, v := range d.vars {
		for _, f := range d.fields {
			if f.name == v.name || v.name == d.tagField || v.name == "Other" {
				return fmt.Errorf("variant %s's field clashes", v.name)
			}
		}
	}
	return nil
}

func quoteTag(v *variant) string {
	if v.def {
		return "untagged " + v.name
	}
	return fmt.Sprintf("%q", v.tag)
}

func (m *model) untagged(d *def, vs []*node) error {
	for _, v := range vs {
		t := m.target(v)
		vr := &variant{doc: v.Description, required: t.Required}
		if r := refName(v); r != "" {
			vr.name, vr.typ = goName(v.Title), "*"+goName(r)
			if vr.name == "" {
				vr.name = goName(r)
			}
		} else {
			vr.name = goName(v.Title)
			s := &def{schemaName: d.schemaName + "." + vr.name, name: d.name + vr.name, kind: kStruct, synthetic: true,
				doc: fmt.Sprintf("%s is the %s variant of [%s].", d.name+vr.name, strings.ToLower(vr.name), d.name)}
			m.fields(s, v, true)
			m.defs[s.schemaName] = s
			vr.typ = "*" + s.name
		}
		if len(vr.required) == 0 {
			return fmt.Errorf("variant %s has no required property to pick it by", vr.name)
		}
		d.vars = append(d.vars, vr)
	}
	return nil
}

// target is the schema a variant stands for: the definition it refers to,
// or itself.
func (m *model) target(v *node) *node {
	if r := refName(v); r != "" {
		return m.s.defs[r]
	}
	return v
}

func withoutKey(p props, key string) props {
	out := props{m: map[string]*node{}}
	for _, k := range p.keys {
		if k != key {
			out.keys = append(out.keys, k)
			out.m[k] = p.m[k]
		}
	}
	return out
}

// fields adds n's properties to d (required ones only when top: a mixin's
// properties are all optional).
func (m *model) fields(d *def, n *node, top bool) {
	if r := refName(n); r != "" && n.Properties.m == nil {
		n = m.s.defs[r]
	}
	for _, k := range n.Properties.keys {
		if slices.ContainsFunc(d.fields, func(f *field) bool { return f.json == k }) || k == d.tag {
			continue
		}
		pn := n.Properties.m[k]
		doc := pn.Description
		if k == "_meta" && strings.HasPrefix(doc, "The _meta property is reserved by ACP") {
			doc = metaDoc
		}
		f := &field{json: k, name: goName(k), doc: doc, required: top && slices.Contains(n.Required, k),
			lenient: pn.Lenient, skipInvalid: pn.SkipInvalid}
		key := d.schemaName + "." + k
		base := m.goType(pn)
		switch {
		case m.ordered[key] != "":
			f.typ = m.ordered[key]
		case m.null[key]:
			f.typ, f.nullable = "Nullable["+base+"]", true
		case !f.required && m.needsPtr(base):
			f.typ, f.ptr = "*"+base, true
		default:
			f.typ = base
		}
		d.fields = append(d.fields, f)
	}
	for _, a := range n.AllOf {
		m.fields(d, a, top)
	}
}

// goType is the Go type of a property's schema (a union with null is its
// other type: absent and null mean the same in ACP but where a Nullable
// says otherwise).
func (m *model) goType(n *node) string {
	if r := refName(n); r != "" {
		return goName(r)
	}
	if vs := variants(n); vs != nil {
		var rest []*node
		for _, v := range vs {
			if v.Type.nonNull() != "" || !v.Type.nullable() {
				rest = append(rest, v)
			}
		}
		if len(rest) == 1 {
			return m.goType(rest[0])
		}
		return "json.RawMessage"
	}
	return m.scalar(n)
}

// scalar is the Go type of a schema that isn't a reference or a union.
func (m *model) scalar(n *node) string {
	switch n.Type.nonNull() {
	case "string":
		return "string"
	case "integer":
		return intType(n.Format)
	case "number":
		return "float64"
	case "boolean":
		return "bool"
	case "array":
		if n.Items == nil {
			return "[]json.RawMessage"
		}
		return "[]" + m.goType(n.Items)
	case "object":
		if ap := n.AdditionalProperties; ap != nil {
			return "map[string]" + m.goType(ap)
		}
		return "map[string]any"
	}
	return "json.RawMessage"
}

func intType(format string) string {
	switch format {
	case "int8", "int16", "int32", "uint8", "uint16", "uint32", "uint64":
		return format
	case "uint":
		return "uint64"
	}
	return "int64"
}

// needsPtr reports whether an optional field of type t is held by pointer:
// a struct (so it can be absent), or a number (whose zero means something).
func (m *model) needsPtr(t string) bool {
	if isNumber(t) {
		return true
	}
	if d := m.byName(t); d != nil {
		switch d.kind {
		case kStruct, kTagged, kUntagged, kValue, kArrayUnion:
			return true
		case kAlias, kEnum:
			n := m.s.defs[d.schemaName]
			if vs := variants(n); vs != nil {
				return vs[0].Type.nonNull() == "integer"
			}
			return !m.rawDefs[d.schemaName] && isNumber(m.scalar(n))
		}
	}
	return false
}

func isNumber(t string) bool {
	return strings.HasPrefix(t, "int") || strings.HasPrefix(t, "uint") || t == "float64"
}

func (m *model) byName(goTyp string) *def {
	for _, d := range m.defs {
		if d.name == goTyp {
			return d
		}
	}
	return nil
}

func allConst(vs []*node, typ string) bool {
	for _, v := range vs {
		if v.Type.nonNull() != typ || (v.Const == nil && v.Title != "other" && v.Title != "Other") {
			return false
		}
	}
	return true
}

func allType(vs []*node, typ string) bool {
	for _, v := range vs {
		if v.Type.nonNull() != typ {
			return false
		}
	}
	return true
}

func allObjects(m *model, vs []*node) bool {
	for _, v := range vs {
		t := m.target(v)
		if t.Type.nonNull() != "object" && t.Properties.m == nil {
			return false
		}
	}
	return true
}

// tagOf is the property whose const tells a union's variants apart.
func tagOf(vs []*node) string {
	for _, v := range vs {
		for _, k := range v.Properties.keys {
			if v.Properties.m[k].Const != nil {
				return k
			}
		}
	}
	return ""
}

// metaDoc is the doc of every _meta field (the schema repeats a long one).
const metaDoc = "Meta is extension data: ACP reserves _meta for it, and nothing may be\nassumed about its keys. See [Extensibility].\n\n[Extensibility]: https://agentclientprotocol.com/protocol/extensibility"

func isUpper(c byte) bool { return c >= 'A' && c <= 'Z' }

var initialisms = map[string]string{
	"Id": "ID", "Url": "URL", "Uri": "URI", "Mcp": "MCP", "Http": "HTTP", "Sse": "SSE",
	"Json": "JSON", "Acp": "ACP", "Llm": "LLM", "Api": "API",
}

// goName turns a schema name (camelCase, snake_case, _meta, a title with
// spaces) into an exported Go name.
func goName(s string) string {
	var parts []string
	for _, w := range strings.FieldsFunc(s, func(r rune) bool { return r == '_' || r == '-' || r == '/' || r == '.' || r == ' ' || r == '$' }) {
		start := 0
		for i := 1; i <= len(w); i++ {
			if i == len(w) || (isUpper(w[i]) && !isUpper(w[i-1])) {
				parts = append(parts, w[start:i])
				start = i
			}
		}
	}
	var out strings.Builder
	for _, p := range parts {
		p = strings.ToUpper(p[:1]) + p[1:]
		if r, ok := initialisms[p]; ok {
			p = r
		}
		out.WriteString(p)
	}
	return out.String()
}
