package main

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
)

// genTypes writes every definition as Go.
func genTypes(b *bytes.Buffer, m *model) {
	for _, n := range m.order {
		d := m.defs[n]
		m.comment(b, "", d.doc, d.name+" is the schema's "+d.schemaName+".")
		switch d.kind {
		case kAlias:
			eq := " "
			if d.alias {
				eq = " = "
			}
			fmt.Fprintf(b, "type %s%s%s\n\n", d.name, eq, d.under)
		case kEnum:
			genEnum(b, m, d)
		case kStruct:
			genStruct(b, m, d)
		case kTagged:
			genTagged(b, m, d)
		case kUntagged:
			genUntagged(b, m, d)
		case kValue:
			genValue(b, m, d)
		case kArrayUnion:
			genArrayUnion(b, m, d)
		}
	}
}

func genEnum(b *bytes.Buffer, m *model, d *def) {
	fmt.Fprintf(b, "type %s %s\n\n", d.name, d.under)
	if d.open {
		fmt.Fprintf(b, "// The values of %s that ACP defines. Others may come from a newer peer or an extension.\n", d.name)
	} else {
		fmt.Fprintf(b, "// The values of %s.\n", d.name)
	}
	b.WriteString("const (\n")
	for _, c := range d.consts {
		m.comment(b, "\t", c.doc, "")
		if s, ok := c.value.(string); ok {
			fmt.Fprintf(b, "\t%s %s = %q\n", c.name, d.name, s)
		} else {
			fmt.Fprintf(b, "\t%s %s = %v\n", c.name, d.name, c.value)
		}
	}
	b.WriteString(")\n\n")
}

func jsonTag(f *field) string {
	switch {
	case f.required:
		return f.json
	case f.nullable:
		return f.json + ",omitzero"
	}
	return f.json + ",omitempty"
}

func writeFields(b *bytes.Buffer, m *model, fields []*field) {
	for _, f := range fields {
		m.comment(b, "\t", f.doc, "")
		fmt.Fprintf(b, "\t%s %s `json:%q`\n", f.name, f.typ, jsonTag(f))
	}
}

func genStruct(b *bytes.Buffer, m *model, d *def) {
	fmt.Fprintf(b, "type %s struct {\n", d.name)
	writeFields(b, m, d.fields)
	b.WriteString("}\n\n")
	genEmptyLists(b, d)
	if lenientFields(d.fields) != nil {
		fmt.Fprintf(b, "// UnmarshalJSON decodes %s, dropping an optional field's bad value (or an\n// array's bad items) where the schema says to, rather than failing.\n", d.name)
		fmt.Fprintf(b, "func (x *%s) UnmarshalJSON(b []byte) error {\n\ttype plain %s\n", d.name, d.name)
		writeLenientDecode(b, d, "x")
		b.WriteString("\treturn nil\n}\n\n")
	}
}

// genEmptyLists writes a MarshalJSON that sends a required list or map left
// nil as an empty one, not null.
func genEmptyLists(b *bytes.Buffer, d *def) {
	var lists []*field
	for _, f := range d.fields {
		if f.required && (strings.HasPrefix(f.typ, "[]") || strings.HasPrefix(f.typ, "map[")) {
			lists = append(lists, f)
		}
	}
	if lists == nil {
		return
	}
	fmt.Fprintf(b, "// MarshalJSON encodes %s, sending a required list left nil as an empty one.\n", d.name)
	fmt.Fprintf(b, "func (x %s) MarshalJSON() ([]byte, error) {\n\ttype plain %s\n\tp := plain(x)\n", d.name, d.name)
	for _, f := range lists {
		fmt.Fprintf(b, "\tif p.%s == nil {\n\t\tp.%s = %s{}\n\t}\n", f.name, f.name, f.typ)
	}
	b.WriteString("\treturn json.Marshal(p)\n}\n\n")
}

func lenientFields(fields []*field) []*field {
	var out []*field
	for _, f := range fields {
		if f.lenient || f.skipInvalid {
			out = append(out, f)
		}
	}
	return out
}

