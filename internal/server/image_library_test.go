package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ahsyarif/promptify/internal/store"
)

func TestImagePromptValidation(t *testing.T) {
	app := newTestApp(t)
	admin := app.admin()
	tests := []struct {
		name, model, ratio, negative string
		image                        bool
		message                      string
	}{
		{name: "missing image", model: "flux", message: "Add an example image."},
		{name: "empty image", model: "flux", image: true, message: "Add an example image."},
		{name: "missing model", image: true, message: "Choose the model this prompt was made for."},
		{name: "unknown model", model: "unknown", image: true, message: "Choose the model this prompt was made for."},
		{name: "unknown ratio", model: "flux", ratio: "12:7", image: true, message: "Choose a valid aspect ratio."},
		{name: "negative too long", model: "flux", negative: strings.Repeat("猫", 5001), image: true, message: "Negative prompt must be 5,000 characters or fewer."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var img []byte
			filename := ""
			if tt.image {
				filename = "image.png"
				if tt.name != "empty image" {
					img = pngBytes(t)
				}
			}
			fields := map[string]string{"title": "Neon portrait", "body": "Paint {{subject}}", "model": tt.model, "negative_prompt": tt.negative, "aspect_ratio": tt.ratio}
			b, ct := multipartBody(t, fields, filename, img)
			code, _, page := app.do(admin, http.MethodPost, "/prompts", b, map[string]string{"Content-Type": ct})
			if code != http.StatusUnprocessableEntity || !strings.Contains(page, tt.message) {
				t.Fatalf("code=%d missing %q: %s", code, tt.message, page)
			}
			if !strings.Contains(page, `value="Neon portrait"`) || !strings.Contains(page, "Paint {{subject}}") {
				t.Fatal("validation must keep field values")
			}
			if len(uploadedFiles(t, app.uploadDir)) != 0 {
				t.Fatal("invalid form must not leave image files")
			}
		})
	}
	code, _, page := app.postForm(admin, "/prompts", url.Values{"model": {"flux"}}, nil)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("urlencoded missing image: %d", code)
	}
	for _, msg := range []string{"Add an example image.", "Title is required.", "Prompt text is required."} {
		if !strings.Contains(page, msg) {
			t.Errorf("missing combined validation: %s", msg)
		}
	}
}

func TestImagePromptFormRoundTrip(t *testing.T) {
	app := newTestApp(t)
	admin := app.admin()
	fields := map[string]string{"title": "Neon portrait", "body": "Paint {{subject}}", "model": "stable-diffusion", "negative_prompt": "no text <watermarks>", "aspect_ratio": "4:5"}
	b, ct := multipartBody(t, fields, "portrait.png", pngBytes(t))
	code, h, page := app.do(admin, http.MethodPost, "/prompts", b, map[string]string{"Content-Type": ct})
	if code != http.StatusSeeOther {
		t.Fatalf("create: %d %s", code, page)
	}
	path := h.Get("Location")
	files := uploadedFiles(t, app.uploadDir)
	if len(files) != 1 {
		t.Fatalf("want one upload, got %v", files)
	}
	_, page = app.get(admin, path+"/edit", false)
	for _, want := range []string{`value="stable-diffusion" selected`, `value="4:5" selected`, "no text &lt;watermarks&gt;", "Upload a new file to replace it"} {
		if !strings.Contains(page, want) {
			t.Errorf("edit form missing %s", want)
		}
	}
	if strings.Contains(page, "remove_image") || strings.Contains(page, `data-preview="#image-preview" required`) {
		t.Fatal("edit form cannot remove or require replacing the image")
	}
	fields["model"], fields["aspect_ratio"], fields["negative_prompt"] = "dall-e", "16:9", "avoid blur"
	b, ct = multipartBody(t, fields, "", nil)
	code, _, _ = app.do(admin, http.MethodPost, path, b, map[string]string{"Content-Type": ct})
	if code != http.StatusSeeOther {
		t.Fatalf("update: %d", code)
	}
	if got := uploadedFiles(t, app.uploadDir); len(got) != 1 || got[0] != files[0] {
		t.Fatalf("update should keep image: %v", got)
	}
	_, page = app.get(admin, path, false)
	for _, want := range []string{"DALL·E", "16:9", "avoid blur", `data-copy="#prompt-body"`, `data-copy="#negative-body"`} {
		if !strings.Contains(page, want) {
			t.Errorf("detail missing %s", want)
		}
	}
}

