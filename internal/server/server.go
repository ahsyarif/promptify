package server

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ahsyarif/promptify/internal/payment/midtrans"
	"github.com/ahsyarif/promptify/internal/store"
	"github.com/ahsyarif/promptify/web"
)

const pageSize = 12

const webhookPattern = "POST /payments/midtrans/notification"

type Config struct {
	UploadDir       string
	AdminEmails     []string
	MembershipPrice int64
	// PublicURL is the externally visible origin, e.g. https://promptify.example.com.
	PublicURL string
	// Midtrans is nil when payments are disabled.
	Midtrans *midtrans.Client
	Google   GoogleConfig
}

type GoogleConfig struct {
	ClientID     string
	ClientSecret string
	// Endpoints default to Google's; tests point them at a fake server.
	AuthURL     string
	TokenURL    string
	UserInfoURL string
}

func (g GoogleConfig) Enabled() bool { return g.ClientID != "" && g.ClientSecret != "" }

type Server struct {
	store      *store.Store
	cfg        Config
	uploadDir  string
	admins     map[string]bool
	httpClient *http.Client
	logger     *slog.Logger
	pages      map[string]*template.Template
	partials   *template.Template
	handler    http.Handler
}

func New(st *store.Store, cfg Config, logger *slog.Logger) (*Server, error) {
	if err := os.MkdirAll(cfg.UploadDir, 0o755); err != nil {
		return nil, fmt.Errorf("create upload dir: %w", err)
	}
	if cfg.MembershipPrice <= 0 {
		return nil, fmt.Errorf("membership price must be positive, got %d", cfg.MembershipPrice)
	}
	if cfg.Google.Enabled() && cfg.PublicURL == "" {
		return nil, fmt.Errorf("PublicURL is required for Google login")
	}
	cfg.PublicURL = strings.TrimSuffix(cfg.PublicURL, "/")
	if cfg.Google.AuthURL == "" {
		cfg.Google.AuthURL = "https://accounts.google.com/o/oauth2/v2/auth"
	}
	if cfg.Google.TokenURL == "" {
		cfg.Google.TokenURL = "https://oauth2.googleapis.com/token"
	}
	if cfg.Google.UserInfoURL == "" {
		cfg.Google.UserInfoURL = "https://openidconnect.googleapis.com/v1/userinfo"
	}

	admins := make(map[string]bool, len(cfg.AdminEmails))
	for _, e := range cfg.AdminEmails {
		if e = normalizeEmail(e); e != "" {
			admins[e] = true
		}
	}
	s := &Server{
		store:      st,
		cfg:        cfg,
		uploadDir:  cfg.UploadDir,
		admins:     admins,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		logger:     logger,
	}
	if err := s.parseTemplates(); err != nil {
		return nil, err
	}
	s.handler = s.routes()
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	static, err := fs.Sub(web.FS, "static")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.Handle("GET /uploads/", http.StripPrefix("/uploads/", noDirListing(http.FileServer(http.Dir(s.uploadDir)))))

	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /bookmarks", s.handleBookmarks)
	mux.HandleFunc("GET /categories", s.handleCategories)
	mux.HandleFunc("POST /categories", s.handleCreateCategory)
	mux.HandleFunc("GET /prompts/new", s.handleNewPrompt)
	mux.HandleFunc("POST /prompts", s.handleCreatePrompt)
	mux.HandleFunc("GET /prompts/{id}", s.handleShowPrompt)
	mux.HandleFunc("GET /prompts/{id}/edit", s.handleEditPrompt)
	mux.HandleFunc("POST /prompts/{id}", s.handleUpdatePrompt)
	mux.HandleFunc("POST /prompts/{id}/delete", s.handleDeletePrompt)
	mux.HandleFunc("POST /prompts/{id}/bookmark", s.handleToggleBookmark)

	mux.HandleFunc("GET /signup", s.handleSignupPage)
	mux.HandleFunc("POST /signup", s.handleSignup)
	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("POST /logout", s.handleLogout)
	mux.HandleFunc("GET /auth/google", s.handleGoogleStart)
	mux.HandleFunc("GET /auth/google/callback", s.handleGoogleCallback)

	mux.HandleFunc("GET /membership", s.handleMembership)
	mux.HandleFunc("POST /membership/checkout", s.handleCheckout)
	mux.HandleFunc("GET /membership/orders/{orderID}", s.handleOrder)
	mux.HandleFunc("GET /membership/orders/{orderID}/status", s.handleOrderStatus)
	mux.HandleFunc("GET /membership/orders/{orderID}/qr.png", s.handleOrderQR)
	mux.HandleFunc(webhookPattern, s.handleMidtransNotification)

	mux.HandleFunc("/", s.handleNotFound)

	// Midtrans posts notifications server-to-server; they are authenticated
	// by signature instead, so they are the only cross-origin exception.
	csrf := http.NewCrossOriginProtection()
	csrf.AddInsecureBypassPattern(webhookPattern)

	return s.recoverer(s.logRequests(securityHeaders(csrf.Handler(s.withVisitor(s.withUser(mux))))))
}

func noDirListing(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "" || strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) parseTemplates() error {
	funcs := template.FuncMap{
		"excerpt":       excerpt,
		"highlightVars": highlightVars,
		"imageURL":      func(p string) string { return "/uploads/" + p },
		"idr":           formatIDR,
		"wib":           formatWIB,
	}
	pageFiles, err := fs.Glob(web.FS, "templates/pages/*.html")
	if err != nil {
		return err
	}
	s.pages = make(map[string]*template.Template, len(pageFiles))
	for _, file := range pageFiles {
		t, err := template.New("").Funcs(funcs).ParseFS(web.FS, "templates/layout.html", "templates/partials/*.html", file)
		if err != nil {
			return fmt.Errorf("parse %s: %w", file, err)
		}
		name := strings.TrimSuffix(file[strings.LastIndex(file, "/")+1:], ".html")
		s.pages[name] = t
	}
	s.partials, err = template.New("").Funcs(funcs).ParseFS(web.FS, "templates/partials/*.html")
	return err
}

func (s *Server) render(w http.ResponseWriter, status int, page string, data any) {
	t, ok := s.pages[page]
	if !ok {
		s.logger.Error("unknown page template", "page", page)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	s.execute(w, status, t, "layout", data)
}

func (s *Server) renderPartial(w http.ResponseWriter, status int, name string, data any) {
	s.execute(w, status, s.partials, name, data)
}

func (s *Server) execute(w http.ResponseWriter, status int, t *template.Template, name string, data any) {
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, name, data); err != nil {
		s.logger.Error("render template", "template", name, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	buf.WriteTo(w)
}