// writeLenientDecode decodes b into recv (of type plain's), the quick way
// first and, if that fails, again field by field for the lenient ones.
func writeLenientDecode(b *bytes.Buffer, d *def, recv string) {
	fmt.Fprintf(b, "\tif err := json.Unmarshal(b, (*plain)(%s)); err == nil {\n\t\treturn nil\n\t}\n", recv)
	fmt.Fprintf(b, "\t*%s = %s{}\n\tvar v struct {\n\t\t*plain\n", recv, d.name)
	for _, f := range lenientFields(d.fields) {
		fmt.Fprintf(b, "\t\t%s json.RawMessage `json:%q`\n", f.name, f.json)
	}
	fmt.Fprintf(b, "\t}\n\tv.plain = (*plain)(%s)\n\tif err := json.Unmarshal(b, &v); err != nil {\n\t\treturn err\n\t}\n", recv)
	for _, f := range lenientFields(d.fields) {
		if f.skipInvalid && strings.HasPrefix(f.typ, "[]") {
			fmt.Fprintf(b, "\tskipInvalid(v.%s, &%s.%s)\n", f.name, recv, f.name)
		} else {
			fmt.Fprintf(b, "\tlenient(v.%s, &%s.%s)\n", f.name, recv, f.name)
		}
	}
}

func genTagged(b *bytes.Buffer, m *model, d *def) {
	hasDefault := slices.ContainsFunc(d.vars, func(v *variant) bool { return v.def })
	fmt.Fprintf(b, "type %s struct {\n", d.name)
	writeFields(b, m, d.fields)
	tagOpt := ""
	if hasDefault {
		tagOpt = ",omitempty"
	}
	fmt.Fprintf(b, "\t// %s is the %q tag, which says which variant this is. MarshalJSON\n\t// fills it in from the variant set when it is empty.\n", d.tagField, d.tag)
	fmt.Fprintf(b, "\t%s string `json:\"%s%s\"`\n", d.tagField, d.tag, tagOpt)
	for _, v := range d.vars {
		what := fmt.Sprintf("the %q variant", v.tag)
		if v.def {
			what = fmt.Sprintf("the variant without a %q tag", d.tag)
		}
		if v.typ == "bool" {
			what += " (it has no fields of its own)"
		}
		m.comment(b, "\t", fmt.Sprintf("%s is %s. %s", v.name, what, v.doc), "")
		fmt.Fprintf(b, "\t%s %s `json:\"-\"`\n", v.name, v.typ)
	}
	b.WriteString(otherDoc)
	b.WriteString("}\n\n")

	// which
	fmt.Fprintf(b, "// which is the tag of the variant set, and how many are set.\nfunc (x *%s) which() (tag string, n int) {\n", d.name)
	for _, v := range d.vars {
		cond := "x." + v.name + " != nil"
		if v.typ == "bool" {
			cond = "x." + v.name
		}
		fmt.Fprintf(b, "\tif %s {\n\t\ttag, n = %q, n+1\n\t}\n", cond, v.tag)
	}
	b.WriteString("\treturn tag, n\n}\n\n")

	// MarshalJSON
	fmt.Fprintf(b, "// MarshalJSON encodes the variant set, with its tag, as one object.\nfunc (x %s) MarshalJSON() ([]byte, error) {\n\ttype plain %s\n", d.name, d.name)
	fmt.Fprintf(b, "\tif x.%s == \"\" && x.Other == nil {\n\t\ttag, n := x.which()\n\t\tif n != 1 {\n\t\t\treturn nil, variantError(%q, n)\n\t\t}\n\t\tx.%s = tag\n\t}\n", d.tagField, d.name, d.tagField)
	fmt.Fprintf(b, "\tswitch x.%s {\n", d.tagField)
	encode := func(payload string) string { return "marshalUnion(plain(x), " + payload + ")" }
	if len(d.fields) == 0 && !slices.ContainsFunc(d.vars, func(v *variant) bool { return v.def }) {
		encode = func(payload string) string { return fmt.Sprintf("marshalTagged(%q, x.%s, %s)", d.tag, d.tagField, payload) }
	}
	for _, v := range d.vars {
		if v.def {
			continue
		}
		fmt.Fprintf(b, "\tcase %q:\n", v.tag)
		if v.typ == "bool" {
			b.WriteString("\t\treturn " + encode("nil") + "\n")
		} else {
			fmt.Fprintf(b, "\t\tif x.%s == nil {\n\t\t\treturn nil, missingVariant(%q, %q)\n\t\t}\n\t\treturn %s\n", v.name, d.name, v.tag, encode("x."+v.name))
		}
	}
	b.WriteString("\t}\n\tif x.Other != nil {\n\t\treturn x.Other, nil\n\t}\n")
	for _, v := range d.vars {
		if v.def {
			fmt.Fprintf(b, "\tif x.%s != nil {\n\t\treturn marshalUnion(plain(x), x.%s)\n\t}\n", v.name, v.name)
		}
	}
	b.WriteString("\treturn marshalUnion(plain(x), nil)\n}\n\n")

	// UnmarshalJSON
	fmt.Fprintf(b, "// UnmarshalJSON decodes the variant its tag names; an unknown tag is kept\n// whole in Other.\nfunc (x *%s) UnmarshalJSON(b []byte) error {\n", d.name)
	fmt.Fprintf(b, "\tif err := x.decodeTag(b); err != nil {\n\t\treturn err\n\t}\n\tswitch x.%s {\n", d.tagField)
	var def *variant
	for _, v := range d.vars {
		if v.def {
			def = v
			continue
		}
		fmt.Fprintf(b, "\tcase %q:\n", v.tag)
		if v.typ == "bool" {
			fmt.Fprintf(b, "\t\tx.%s = true\n\t\treturn nil\n", v.name)
		} else {
			fmt.Fprintf(b, "\t\tx.%s = new(%s)\n\t\treturn json.Unmarshal(b, x.%s)\n", v.name, strings.TrimPrefix(v.typ, "*"), v.name)
		}
	}
	switch {
	case def != nil && d.unknownToDefault:
		fmt.Fprintf(b, "\tdefault:\n\t\tx.%s = new(%s)\n\t\tif err := json.Unmarshal(b, x.%s); err == nil || x.%s == \"\" {\n\t\t\treturn err\n\t\t}\n\t\tx.%s = nil\n", def.name, strings.TrimPrefix(def.typ, "*"), def.name, d.tagField, def.name)
	case def != nil:
		fmt.Fprintf(b, "\tcase \"\":\n\t\tx.%s = new(%s)\n\t\treturn json.Unmarshal(b, x.%s)\n", def.name, strings.TrimPrefix(def.typ, "*"), def.name)
	}
	b.WriteString("\t}\n\tx.Other = append(json.RawMessage(nil), b...)\n\treturn nil\n}\n\n")

	fmt.Fprintf(b, "// decodeTag decodes the tag and the fields beside it.\nfunc (x *%s) decodeTag(b []byte) error {\n\ttype plain %s\n", d.name, d.name)
	if lenientFields(d.fields) != nil {
		fmt.Fprintf(b, "\t*x = %s{}\n", d.name)
		writeLenientDecode(b, d, "x")
		b.WriteString("\treturn nil\n}\n\n")
	} else {
		fmt.Fprintf(b, "\t*x = %s{}\n\treturn json.Unmarshal(b, (*plain)(x))\n}\n\n", d.name)
	}
}

