package server

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ahsyarif/promptify/internal/store"
	"github.com/ahsyarif/promptify/internal/testdb"
)

type testApp struct {
	t         *testing.T
	srv       *httptest.Server
	store     *store.Store
	uploadDir string
}

func newTestApp(t *testing.T) *testApp {
	t.Helper()
	st := store.New(testdb.New(t))
	uploadDir := t.TempDir()
	s, err := New(st, uploadDir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return &testApp{t: t, srv: ts, store: st, uploadDir: uploadDir}
}

// client returns a browser-like client with its own cookie jar (i.e. its own
// visitor identity) that does not follow redirects.
func (a *testApp) client() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (a *testApp) do(c *http.Client, method, path string, body io.Reader, headers map[string]string) (int, http.Header, string) {
	a.t.Helper()
	req, err := http.NewRequest(method, a.srv.URL+path, body)
	if err != nil {
		a.t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(b)
}

func (a *testApp) get(c *http.Client, path string, htmx bool) (int, string) {
	a.t.Helper()
	h := map[string]string{}
	if htmx {
		h["HX-Request"] = "true"
	}
	code, _, body := a.do(c, http.MethodGet, path, nil, h)
	return code, body
}

func multipartBody(t *testing.T, fields map[string]string, fileName string, file []byte) (io.Reader, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		w.WriteField(k, v)
	}
	if fileName != "" {
		fw, err := w.CreateFormFile("image", fileName)
		if err != nil {
			t.Fatal(err)
		}
		fw.Write(file)
	}
	w.Close()
	return &buf, w.FormDataContentType()
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for x := range 8 {
		img.Set(x, x, color.RGBA{200, 100, 50, 255})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func uploadedFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestIndexFullPageAndPartial(t *testing.T) {
	app := newTestApp(t)
	c := app.client()

	code, body := app.get(c, "/", false)
	if code != http.StatusOK || !strings.Contains(body, "<!doctype html>") || !strings.Contains(body, `id="results"`) {
		t.Fatalf("full page: code=%d, missing layout or results container", code)
	}

	code, body = app.get(c, "/?q=sql", true)
	if code != http.StatusOK {
		t.Fatalf("partial: code=%d", code)
	}
	if strings.Contains(body, "<!doctype html>") {
		t.Fatal("htmx request should get a fragment, not the full layout")
	}
	if !strings.Contains(body, "SQL query builder") || strings.Contains(body, "Feynman tutor") {
		t.Fatalf("search results wrong:\n%s", body)
	}

	_, h, body := app.do(c, http.MethodGet, "/?q=sql", nil, map[string]string{"HX-Request": "true", "HX-History-Restore-Request": "true"})
	if !strings.Contains(body, "<!doctype html>") {
		t.Fatal("history restore requests need the full page")
	}
	if !strings.Contains(h.Get("Vary"), "HX-Request") {
		t.Fatal("responses must vary on HX-Request so caches don't mix fragments and pages")
	}

	_, body = app.get(c, "/?q=nothingmatchesthis", true)
	if !strings.Contains(body, "No matches") {
		t.Fatal("expected empty state for no matches")
	}
}

func TestCategoryFilterAndLoadMore(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	cat, err := app.store.CreateCategory(ctx, "Bulk", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := range pageSize + 3 {
		if _, err := app.store.CreatePrompt(ctx, store.PromptInput{Title: fmt.Sprintf("Bulk prompt %02d", i), Body: "b", CategoryID: &cat.ID}); err != nil {
			t.Fatal(err)
		}
	}
	c := app.client()

	_, first := app.get(c, "/?category=bulk", true)
	if n := strings.Count(first, `class="card"`); n != pageSize {
		t.Fatalf("first page has %d cards, want %d", n, pageSize)
	}
	if strings.Contains(first, "Feynman tutor") {
		t.Fatal("category filter leaked prompts from other categories")
	}
	next := fmt.Sprintf(`hx-get="/?category=bulk&amp;offset=%d"`, pageSize)
	if !strings.Contains(first, next) {
		t.Fatalf("missing load-more link %s", next)
	}

	_, second := app.get(c, fmt.Sprintf("/?category=bulk&offset=%d", pageSize), true)
	if strings.Contains(second, `class="grid"`) {
		t.Fatal("load-more response must not include the grid wrapper")
	}
	if n := strings.Count(second, `class="card"`); n != 3 {
		t.Fatalf("second page has %d cards, want 3", n)
	}
	if strings.Contains(second, "Load more") {
		t.Fatal("last page should not offer load more")
	}
}

func TestCreatePromptWithImage(t *testing.T) {
	app := newTestApp(t)
	c := app.client()

	body, ctype := multipartBody(t, map[string]string{
		"title": "Sunset painting",
		"body":  "Paint a {{style}} sunset over <the> sea",
	}, "sunset.png", pngBytes(t))
	code, h, _ := app.do(c, http.MethodPost, "/prompts", body, map[string]string{"Content-Type": ctype})
	if code != http.StatusSeeOther || !strings.HasPrefix(h.Get("Location"), "/prompts/") {
		t.Fatalf("create: code=%d location=%q", code, h.Get("Location"))
	}

	files := uploadedFiles(t, app.uploadDir)
	if len(files) != 1 || filepath.Ext(files[0]) != ".png" {
		t.Fatalf("uploaded files = %v, want one .png", files)
	}

	code, page := app.get(c, h.Get("Location"), false)
	if code != http.StatusOK {
		t.Fatalf("detail: code=%d", code)
	}
	if !strings.Contains(page, "/uploads/"+files[0]) {
		t.Fatal("detail page should show the uploaded image")
	}
	if !strings.Contains(page, "<mark>{{style}}</mark>") || !strings.Contains(page, "&lt;the&gt;") {
		t.Fatal("prompt body should be escaped with placeholders highlighted")
	}

	code, img := app.get(c, "/uploads/"+files[0], false)
	if code != http.StatusOK || !bytes.Equal([]byte(img), pngBytes(t)) {
		t.Fatalf("serving upload: code=%d", code)
	}
	if code, _ := app.get(c, "/uploads/", false); code != http.StatusNotFound {
		t.Fatalf("upload directory listing should be disabled, got %d", code)
	}
}

func TestCreatePromptValidation(t *testing.T) {
	app := newTestApp(t)
	c := app.client()

	cases := []struct {
		name     string
		fields   map[string]string
		file     []byte
		wantText string
	}{
		{"missing title", map[string]string{"body": "x"}, nil, "Title is required."},
		{"missing body", map[string]string{"title": "x"}, nil, "Prompt text is required."},
		{"unknown category", map[string]string{"title": "x", "body": "y", "category_id": "424242"}, nil, "Choose a valid category."},
		{"non-numeric category", map[string]string{"title": "x", "body": "y", "category_id": "abc"}, nil, "Choose a valid category."},
		{"title too long", map[string]string{"title": strings.Repeat("a", 201), "body": "y"}, nil, "200 characters"},
		{"fake image", map[string]string{"title": "x", "body": "y"}, []byte("<html>not an image</html>"), msgInvalidImage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fileName := ""
			if tc.file != nil {
				fileName = "evil.png"
			}
			body, ctype := multipartBody(t, tc.fields, fileName, tc.file)
			code, _, page := app.do(c, http.MethodPost, "/prompts", body, map[string]string{"Content-Type": ctype})
			if code != http.StatusUnprocessableEntity {
				t.Fatalf("code=%d, want 422", code)
			}
			if !strings.Contains(page, tc.wantText) {
				t.Fatalf("response missing %q", tc.wantText)
			}
		})
	}
	if files := uploadedFiles(t, app.uploadDir); len(files) != 0 {
		t.Fatalf("no files should be stored for invalid submissions, found %v", files)
	}
}

func TestOversizedUpload(t *testing.T) {
	app := newTestApp(t)
	c := app.client()
	big := append(pngBytes(t), make([]byte, maxRequestBytes)...)
	body, ctype := multipartBody(t, map[string]string{"title": "x", "body": "y"}, "big.png", big)
	code, _, page := app.do(c, http.MethodPost, "/prompts", body, map[string]string{"Content-Type": ctype})
	if code != http.StatusUnprocessableEntity || !strings.Contains(page, msgImageTooLarge) {
		t.Fatalf("code=%d, want 422 with size error", code)
	}
}

func TestUpdateAndDeleteManageImages(t *testing.T) {
	app := newTestApp(t)
	c := app.client()

	body, ctype := multipartBody(t, map[string]string{"title": "Original", "body": "b"}, "a.png", pngBytes(t))
	_, h, _ := app.do(c, http.MethodPost, "/prompts", body, map[string]string{"Content-Type": ctype})
	path := h.Get("Location")
	original := uploadedFiles(t, app.uploadDir)

	body, ctype = multipartBody(t, map[string]string{"title": "Renamed", "body": "b"}, "", nil)
	if code, _, _ := app.do(c, http.MethodPost, path, body, map[string]string{"Content-Type": ctype}); code != http.StatusSeeOther {
		t.Fatalf("update without image: code=%d", code)
	}
	if files := uploadedFiles(t, app.uploadDir); len(files) != 1 || files[0] != original[0] {
		t.Fatalf("image should be kept when no new file is uploaded, files=%v", files)
	}
	if _, page := app.get(c, path, false); !strings.Contains(page, "Renamed") {
		t.Fatal("title was not updated")
	}

	body, ctype = multipartBody(t, map[string]string{"title": "Renamed", "body": "b"}, "b.png", pngBytes(t))
	app.do(c, http.MethodPost, path, body, map[string]string{"Content-Type": ctype})
	replaced := uploadedFiles(t, app.uploadDir)
	if len(replaced) != 1 || replaced[0] == original[0] {
		t.Fatalf("replacing the image should delete the old file, files=%v", replaced)
	}

	body, ctype = multipartBody(t, map[string]string{"title": "Renamed", "body": "b", "remove_image": "1"}, "", nil)
	app.do(c, http.MethodPost, path, body, map[string]string{"Content-Type": ctype})
	if files := uploadedFiles(t, app.uploadDir); len(files) != 0 {
		t.Fatalf("remove_image should delete the file, files=%v", files)
	}

	body, ctype = multipartBody(t, map[string]string{"title": "Renamed", "body": "b"}, "c.png", pngBytes(t))
	app.do(c, http.MethodPost, path, body, map[string]string{"Content-Type": ctype})

	code, h, _ := app.do(c, http.MethodPost, path+"/delete", nil, map[string]string{"HX-Request": "true"})
	if code != http.StatusNoContent || h.Get("HX-Redirect") != "/" {
		t.Fatalf("htmx delete: code=%d HX-Redirect=%q", code, h.Get("HX-Redirect"))
	}
	if files := uploadedFiles(t, app.uploadDir); len(files) != 0 {
		t.Fatalf("deleting a prompt should delete its image, files=%v", files)
	}
	if code, _ := app.get(c, path, false); code != http.StatusNotFound {
		t.Fatalf("deleted prompt: code=%d, want 404", code)
	}
}

func TestBookmarkFlow(t *testing.T) {
	app := newTestApp(t)
	id, err := app.store.CreatePrompt(context.Background(), store.PromptInput{Title: "Bookmark me", Body: "b"})
	if err != nil {
		t.Fatal(err)
	}
	alice, bob := app.client(), app.client()
	app.get(alice, "/", false) // obtain visitor cookie
	path := fmt.Sprintf("/prompts/%d/bookmark", id)

	code, _, body := app.do(alice, http.MethodPost, path, nil, map[string]string{"HX-Request": "true"})
	if code != http.StatusOK || !strings.Contains(body, "is-saved") || !strings.Contains(body, `aria-pressed="true"`) {
		t.Fatalf("toggle on: code=%d body=%s", code, body)
	}
	if !strings.Contains(body, `hx-swap-oob="true">1</span>`) {
		t.Fatalf("response should update the nav counter out-of-band: %s", body)
	}

	if _, page := app.get(alice, "/bookmarks", false); !strings.Contains(page, "Bookmark me") {
		t.Fatal("alice's saved page should list the prompt")
	}
	if _, page := app.get(bob, "/bookmarks", false); strings.Contains(page, "Bookmark me") || !strings.Contains(page, "No saved prompts yet") {
		t.Fatal("bob must not see alice's bookmarks")
	}

	_, _, body = app.do(alice, http.MethodPost, path, nil, map[string]string{"HX-Request": "true"})
	if strings.Contains(body, "is-saved") || !strings.Contains(body, `hx-swap-oob="true"></span>`) {
		t.Fatalf("toggle off: %s", body)
	}

	code, h, _ := app.do(alice, http.MethodPost, path, nil, nil)
	if code != http.StatusSeeOther || h.Get("Location") != fmt.Sprintf("/prompts/%d", id) {
		t.Fatalf("non-htmx bookmark should redirect: code=%d location=%q", code, h.Get("Location"))
	}

	if code, _, _ := app.do(alice, http.MethodPost, "/prompts/999999/bookmark", nil, map[string]string{"HX-Request": "true"}); code != http.StatusNotFound {
		t.Fatalf("bookmark missing prompt: code=%d", code)
	}
}

func TestVisitorCookie(t *testing.T) {
	app := newTestApp(t)
	c := app.client()
	app.get(c, "/", false)
	u, _ := url.Parse(app.srv.URL)
	cookies := c.Jar.Cookies(u)
	if len(cookies) != 1 || cookies[0].Name != visitorCookie || !validVisitorID(cookies[0].Value) {
		t.Fatalf("expected a valid visitor cookie, got %v", cookies)
	}
	id := cookies[0].Value
	app.get(c, "/", false)
	if got := c.Jar.Cookies(u)[0].Value; got != id {
		t.Fatal("visitor id should be stable across requests")
	}
}

func TestCategories(t *testing.T) {
	app := newTestApp(t)
	c := app.client()
	post := func(name string) (int, http.Header, string) {
		form := url.Values{"name": {name}}
		return app.do(c, http.MethodPost, "/categories", strings.NewReader(form.Encode()),
			map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	}

	if code, h, _ := post("Data Science"); code != http.StatusSeeOther || h.Get("Location") != "/categories" {
		t.Fatalf("create: code=%d", code)
	}
	if _, page := app.get(c, "/categories", false); !strings.Contains(page, `href="/?category=data-science"`) {
		t.Fatal("new category should be listed with its slug")
	}
	if code, _, page := post("data science"); code != http.StatusUnprocessableEntity || !strings.Contains(page, "already exists") {
		t.Fatalf("duplicate slug: code=%d", code)
	}
	if code, _, page := post("!!!"); code != http.StatusUnprocessableEntity || !strings.Contains(page, "at least one letter") {
		t.Fatalf("empty slug: code=%d", code)
	}
}

func TestNotFoundAndSecurityHeaders(t *testing.T) {
	app := newTestApp(t)
	c := app.client()
	for _, p := range []string{"/nope", "/prompts/abc", "/prompts/0", "/prompts/999999", "/prompts/999999/edit"} {
		if code, _ := app.get(c, p, false); code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, code)
		}
	}
	_, h, _ := app.do(c, http.MethodGet, "/", nil, nil)
	if h.Get("Content-Security-Policy") == "" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("security headers missing")
	}
}
