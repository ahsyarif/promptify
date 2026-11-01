package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/ahsyarif/promptify/internal/payment/midtrans"
	"github.com/ahsyarif/promptify/internal/store"
)

func TestPasswordHashing(t *testing.T) {
	h, err := hashPassword("s3cret-passw0rd")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(h, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" || parts[1] != fmt.Sprint(passwordIterations) {
		t.Fatalf("unexpected hash format %q", h)
	}
	if !checkPassword(h, "s3cret-passw0rd") {
		t.Fatal("correct password rejected")
	}
	if checkPassword(h, "s3cret-passw0rd ") || checkPassword(h, "") {
		t.Fatal("wrong password accepted")
	}
	if h2, _ := hashPassword("s3cret-passw0rd"); h2 == h {
		t.Fatal("hashes must be salted")
	}

	old := passwordIterations
	passwordIterations = old * 2
	defer func() { passwordIterations = old }()
	if !checkPassword(h, "s3cret-passw0rd") {
		t.Fatal("raising the work factor must not break existing hashes")
	}

	for _, bad := range []string{"", "plain", "bcrypt$10$x$y", "pbkdf2-sha256$0$c2FsdA$aGFzaA", "pbkdf2-sha256$x$c2FsdA$aGFzaA", "pbkdf2-sha256$1000$!!$aGFzaA", "pbkdf2-sha256$1000$c2FsdA$"} {
		if checkPassword(bad, "anything") {
			t.Errorf("malformed hash %q accepted", bad)
		}
	}
}

