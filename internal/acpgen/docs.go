package main

import (
	"bytes"
	"regexp"
	"strings"
)

const docsSite = "https://agentclientprotocol.com"

var (
	mdLink    = regexp.MustCompile(`\[([^\]\[]+)\]\(([^)\s]+)\)`)
	rustLink  = regexp.MustCompile("\\[`([A-Za-z_][A-Za-z0-9_:]*)`\\]")
	bold      = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	refLinkDf = regexp.MustCompile(`^\\?\[(\d+)\\?\]: (\S+)$`)
)

// comment writes doc as a Go comment, indented, or fallback if doc is
// empty. Markdown links become doc links, rustdoc's [`Name`] a link to
// the Go name where there is one, fenced code an indented block.
func (m *model) comment(b *bytes.Buffer, indent, doc, fallback string) {
	doc = strings.TrimSpace(doc)
	if doc == "" {
		doc = fallback
	}
	if doc == "" {
		return
	}
	var links []string
	seen := map[string]bool{}
	doc = mdLink.ReplaceAllStringFunc(doc, func(s string) string {
		p := mdLink.FindStringSubmatch(s)
		text, url := p[1], p[2]
		if strings.HasPrefix(url, "/") {
			url = docsSite + url
		}
		if strings.HasPrefix(url, "#") {
			return text
		}
		text = strings.Trim(text, "`")
		if !seen[text] {
			seen[text] = true
			links = append(links, "["+text+"]: "+url)
		}
		return "[" + text + "]"
	})
	doc = rustLink.ReplaceAllStringFunc(doc, func(s string) string {
		name := rustLink.FindStringSubmatch(s)[1]
		if i := strings.LastIndex(name, "::"); i >= 0 {
			name = name[i+2:]
		}
		if d := m.defs[name]; d != nil {
			return "[" + d.name + "]"
		}
		return name
	})
	doc = bold.ReplaceAllString(doc, "$1")

	var out []string
	inCode := false
	for line := range strings.SplitSeq(doc, "\n") {
		line = strings.TrimRight(line, " ")
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inCode = !inCode
			continue
		}
		if inCode {
			out = append(out, "\t"+line)
			continue
		}
		if p := refLinkDf.FindStringSubmatch(line); p != nil {
			links = append(links, "["+p[1]+"]: "+p[2])
			continue
		}
		out = append(out, strings.ReplaceAll(line, `\[`, "["))
	}
	if len(links) > 0 {
		out = append(out, "")
		out = append(out, links...)
	}
	for _, l := range out {
		if l == "" {
			b.WriteString(indent + "//\n")
		} else {
			b.WriteString(indent + "// " + strings.ReplaceAll(l, `\]`, "]") + "\n")
		}
	}
}
