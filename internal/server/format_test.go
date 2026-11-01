package server

import "testing"

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