func TestSafeNext(t *testing.T) {
	cases := map[string]string{
		"":                    "/",
		"/":                   "/",
		"/membership":         "/membership",
		"/prompts/1?x=1#top":  "/prompts/1?x=1#top",
		"//evil.com":          "/",
		"///evil.com":         "/",
		`/\evil.com`:          "/",
		`/foo\bar`:            "/",
		"https://evil.com":    "/",
		"javascript:alert(1)": "/",
		"evil.com":            "/",
		"/ok\r\nSet-Cookie:x": "/",
		"/\x00":               "/",
	}
	for in, want := range cases {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateCredentials(t *testing.T) {
	cases := []struct {
		email, password string
		wantKeys        []string
	}{
		{"a@b.co", "12345678", nil},
		{"", "12345678", []string{"email"}},
		{"no-at-sign", "12345678", []string{"email"}},
		{"@b.co", "12345678", []string{"email"}},
		{"a@", "12345678", []string{"email"}},
		{"a b@c.co", "12345678", []string{"email"}},
		{strings.Repeat("a", 250) + "@b.co", "12345678", []string{"email"}},
		{"a@b.co", "1234567", []string{"password"}},
		{"a@b.co", strings.Repeat("x", 73), []string{"password"}},
		{"a@b.co", strings.Repeat("é", 72), nil},
	}
	for _, c := range cases {
		errs := validateCredentials(c.email, c.password)
		if len(errs) != len(c.wantKeys) {
			t.Errorf("validateCredentials(%q, len %d) = %v, want keys %v", c.email, len(c.password), errs, c.wantKeys)
			continue
		}
		for _, k := range c.wantKeys {
			if errs[k] == "" {
				t.Errorf("validateCredentials(%q): missing %s error", c.email, k)
			}
		}
	}
}

func TestSignupLoginLogout(t *testing.T) {
	app := newTestApp(t)
	c := app.client()

	code, h, _ := app.postForm(c, "/signup", url.Values{"email": {"  Alice@Example.com "}, "password": {testPassword}, "next": {"/membership"}}, nil)
	if code != http.StatusSeeOther || h.Get("Location") != "/membership" {
		t.Fatalf("signup: code=%d location=%q", code, h.Get("Location"))
	}
	var session *http.Cookie
	for _, ck := range strings.Split(strings.Join(h.Values("Set-Cookie"), "\n"), "\n") {
		if strings.HasPrefix(ck, sessionCookie+"=") {
			session, _ = http.ParseSetCookie(ck)
		}
	}
	if session == nil || !session.HttpOnly || session.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie should be HttpOnly and SameSite=Lax: %+v", session)
	}
	if _, page := app.get(c, "/", false); !strings.Contains(page, "alice@example.com") || !strings.Contains(page, "Log out") {
		t.Fatal("logged-in nav should show the (normalised) email and a logout button")
	}
	if code, h, _ := app.do(c, http.MethodGet, "/login", nil, nil); code != http.StatusSeeOther || h.Get("Location") != "/" {
		t.Fatalf("logged-in users visiting /login should be redirected home, got %d", code)
	}

	if code, _, _ := app.postForm(c, "/logout", nil, nil); code != http.StatusSeeOther {
		t.Fatalf("logout: code=%d", code)
	}
	if _, page := app.get(c, "/", false); strings.Contains(page, "alice@example.com") || !strings.Contains(page, `href="/login"`) {
		t.Fatal("after logout the nav should offer login again")
	}

	code, _, page := app.postForm(c, "/login", url.Values{"email": {"alice@example.com"}, "password": {"wrong password"}}, nil)
	if code != http.StatusUnprocessableEntity || !strings.Contains(page, msgBadCredentials) {
		t.Fatalf("wrong password: code=%d", code)
	}
	code, _, page = app.postForm(c, "/login", url.Values{"email": {"nobody@example.com"}, "password": {testPassword}}, nil)
	if code != http.StatusUnprocessableEntity || !strings.Contains(page, msgBadCredentials) {
		t.Fatalf("unknown email must get the same generic error: code=%d", code)
	}
	if !strings.Contains(page, `value="nobody@example.com"`) {
		t.Fatal("the email should be kept in the form")
	}

	code, h, _ = app.postForm(c, "/login", url.Values{"email": {"ALICE@example.com"}, "password": {testPassword}, "next": {"//evil.com"}}, nil)
	if code != http.StatusSeeOther || h.Get("Location") != "/" {
		t.Fatalf("login with an off-site next: code=%d location=%q", code, h.Get("Location"))
	}
	if _, page := app.get(c, "/", false); !strings.Contains(page, "alice@example.com") {
		t.Fatal("login should start a session")
	}
}

func TestSignupValidation(t *testing.T) {
	app := newTestApp(t)
	app.signup("taken@example.com")

	cases := []struct {
		email, password, want string
	}{
		{"TAKEN@example.com", testPassword, "already exists"},
		{"not-an-email", testPassword, "valid email"},
		{"new@example.com", "short", "at least 8"},
	}
	for _, tc := range cases {
		code, _, page := app.postForm(app.client(), "/signup", url.Values{"email": {tc.email}, "password": {tc.password}}, nil)
		if code != http.StatusUnprocessableEntity || !strings.Contains(page, tc.want) {
			t.Errorf("signup(%q): code=%d, want 422 with %q", tc.email, code, tc.want)
		}
	}
}

func TestExpiredOrUnknownSessionCookie(t *testing.T) {
	app := newTestApp(t)
	c := app.client()
	u, _ := url.Parse(app.srv.URL)
	c.Jar.SetCookies(u, []*http.Cookie{{Name: sessionCookie, Value: "forged-token"}})
	code, h, page := app.do(c, http.MethodGet, "/", nil, nil)
	if code != http.StatusOK || strings.Contains(page, "Log out") {
		t.Fatalf("unknown session must be treated as anonymous: code=%d", code)
	}
	if !strings.Contains(strings.Join(h.Values("Set-Cookie"), ";"), sessionCookie+"=;") {
		t.Fatal("an invalid session cookie should be cleared")
	}
}

func TestAdminOnlyRoutes(t *testing.T) {
	app := newTestApp(t)
	id, err := app.store.CreatePrompt(context.Background(), store.PromptInput{Title: "Keep me", Body: "b"})
	if err != nil {
		t.Fatal(err)
	}
	prompt := fmt.Sprintf("/prompts/%d", id)
	form := url.Values{"title": {"Hacked"}, "body": {"b"}, "name": {"Hacked"}}

	type req struct{ method, path string }
	routes := []req{
		{http.MethodGet, "/prompts/new"},
		{http.MethodPost, "/prompts"},
		{http.MethodGet, prompt + "/edit"},
		{http.MethodPost, prompt},
		{http.MethodPost, prompt + "/delete"},
		{http.MethodPost, "/categories"},
	}
	send := func(c *http.Client, r req, htmx bool) (int, http.Header) {
		h := map[string]string{}
		if htmx {
			h["HX-Request"] = "true"
		}
		if r.method == http.MethodGet {
			code, hdr, _ := app.do(c, r.method, r.path, nil, h)
			return code, hdr
		}
		code, hdr, _ := app.postForm(c, r.path, form, h)
		return code, hdr
	}

	anon := app.client()
	for _, r := range routes {
		code, h := send(anon, r, false)
		want := "/login?next=%2F"
		if r.method == http.MethodGet {
			want = "/login?next=" + url.QueryEscape(r.path)
		}
		if code != http.StatusSeeOther || h.Get("Location") != want {
			t.Errorf("anonymous %s %s: code=%d location=%q, want 303 to %s", r.method, r.path, code, h.Get("Location"), want)
		}
	}
	if code, h := send(anon, req{http.MethodPost, prompt + "/delete"}, true); code != http.StatusNoContent || !strings.HasPrefix(h.Get("HX-Redirect"), "/login") {
		t.Errorf("anonymous htmx delete: code=%d HX-Redirect=%q", code, h.Get("HX-Redirect"))
	}

	user := app.signup("user@example.com")
	for _, r := range routes {
		if code, _ := send(user, r, false); code != http.StatusNotFound {
			t.Errorf("non-admin %s %s: code=%d, want 404", r.method, r.path, code)
		}
	}
	if p, err := app.store.GetPrompt(context.Background(), id, ""); err != nil || p.Title != "Keep me" {
		t.Fatalf("non-admins must not change content: %+v, %v", p, err)
	}
	if cats, _ := app.store.ListCategories(context.Background()); len(cats) != 5 {
		t.Fatalf("non-admins must not create categories, have %d", len(cats))
	}
	for _, path := range []string{"/", prompt, "/categories"} {
		_, page := app.get(user, path, false)
		if strings.Contains(page, `href="/prompts/new"`) || strings.Contains(page, "/edit\"") || strings.Contains(page, "New category") {
			t.Errorf("%s shows admin controls to a non-admin", path)
		}
	}

	admin := app.admin()
	if code, page := app.get(admin, "/prompts/new", false); code != http.StatusOK || !strings.Contains(page, `name="is_premium"`) {
		t.Fatalf("admin new prompt form: code=%d", code)
	}
	_, page := app.get(admin, prompt, false)
	if !strings.Contains(page, prompt+"/edit") || !strings.Contains(page, prompt+"/delete") {
		t.Fatal("admins should see edit and delete controls")
	}
	if _, page := app.get(admin, "/categories", false); !strings.Contains(page, "New category") {
		t.Fatal("admins should see the new category form")
	}
}

func TestAdminEmailsAreCaseInsensitive(t *testing.T) {
	app := newTestApp(t, func(c *Config) { c.AdminEmails = []string{" Boss@Example.COM "} })
	boss := app.signup("boss@example.com")
	if code, _ := app.get(boss, "/prompts/new", false); code != http.StatusOK {
		t.Fatalf("admin match should ignore case and spaces, got %d", code)
	}
}

func TestCrossOriginProtection(t *testing.T) {
	app := newTestApp(t)
	admin := app.admin()
	form := url.Values{"title": {"x"}, "body": {"y"}}

	code, _, _ := app.postForm(admin, "/prompts", form, map[string]string{"Sec-Fetch-Site": "cross-site"})
	if code != http.StatusForbidden {
		t.Fatalf("cross-site POST: code=%d, want 403", code)
	}
	code, _, _ = app.postForm(admin, "/logout", nil, map[string]string{"Origin": "https://evil.example"})
	if code != http.StatusForbidden {
		t.Fatalf("cross-origin logout: code=%d, want 403", code)
	}
	code, _, _ = app.postForm(admin, "/prompts", form, map[string]string{"Sec-Fetch-Site": "same-origin"})
	if code != http.StatusSeeOther {
		t.Fatalf("same-origin POST: code=%d, want 303", code)
	}

	n := midtrans.Notification{OrderID: "unknown", StatusCode: "200", GrossAmount: "49000.00", TransactionStatus: "settlement"}
	if code := app.notify(n, map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://api.midtrans.com"}); code != http.StatusOK {
		t.Fatalf("the Midtrans webhook must bypass cross-origin protection, got %d", code)
	}
}

// fakeGoogle implements Google's token and userinfo endpoints.
type fakeGoogle struct {
	srv *httptest.Server

	mu        sync.Mutex
	challenge string
	user      map[string]any
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	f := &fakeGoogle{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f.mu.Lock()
		challenge := f.challenge
		f.mu.Unlock()
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		switch {
		case r.PostForm.Get("code") != "good-code",
			r.PostForm.Get("grant_type") != "authorization_code",
			r.PostForm.Get("client_id") != "client-id",
			r.PostForm.Get("client_secret") != "client-secret",
			r.PostForm.Get("redirect_uri") != "http://promptify.test/auth/google/callback",
			base64.RawURLEncoding.EncodeToString(sum[:]) != challenge:
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{"access_token": "access-123", "token_type": "Bearer"})
	})
	mux.HandleFunc("GET /userinfo", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access-123" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		json.NewEncoder(w).Encode(f.user)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGoogle) setUser(sub, email string, verified bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.user = map[string]any{"sub": sub, "email": email, "email_verified": verified}
}

func newGoogleApp(t *testing.T) (*testApp, *fakeGoogle) {
	g := newFakeGoogle(t)
	app := newTestApp(t, func(c *Config) {
		c.Google = GoogleConfig{
			ClientID:     "client-id",
			ClientSecret: "client-secret",
			AuthURL:      g.srv.URL + "/auth",
			TokenURL:     g.srv.URL + "/token",
			UserInfoURL:  g.srv.URL + "/userinfo",
		}
	})
	return app, g
}

// startGoogle follows /auth/google and returns the state Google would echo.
func (a *testApp) startGoogle(c *http.Client, g *fakeGoogle, next string) string {
	a.t.Helper()
	code, h, _ := a.do(c, http.MethodGet, "/auth/google?next="+url.QueryEscape(next), nil, nil)
	if code != http.StatusSeeOther {
		a.t.Fatalf("/auth/google: code=%d", code)
	}
	loc, _ := url.Parse(h.Get("Location"))
	q := loc.Query()
	if loc.Path != "/auth" || q.Get("client_id") != "client-id" || q.Get("response_type") != "code" ||
		q.Get("scope") != "openid email profile" || q.Get("code_challenge_method") != "S256" ||
		q.Get("redirect_uri") != "http://promptify.test/auth/google/callback" || q.Get("state") == "" {
		a.t.Fatalf("bad authorization redirect: %s", loc)
	}
	g.mu.Lock()
	g.challenge = q.Get("code_challenge")
	g.mu.Unlock()
	return q.Get("state")
}

func (a *testApp) googleCallback(c *http.Client, state, code string) (int, string) {
	a.t.Helper()
	status, h, _ := a.do(c, http.MethodGet, "/auth/google/callback?state="+url.QueryEscape(state)+"&code="+code, nil, nil)
	return status, h.Get("Location")
}

func TestGoogleLoginCreatesUser(t *testing.T) {
	app, g := newGoogleApp(t)
	g.setUser("sub-1", "Gina@Gmail.com", true)
	c := app.client()

	if _, page := app.get(c, "/login", false); !strings.Contains(page, "Continue with Google") {
		t.Fatal("login page should offer Google when it is configured")
	}
	state := app.startGoogle(c, g, "/membership")
	if code, loc := app.googleCallback(c, state, "good-code"); code != http.StatusSeeOther || loc != "/membership" {
		t.Fatalf("callback: code=%d location=%q", code, loc)
	}
	if _, page := app.get(c, "/", false); !strings.Contains(page, "gina@gmail.com") {
		t.Fatal("Google login should start a session")
	}
	u, err := app.store.UserByGoogleSub(context.Background(), "sub-1")
	if err != nil || u.Email != "gina@gmail.com" || u.HasPassword() {
		t.Fatalf("google user = %+v, %v", u, err)
	}

	// Logging in again finds the same account, even if the Google email changed.
	g.setUser("sub-1", "gina.new@gmail.com", true)
	c2 := app.client()
	state = app.startGoogle(c2, g, "/")
	app.googleCallback(c2, state, "good-code")
	if _, err := app.store.UserByEmail(context.Background(), "gina.new@gmail.com"); err == nil {
		t.Fatal("returning Google users should be matched by sub, not create a new account")
	}

	code, _, page := app.postForm(app.client(), "/login", url.Values{"email": {"gina@gmail.com"}, "password": {testPassword}}, nil)
	if code != http.StatusUnprocessableEntity || !strings.Contains(page, "uses Google sign-in") {
		t.Fatalf("password login for a Google-only account: code=%d", code)
	}
}

func TestGoogleLoginLinksExistingAccount(t *testing.T) {
	app, g := newGoogleApp(t)
	squatter := app.signup("victim@gmail.com")

	g.setUser("sub-victim", "victim@gmail.com", true)
	owner := app.client()
	state := app.startGoogle(owner, g, "/")
	if code, loc := app.googleCallback(owner, state, "good-code"); code != http.StatusSeeOther || loc != "/" {
		t.Fatalf("callback: code=%d location=%q", code, loc)
	}
	if _, page := app.get(owner, "/", false); !strings.Contains(page, "victim@gmail.com") {
		t.Fatal("the verified Google owner should be logged in")
	}

	if _, page := app.get(squatter, "/", false); strings.Contains(page, "victim@gmail.com") {
		t.Fatal("sessions created before the Google link must be revoked")
	}
	code, _, _ := app.postForm(app.client(), "/login", url.Values{"email": {"victim@gmail.com"}, "password": {testPassword}}, nil)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("the unverified password must stop working after linking, got %d", code)
	}
}

