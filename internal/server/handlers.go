package server

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ahsyarif/promptify/internal/store"
)

type viewData struct {
	Title         string
	Nav           string
	BookmarkCount int
	Categories    []store.Category

	// Listing pages.
	BasePath   string
	Query      string
	Category   string
	Prompts    []store.Prompt
	HasMore    bool
	NextURL    string
	EmptyTitle string
	EmptyText  string

	// Detail and form pages.
	Prompt store.Prompt
	Form   promptForm
	Errors map[string]string

	// Categories page.
	CategoryName        string
	CategoryDescription string
}

type promptForm struct {
	Title       string
	Description string
	Body        string
	CategoryID  string
	ImagePath   string
}

// isPartialRequest reports whether htmx asked for a fragment. History-restore
// requests need the full page because htmx swaps the whole body.
func isPartialRequest(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-History-Restore-Request") != "true"
}

func (s *Server) baseData(r *http.Request, title, nav string) (viewData, error) {
	cats, err := s.store.ListCategories(r.Context())
	if err != nil {
		return viewData{}, err
	}
	count, err := s.store.CountBookmarks(r.Context(), visitorID(r))
	if err != nil {
		return viewData{}, err
	}
	return viewData{Title: title, Nav: nav, Categories: cats, BookmarkCount: count}, nil
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	s.listPrompts(w, r, "/", false)
}

func (s *Server) handleBookmarks(w http.ResponseWriter, r *http.Request) {
	s.listPrompts(w, r, "/bookmarks", true)
}

func (s *Server) listPrompts(w http.ResponseWriter, r *http.Request, basePath string, bookmarkedOnly bool) {
	q := r.URL.Query()
	query := strings.TrimSpace(q.Get("q"))
	category := q.Get("category")
	offset, _ := strconv.Atoi(q.Get("offset"))
	offset = max(offset, 0)

	prompts, hasMore, err := s.store.ListPrompts(r.Context(), store.ListParams{
		Query:          query,
		CategorySlug:   category,
		VisitorID:      visitorID(r),
		BookmarkedOnly: bookmarkedOnly,
		Limit:          pageSize,
		Offset:         offset,
	})
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	data := viewData{
		BasePath: basePath,
		Query:    query,
		Category: category,
		Prompts:  prompts,
		HasMore:  hasMore,
	}
	if hasMore {
		next := url.Values{}
		if query != "" {
			next.Set("q", query)
		}
		if category != "" {
			next.Set("category", category)
		}
		next.Set("offset", strconv.Itoa(offset+pageSize))
		data.NextURL = basePath + "?" + next.Encode()
	}
	switch {
	case bookmarkedOnly && query == "" && category == "":
		data.EmptyTitle, data.EmptyText = "No saved prompts yet", "Tap the bookmark icon on any prompt to keep it here."
	case query != "" || category != "":
		data.EmptyTitle, data.EmptyText = "No matches", "Try a different search term or category."
	default:
		data.EmptyTitle, data.EmptyText = "The library is empty", "Add the first prompt to get started."
	}

	w.Header().Add("Vary", "HX-Request")
	if isPartialRequest(r) {
		if offset > 0 {
			s.renderPartial(w, http.StatusOK, "cards", data)
		} else {
			s.renderPartial(w, http.StatusOK, "results", data)
		}
		return
	}

	title, nav := "Prompt library", "library"
	if bookmarkedOnly {
		title, nav = "Saved prompts", "bookmarks"
	}
	base, err := s.baseData(r, title, nav)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data.Title, data.Nav, data.Categories, data.BookmarkCount = base.Title, base.Nav, base.Categories, base.BookmarkCount
	s.render(w, http.StatusOK, "list", data)
}