const otherDoc = "\t// Other is the whole object when its tag is one this package doesn't\n" +
	"\t// know (a newer peer's), kept as it came so it can be passed on.\n" +
	"\t// MarshalJSON writes it as it is.\n" +
	"\tOther json.RawMessage `json:\"-\"`\n"

func genUntagged(b *bytes.Buffer, m *model, d *def) {
	fmt.Fprintf(b, "type %s struct {\n", d.name)
	for _, v := range d.vars {
		m.comment(b, "\t", fmt.Sprintf("%s is set for an object with %s. %s", v.name, listQuoted(v.required), v.doc), "")
		fmt.Fprintf(b, "\t%s %s\n", v.name, v.typ)
	}
	b.WriteString(strings.Replace(otherDoc, "when its tag is one this package doesn't\n\t// know", "when it is none of the variants this package\n\t// knows", 1))
	b.WriteString("}\n\n")
	fmt.Fprintf(b, "// MarshalJSON encodes the variant set.\nfunc (x %s) MarshalJSON() ([]byte, error) {\n\tswitch {\n", d.name)
	for _, v := range d.vars {
		fmt.Fprintf(b, "\tcase x.%s != nil:\n\t\treturn json.Marshal(x.%s)\n", v.name, v.name)
	}
	fmt.Fprintf(b, "\tcase x.Other != nil:\n\t\treturn x.Other, nil\n\t}\n\treturn nil, variantError(%q, 0)\n}\n\n", d.name)
	fmt.Fprintf(b, "// UnmarshalJSON decodes the variant whose required properties b has.\nfunc (x *%s) UnmarshalJSON(b []byte) error {\n\t*x = %s{}\n", d.name, d.name)
	b.WriteString("\tvar keys map[string]json.RawMessage\n\tif err := json.Unmarshal(b, &keys); err != nil {\n\t\treturn err\n\t}\n\tswitch {\n")
	vars := slices.Clone(d.vars)
	slices.SortStableFunc(vars, func(a, b *variant) int { return len(b.required) - len(a.required) })
	for _, v := range vars {
		fmt.Fprintf(b, "\tcase hasKeys(keys, %s):\n\t\tx.%s = new(%s)\n\t\treturn json.Unmarshal(b, x.%s)\n", quotedArgs(v.required), v.name, strings.TrimPrefix(v.typ, "*"), v.name)
	}
	b.WriteString("\t}\n\tx.Other = append(json.RawMessage(nil), b...)\n\treturn nil\n}\n\n")
}