func TestGalleryCards(t *testing.T) {
	app := newTestApp(t)
	in := store.PromptInput{Title: "Gallery portrait", Body: "BODY_NOT_ON_CARD", Description: "DESCRIPTION_NOT_ON_CARD", ImagePath: "portrait.png", Model: "stable-diffusion", AspectRatio: "4:5", NegativePrompt: "NEGATIVE_NOT_ON_CARD", IsPremium: true}
	id, err := app.store.CreatePrompt(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	anon := app.client()
	app.do(anon, http.MethodPost, fmt.Sprintf("/prompts/%d/bookmark", id), nil, nil)
	for _, path := range []string{"/", "/bookmarks"} {
		for _, partial := range []bool{false, true} {
			_, page := app.get(anon, path, partial)
			for _, want := range []string{`<img src="/uploads/portrait.png"`, "Stable Diffusion", "4:5", `class="card-lock"`} {
				if !strings.Contains(page, want) {
					t.Errorf("%s partial=%v missing %s", path, partial, want)
				}
			}
			for _, hidden := range []string{in.Body, in.Description, in.NegativePrompt, "card-snippet", "card-locked"} {
				if strings.Contains(page, hidden) {
					t.Errorf("card contains %q", hidden)
				}
			}
		}
	}
	_, detail := app.get(anon, fmt.Sprintf("/prompts/%d", id), false)
	for _, want := range []string{"/uploads/portrait.png", "Stable Diffusion", "4:5", "unlock-panel"} {
		if !strings.Contains(detail, want) {
			t.Errorf("locked detail missing %s", want)
		}
	}
	for _, forbidden := range []string{in.Body, in.NegativePrompt, "data-copy="} {
		if strings.Contains(detail, forbidden) {
			t.Errorf("locked detail leaks %s", forbidden)
		}
	}
}

func TestImageMetadataOptions(t *testing.T) {
	for _, o := range imageModels {
		if !validModel(o.Value) || modelLabel(o.Value) != o.Label {
			t.Errorf("model option: %+v", o)
		}
	}
	for _, v := range []string{"", "unknown", "Midjourney"} {
		if validModel(v) {
			t.Errorf("accepted invalid model %q", v)
		}
	}
	if modelLabel("future-model") != "future-model" {
		t.Fatal("unknown model labels should retain their value")
	}
	for _, v := range append([]string{""}, aspectRatios...) {
		if !validAspectRatio(v) {
			t.Errorf("rejected aspect ratio %q", v)
		}
	}
	for _, v := range []string{"16/9", "2:2", "1:0"} {
		if validAspectRatio(v) {
			t.Errorf("accepted invalid aspect ratio %q", v)
		}
	}
}

func TestRedactBothPromptTexts(t *testing.T) {
	s := &Server{admins: map[string]bool{adminEmail: true}}
	now := time.Now()
	viewers := []struct {
		name   string
		user   *store.User
		locked bool
	}{
		{"anonymous", nil, true},
		{"non-member", &store.User{Email: "reader@example.test"}, true},
		{"member", &store.User{MemberSince: &now}, false},
		{"admin", &store.User{Email: adminEmail}, false},
	}
	for _, v := range viewers {
		t.Run(v.name, func(t *testing.T) {
			premium := store.Prompt{Body: "premium body", NegativePrompt: "premium negative", ImagePath: "teaser.png", Model: "flux", AspectRatio: "16:9", IsPremium: true}
			free := store.Prompt{Body: "free body", NegativePrompt: "free negative"}
			s.redact(v.user, &premium, &free)
			if premium.Locked != v.locked {
				t.Fatalf("locked = %v, want %v", premium.Locked, v.locked)
			}
			if v.locked && (premium.Body != "" || premium.NegativePrompt != "") {
				t.Fatal("redaction must clear both texts")
			}
			if !v.locked && (premium.Body != "premium body" || premium.NegativePrompt != "premium negative") {
				t.Fatal("authorized viewers should retain both texts")
			}
			if premium.ImagePath != "teaser.png" || premium.Model != "flux" || premium.AspectRatio != "16:9" {
				t.Fatal("redaction should retain teaser metadata")
			}
			if free.Body != "free body" || free.NegativePrompt != "free negative" || free.Locked {
				t.Fatal("free prompts should not be redacted")
			}
		})
	}
}

func TestNegativePromptCharacterLimitAndTrimming(t *testing.T) {
	app := newTestApp(t)
	admin := app.admin()
	negative := strings.Repeat("猫", maxNegativeLen)
	b, ct := multipartBody(t, map[string]string{"title": "Portrait", "body": "body", "model": " flux ", "negative_prompt": " " + negative + " ", "aspect_ratio": " 4:5 "}, "image.png", pngBytes(t))
	code, h, page := app.do(admin, http.MethodPost, "/prompts", b, map[string]string{"Content-Type": ct})
	if code != http.StatusSeeOther {
		t.Fatalf("5000 Unicode characters should be accepted: %d %s", code, page)
	}
	_, page = app.get(admin, h.Get("Location")+"/edit", false)
	for _, want := range []string{negative, `value="flux" selected`, `value="4:5" selected`} {
		if !strings.Contains(page, want) {
			t.Fatal("trimmed metadata was not persisted")
		}
	}
}
