package server

import (
	"html/template"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func excerpt(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + "…"
}

// formatIDR formats an amount of rupiah the Indonesian way: Rp49.000.
func formatIDR(amount int64) string {
	sign := ""
	if amount < 0 {
		sign, amount = "-", -amount
	}
	digits := strconv.FormatInt(amount, 10)
	var b strings.Builder
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(d)
	}
	return sign + "Rp" + b.String()
}

var wib = time.FixedZone("WIB", 7*60*60)

func formatWIB(t time.Time) string {
	return t.In(wib).Format("2 Jan 2006, 15:04 MST")
}

var varPattern = regexp.MustCompile(`\{\{\s*[\w .-]+?\s*\}\}`)

// highlightVars escapes the prompt body and wraps {{placeholders}} in <mark>.
// Escaping first is what makes the returned HTML safe: braces and word
// characters are untouched by HTML escaping, so the pattern still matches.
func highlightVars(s string) template.HTML {
	escaped := template.HTMLEscapeString(s)
	return template.HTML(varPattern.ReplaceAllString(escaped, `<mark>$0</mark>`))
}