func TestGoogleLoginFailures(t *testing.T) {
	app, g := newGoogleApp(t)
	g.setUser("sub-2", "hal@gmail.com", true)
	const failURL = "/login?error=google"

	c := app.client()
	app.startGoogle(c, g, "/")
	if code, loc := app.googleCallback(c, "wrong-state", "good-code"); code != http.StatusSeeOther || loc != failURL {
		t.Errorf("wrong state: code=%d location=%q", code, loc)
	}

	c = app.client()
	state := app.startGoogle(c, g, "/")
	app.googleCallback(c, state, "good-code")
	if code, loc := app.googleCallback(app.client(), state, "good-code"); loc != failURL {
		t.Errorf("callback without the state cookie: code=%d location=%q", code, loc)
	}

	c = app.client()
	state = app.startGoogle(c, g, "/")
	if _, loc := app.googleCallback(c, state, "bad-code"); loc != failURL {
		t.Errorf("rejected code: location=%q", loc)
	}

	c = app.client()
	state = app.startGoogle(c, g, "/")
	status, h, _ := app.do(c, http.MethodGet, "/auth/google/callback?error=access_denied&state="+state, nil, nil)
	if status != http.StatusSeeOther || h.Get("Location") != failURL {
		t.Errorf("user denied consent: location=%q", h.Get("Location"))
	}

	g.setUser("sub-3", "unverified@gmail.com", false)
	c = app.client()
	state = app.startGoogle(c, g, "/")
	if _, loc := app.googleCallback(c, state, "good-code"); loc != failURL {
		t.Errorf("unverified email: location=%q", loc)
	}
	if _, err := app.store.UserByGoogleSub(context.Background(), "sub-3"); err == nil {
		t.Error("no account should be created for an unverified Google email")
	}

	if _, page := app.get(c, failURL, false); !strings.Contains(page, "Google sign-in didn") {
		t.Error("the login page should explain that Google sign-in failed")
	}
}

func TestGoogleDisabled(t *testing.T) {
	app := newTestApp(t)
	c := app.client()
	for _, p := range []string{"/auth/google", "/auth/google/callback?state=x&code=y"} {
		if code, _ := app.get(c, p, false); code != http.StatusNotFound {
			t.Errorf("GET %s without Google configured: code=%d, want 404", p, code)
		}
	}
	if _, page := app.get(c, "/login", false); strings.Contains(page, "Continue with Google") {
		t.Error("the Google button should be hidden when Google isn't configured")
	}
}