func (s *Server) handleShowPrompt(w http.ResponseWriter, r *http.Request) {
	id, ok := s.promptID(w, r)
	if !ok {
		return
	}
	p, err := s.store.GetPrompt(r.Context(), id, visitorID(r))
	if errors.Is(err, store.ErrNotFound) {
		s.handleNotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data, err := s.baseData(r, p.Title, "")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data.Prompt = p
	s.render(w, http.StatusOK, "prompt", data)
}

func (s *Server) handleNewPrompt(w http.ResponseWriter, r *http.Request) {
	data, err := s.baseData(r, "New prompt", "new")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data.Form.CategoryID = r.URL.Query().Get("category_id")
	s.render(w, http.StatusOK, "form", data)
}

func (s *Server) handleEditPrompt(w http.ResponseWriter, r *http.Request) {
	id, ok := s.promptID(w, r)
	if !ok {
		return
	}
	p, err := s.store.GetPrompt(r.Context(), id, visitorID(r))
	if errors.Is(err, store.ErrNotFound) {
		s.handleNotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data, err := s.baseData(r, "Edit prompt", "")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data.Prompt = p
	data.Form = promptForm{Title: p.Title, Description: p.Description, Body: p.Body, ImagePath: p.ImagePath}
	if p.CategoryID != nil {
		data.Form.CategoryID = strconv.FormatInt(*p.CategoryID, 10)
	}
	s.render(w, http.StatusOK, "form", data)
}

func (s *Server) handleCreatePrompt(w http.ResponseWriter, r *http.Request) {
	form, input, errs, err := s.parsePromptForm(w, r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if len(errs) == 0 {
		input.ImagePath, err = s.imageFromRequest(r, "", errs)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	if len(errs) > 0 {
		s.renderFormErrors(w, r, "New prompt", "new", store.Prompt{}, form, errs)
		return
	}
	id, err := s.store.CreatePrompt(r.Context(), input)
	if err != nil {
		s.removeImage(input.ImagePath)
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/prompts/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (s *Server) handleUpdatePrompt(w http.ResponseWriter, r *http.Request) {
	id, ok := s.promptID(w, r)
	if !ok {
		return
	}
	existing, err := s.store.GetPrompt(r.Context(), id, visitorID(r))
	if errors.Is(err, store.ErrNotFound) {
		s.handleNotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	form, input, errs, err := s.parsePromptForm(w, r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	form.ImagePath = existing.ImagePath
	if len(errs) == 0 {
		input.ImagePath, err = s.imageFromRequest(r, existing.ImagePath, errs)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	if len(errs) > 0 {
		s.renderFormErrors(w, r, "Edit prompt", "", existing, form, errs)
		return
	}
	if err := s.store.UpdatePrompt(r.Context(), id, input); err != nil {
		if input.ImagePath != existing.ImagePath {
			s.removeImage(input.ImagePath)
		}
		s.serverError(w, r, err)
		return
	}
	if input.ImagePath != existing.ImagePath {
		s.removeImage(existing.ImagePath)
	}
	http.Redirect(w, r, "/prompts/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (s *Server) handleDeletePrompt(w http.ResponseWriter, r *http.Request) {
	id, ok := s.promptID(w, r)
	if !ok {
		return
	}
	imagePath, err := s.store.DeletePrompt(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.handleNotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.removeImage(imagePath)
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleToggleBookmark(w http.ResponseWriter, r *http.Request) {
	id, ok := s.promptID(w, r)
	if !ok {
		return
	}
	bookmarked, err := s.store.ToggleBookmark(r.Context(), visitorID(r), id)
	if errors.Is(err, store.ErrNotFound) {
		s.handleNotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if r.Header.Get("HX-Request") != "true" {
		http.Redirect(w, r, "/prompts/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
		return
	}
	count, err := s.store.CountBookmarks(r.Context(), visitorID(r))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderPartial(w, http.StatusOK, "bookmark-response", map[string]any{
		"Prompt":        store.Prompt{ID: id, Bookmarked: bookmarked},
		"BookmarkCount": count,
	})
}

func (s *Server) handleCategories(w http.ResponseWriter, r *http.Request) {
	data, err := s.baseData(r, "Categories", "categories")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, http.StatusOK, "categories", data)
}

func (s *Server) handleCreateCategory(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	description := strings.TrimSpace(r.FormValue("description"))
	errs := map[string]string{}
	switch {
	case name == "" || store.Slugify(name) == "":
		errs["name"] = "Name must contain at least one letter or number."
	case utf8.RuneCountInString(name) > 50:
		errs["name"] = "Name must be 50 characters or fewer."
	}
	if utf8.RuneCountInString(description) > 200 {
		errs["description"] = "Description must be 200 characters or fewer."
	}
	if len(errs) == 0 {
		_, err := s.store.CreateCategory(r.Context(), name, description)
		if errors.Is(err, store.ErrConflict) {
			errs["name"] = "A category with this name already exists."
		} else if err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	if len(errs) == 0 {
		http.Redirect(w, r, "/categories", http.StatusSeeOther)
		return
	}
	data, err := s.baseData(r, "Categories", "categories")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data.Errors, data.CategoryName, data.CategoryDescription = errs, name, description
	s.render(w, http.StatusUnprocessableEntity, "categories", data)
}

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	data, err := s.baseData(r, "Not found", "")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, http.StatusNotFound, "notfound", data)
}

func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	s.logger.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

func (s *Server) promptID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.handleNotFound(w, r)
		return 0, false
	}
	return id, true
}

func (s *Server) renderFormErrors(w http.ResponseWriter, r *http.Request, title, nav string, p store.Prompt, form promptForm, errs map[string]string) {
	data, err := s.baseData(r, title, nav)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data.Prompt, data.Form, data.Errors = p, form, errs
	s.render(w, http.StatusUnprocessableEntity, "form", data)
}
