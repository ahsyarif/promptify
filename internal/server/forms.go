package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ahsyarif/promptify/internal/store"
)

const (
	maxTitleLen       = 200
	maxDescriptionLen = 500
	maxBodyLen        = 20000
	maxNegativeLen    = 5000
	maxRequestBytes   = maxImageBytes + 1<<20
)

// parsePromptForm validates the text fields of the prompt form. Validation
// problems are returned in errs; err is reserved for unexpected failures.
func (s *Server) parsePromptForm(w http.ResponseWriter, r *http.Request, requireImage bool) (promptForm, store.PromptInput, map[string]string, error) {
	errs := map[string]string{}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	if err := r.ParseMultipartForm(1 << 20); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			errs["image"] = msgImageTooLarge
			return promptForm{}, store.PromptInput{}, errs, nil
		}
		return promptForm{}, store.PromptInput{}, nil, err
	}

	if requireImage && (r.MultipartForm == nil || len(r.MultipartForm.File["image"]) == 0 || r.MultipartForm.File["image"][0].Size == 0) {
		errs["image"] = "Add an example image."
	}
	form := promptForm{
		Model:          strings.TrimSpace(r.PostFormValue("model")),
		NegativePrompt: strings.TrimSpace(r.PostFormValue("negative_prompt")),
		AspectRatio:    strings.TrimSpace(r.PostFormValue("aspect_ratio")),
		Title:          strings.TrimSpace(r.PostFormValue("title")),
		Description:    strings.TrimSpace(r.PostFormValue("description")),
		Body:           strings.TrimSpace(r.PostFormValue("body")),
		CategoryID:     r.PostFormValue("category_id"),
		IsPremium:      r.PostFormValue("is_premium") == "1",
	}
	input := store.PromptInput{
		Title: form.Title, Description: form.Description, Body: form.Body, IsPremium: form.IsPremium,
		Model: form.Model, NegativePrompt: form.NegativePrompt, AspectRatio: form.AspectRatio,
	}

	if !validModel(form.Model) {
		errs["model"] = "Choose the model this prompt was made for."
	}
	if !validAspectRatio(form.AspectRatio) {
		errs["aspect_ratio"] = "Choose a valid aspect ratio."
	}
	if utf8.RuneCountInString(form.NegativePrompt) > maxNegativeLen {
		errs["negative_prompt"] = "Negative prompt must be 5,000 characters or fewer."
	}
	switch {
	case form.Title == "":
		errs["title"] = "Title is required."
	case utf8.RuneCountInString(form.Title) > maxTitleLen:
		errs["title"] = "Title must be 200 characters or fewer."
	}
	if utf8.RuneCountInString(form.Description) > maxDescriptionLen {
		errs["description"] = "Description must be 500 characters or fewer."
	}
	switch {
	case form.Body == "":
		errs["body"] = "Prompt text is required."
	case utf8.RuneCountInString(form.Body) > maxBodyLen:
		errs["body"] = "Prompt text must be 20,000 characters or fewer."
	}
	if form.CategoryID != "" {
		id, err := strconv.ParseInt(form.CategoryID, 10, 64)
		if err != nil {
			errs["category_id"] = "Choose a valid category."
		} else {
			exists, err := s.store.CategoryExists(r.Context(), id)
			if err != nil {
				return form, input, nil, err
			}
			if !exists {
				errs["category_id"] = "Choose a valid category."
			}
			input.CategoryID = &id
		}
	}
	return form, input, errs, nil
}

// imageFromRequest resolves the image the prompt should have after the form is
// submitted: a newly uploaded file or the current image unchanged.
func (s *Server) imageFromRequest(r *http.Request, current string, errs map[string]string) (string, error) {
	file, header, err := r.FormFile("image")
	if errors.Is(err, http.ErrMissingFile) || errors.Is(err, http.ErrNotMultipart) {
		return current, nil
	}
	if err != nil {
		return "", err
	}
	defer file.Close()
	if header.Size == 0 {
		return current, nil
	}

	name, err := s.saveImage(file, header)
	switch {
	case errors.Is(err, errInvalidImage):
		errs["image"] = msgInvalidImage
		return current, nil
	case errors.Is(err, errImageTooLarge):
		errs["image"] = msgImageTooLarge
		return current, nil
	}
	return name, err
}