func genValue(b *bytes.Buffer, m *model, d *def) {
	fmt.Fprintf(b, "// Set one field; none is null.\ntype %s struct {\n", d.name)
	for _, v := range d.vars {
		m.comment(b, "\t", v.doc, "")
		fmt.Fprintf(b, "\t%s %s\n", v.name, v.typ)
	}
	b.WriteString("}\n\n")
	fmt.Fprintf(b, "// MarshalJSON encodes the value set, or null.\nfunc (x %s) MarshalJSON() ([]byte, error) {\n\tswitch {\n", d.name)
	for _, v := range d.vars {
		fmt.Fprintf(b, "\tcase x.%s != nil:\n\t\treturn json.Marshal(x.%s)\n", v.name, v.name)
	}
	b.WriteString("\t}\n\treturn []byte(\"null\"), nil\n}\n\n")
	fmt.Fprintf(b, "// UnmarshalJSON decodes b into the field for its JSON kind.\nfunc (x *%s) UnmarshalJSON(b []byte) error {\n\t*x = %s{}\n\tswitch jsonKind(b) {\n", d.name, d.name)
	for _, v := range d.vars {
		switch v.jsonKind {
		case "integer":
			fmt.Fprintf(b, "\tcase \"number\":\n\t\tvar i %s\n\t\tif json.Unmarshal(b, &i) == nil {\n\t\t\tx.%s = &i\n\t\t\treturn nil\n\t\t}\n", strings.TrimPrefix(v.typ, "*"), v.name)
			for _, w := range d.vars {
				if w.jsonKind == "number" {
					fmt.Fprintf(b, "\t\tx.%s = new(%s)\n\t\treturn json.Unmarshal(b, x.%s)\n", w.name, strings.TrimPrefix(w.typ, "*"), w.name)
				}
			}
		case "number":
			// with integer
		case "array":
			fmt.Fprintf(b, "\tcase \"array\":\n\t\treturn json.Unmarshal(b, &x.%s)\n", v.name)
		default:
			fmt.Fprintf(b, "\tcase %q:\n\t\tx.%s = new(%s)\n\t\treturn json.Unmarshal(b, x.%s)\n", v.jsonKind, v.name, strings.TrimPrefix(v.typ, "*"), v.name)
		}
	}
	fmt.Fprintf(b, "\tcase \"null\":\n\t\treturn nil\n\t}\n\treturn kindError(%q, b)\n}\n\n", d.name)
}

func genArrayUnion(b *bytes.Buffer, m *model, d *def) {
	fmt.Fprintf(b, "type %s struct {\n", d.name)
	for _, v := range d.vars {
		m.comment(b, "\t", v.doc, "")
		fmt.Fprintf(b, "\t%s %s\n", v.name, v.typ)
	}
	b.WriteString("}\n\n")
	fmt.Fprintf(b, "// MarshalJSON encodes the list set (an empty list when none is).\nfunc (x %s) MarshalJSON() ([]byte, error) {\n\tswitch {\n", d.name)
	for _, v := range d.vars {
		fmt.Fprintf(b, "\tcase x.%s != nil:\n\t\treturn json.Marshal(x.%s)\n", v.name, v.name)
	}
	b.WriteString("\t}\n\treturn []byte(\"[]\"), nil\n}\n\n")
	fmt.Fprintf(b, "// UnmarshalJSON decodes b as the list whose items have the first item's\n// required properties.\nfunc (x *%s) UnmarshalJSON(b []byte) error {\n\t*x = %s{}\n", d.name, d.name)
	b.WriteString("\tvar items []map[string]json.RawMessage\n\tif err := json.Unmarshal(b, &items); err != nil {\n\t\treturn err\n\t}\n\tswitch {\n")
	vars := slices.Clone(d.vars)
	slices.SortStableFunc(vars, func(a, b *variant) int {
		return len(m.s.defs[b.item].Required) - len(m.s.defs[a.item].Required)
	})
	for i, v := range vars {
		if i == len(vars)-1 {
			b.WriteString("\tdefault:\n")
		} else {
			fmt.Fprintf(b, "\tcase len(items) > 0 && hasKeys(items[0], %s):\n", quotedArgs(m.s.defs[v.item].Required))
		}
		fmt.Fprintf(b, "\t\treturn json.Unmarshal(b, &x.%s)\n", v.name)
	}
	b.WriteString("\t}\n}\n\n")
}

func listQuoted(keys []string) string {
	q := make([]string, len(keys))
	for i, k := range keys {
		q[i] = fmt.Sprintf("%q", k)
	}
	return strings.Join(q, ", ")
}

func quotedArgs(keys []string) string { return listQuoted(keys) }
