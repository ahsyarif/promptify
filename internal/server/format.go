package server

import (
	"html/template"
	"regexp"
	"strings"
)

func excerpt(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + "…"
}

var varPattern = regexp.MustCompile(`\{\{\s*[\w .-]+?\s*\}\}`)

// highlightVars escapes the prompt body and wraps {{placeholders}} in <mark>.
// Escaping first is what makes the returned HTML safe: braces and word
// characters are untouched by HTML escaping, so the pattern still matches.
func highlightVars(s string) template.HTML {
	escaped := template.HTMLEscapeString(s)
	return template.HTML(varPattern.ReplaceAllString(escaped, `<mark>$0</mark>`))
}
