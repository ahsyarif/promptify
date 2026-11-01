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

	"github.com/ahsyarif/promptify/internal/store"
	"github.com/ahsyarif/promptify/web"
)

const pageSize = 12

type Server struct {
	store     *store.Store
	uploadDir string
	logger    *slog.Logger
	pages     map[string]*template.Template
	partials  *template.Template
	handler   http.Handler
}

func New(st *store.Store, uploadDir string, logger *slog.Logger) (*Server, error) {
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		return nil, fmt.Errorf("create upload dir: %w", err)
	}
	s := &Server{store: st, uploadDir: uploadDir, logger: logger}
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
	mux.HandleFunc("/", s.handleNotFound)

	return s.recoverer(s.logRequests(securityHeaders(s.withVisitor(mux))))
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
