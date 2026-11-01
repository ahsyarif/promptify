package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ahsyarif/promptify/internal/store"
)

const (
	oauthCookie     = "oauth_state"
	oauthCookiePath = "/auth/google"
	oauthStateTTL   = 10 * time.Minute
)

func randomToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *Server) googleRedirectURI() string {
	return s.cfg.PublicURL + "/auth/google/callback"
}

// handleGoogleStart begins the OAuth authorization-code flow with PKCE. The
// state, PKCE verifier and post-login path live in a short-lived cookie.
func (s *Server) handleGoogleStart(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Google.Enabled() {
		s.handleNotFound(w, r)
		return
	}
	state, verifier := randomToken(), randomToken()
	next := safeNext(r.URL.Query().Get("next"))
	http.SetCookie(w, &http.Cookie{
		Name:     oauthCookie,
		Value:    state + "|" + verifier + "|" + base64.RawURLEncoding.EncodeToString([]byte(next)),
		Path:     oauthCookiePath,
		MaxAge:   int(oauthStateTTL.Seconds()),
		HttpOnly: true,
		Secure:   isSecure(r),
		SameSite: http.SameSiteLaxMode,
	})

	challenge := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"client_id":             {s.cfg.Google.ClientID},
		"redirect_uri":          {s.googleRedirectURI()},
		"response_type":         {"code"},
		"scope":                 {"openid email profile"},
		"state":                 {state},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
		"prompt":                {"select_account"},
	}
	http.Redirect(w, r, s.cfg.Google.AuthURL+"?"+q.Encode(), http.StatusSeeOther)
}

func (s *Server) handleGoogleCallback(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Google.Enabled() {
		s.handleNotFound(w, r)
		return
	}
	next, err := s.googleLogin(w, r)
	if err != nil {
		s.logger.Warn("google login failed", "err", err)
		http.Redirect(w, r, "/login?error=google", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// googleLogin completes the flow, logs the user in and returns where to go.
func (s *Server) googleLogin(w http.ResponseWriter, r *http.Request) (string, error) {
	c, err := r.Cookie(oauthCookie)
	clearCookie(w, r, oauthCookie, oauthCookiePath)
	if err != nil {
		return "", errors.New("missing state cookie")
	}
	parts := strings.SplitN(c.Value, "|", 3)
	if len(parts) != 3 {
		return "", errors.New("malformed state cookie")
	}
	state, verifier := parts[0], parts[1]
	nextBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", errors.New("malformed state cookie")
	}

	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		return "", fmt.Errorf("authorization denied: %s", e)
	}
	if state == "" || subtle.ConstantTimeCompare([]byte(state), []byte(q.Get("state"))) != 1 {
		return "", errors.New("state mismatch")
	}
	code := q.Get("code")
	if code == "" {
		return "", errors.New("missing code")
	}

	token, err := s.googleExchange(r.Context(), code, verifier)
	if err != nil {
		return "", err
	}
	info, err := s.googleUserInfo(r.Context(), token)
	if err != nil {
		return "", err
	}
	if info.Sub == "" || info.Email == "" || !info.EmailVerified {
		return "", errors.New("google account has no verified email")
	}

	u, err := s.resolveGoogleUser(r.Context(), info.Sub, normalizeEmail(info.Email))
	if err != nil {
		return "", err
	}
	if err := s.startSession(w, r, u.ID); err != nil {
		return "", err
	}
	return safeNext(string(nextBytes)), nil
}

func (s *Server) resolveGoogleUser(ctx context.Context, sub, email string) (store.User, error) {
	u, err := s.store.UserByGoogleSub(ctx, sub)
	if !errors.Is(err, store.ErrNotFound) {
		return u, err
	}
	u, err = s.store.UserByEmail(ctx, email)
	if err == nil {
		return s.store.LinkGoogle(ctx, u.ID, sub)
	}
	if !errors.Is(err, store.ErrNotFound) {
		return store.User{}, err
	}
	return s.store.CreateGoogleUser(ctx, email, sub)
}

func (s *Server) googleExchange(ctx context.Context, code, verifier string) (string, error) {
	form := url.Values{
		"code":          {code},
		"client_id":     {s.cfg.Google.ClientID},
		"client_secret": {s.cfg.Google.ClientSecret},
		"redirect_uri":  {s.googleRedirectURI()},
		"grant_type":    {"authorization_code"},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.Google.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := s.doJSON(req, &out); err != nil {
		return "", fmt.Errorf("token exchange: %w", err)
	}
	if out.AccessToken == "" {
		return "", errors.New("token exchange: no access token")
	}
	return out.AccessToken, nil
}

type googleUser struct {
	Sub           string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
}

func (s *Server) googleUserInfo(ctx context.Context, accessToken string) (googleUser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cfg.Google.UserInfoURL, nil)
	if err != nil {
		return googleUser{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	var u googleUser
	if err := s.doJSON(req, &u); err != nil {
		return googleUser{}, fmt.Errorf("userinfo: %w", err)
	}
	return u, nil
}

func (s *Server) doJSON(req *http.Request, out any) error {
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body := io.LimitReader(resp.Body, 1<<20)
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(body, 512))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, msg)
	}
	return json.NewDecoder(body).Decode(out)
}
