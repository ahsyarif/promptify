package server

import (
	"testing"
	"time"
)

func TestHighlightVars(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Hello {{name}}!", "Hello <mark>{{name}}</mark>!"},
		{"{{ first name }} and {{last-name}}", "<mark>{{ first name }}</mark> and <mark>{{last-name}}</mark>"},
		{"<script>alert(1)</script> {{x}}", "&lt;script&gt;alert(1)&lt;/script&gt; <mark>{{x}}</mark>"},
		{`{{"><img src=x onerror=alert(1)>}}`, "{{&#34;&gt;&lt;img src=x onerror=alert(1)&gt;}}"},
		{"no placeholders", "no placeholders"},
		{"{{}} empty", "{{}} empty"},
	}
	for _, c := range cases {
		if got := string(highlightVars(c.in)); got != c.want {
			t.Errorf("highlightVars(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func TestFormatIDR(t *testing.T) {
	cases := map[int64]string{
		0:          "Rp0",
		999:        "Rp999",
		1000:       "Rp1.000",
		49000:      "Rp49.000",
		149000:     "Rp149.000",
		1250000:    "Rp1.250.000",
		-5000:      "-Rp5.000",
		1000000000: "Rp1.000.000.000",
	}
	for in, want := range cases {
		if got := formatIDR(in); got != want {
			t.Errorf("formatIDR(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatWIB(t *testing.T) {
	got := formatWIB(time.Date(2026, 11, 1, 10, 15, 0, 0, time.UTC))
	if got != "1 Nov 2026, 17:15 WIB" {
		t.Errorf("formatWIB = %q", got)
	}
}

func TestExcerpt(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"collapse\n\n  whitespace", 50, "collapse whitespace"},
		{"hello world", 5, "hello…"},
		{"héllo wörld", 7, "héllo w…"},
	}
	for _, c := range cases {
		if got := excerpt(c.in, c.n); got != c.want {
			t.Errorf("excerpt(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}
