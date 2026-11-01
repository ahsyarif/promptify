# AGENTS.md

Go + PostgreSQL + htmx prompt library. See README.md for the feature overview.

## Commands

- Build: `go build ./...`; vet: `go vet ./...`; format: `gofmt -w .`
- Test: `TEST_DATABASE_URL=postgres://... go test -race ./...` (DB tests skip without it; each test gets its own schema through `internal/testdb`)
- Run: `DATABASE_URL=postgres://... go run ./cmd/server`

## Conventions

- Standard library `net/http` routing (Go 1.22+ patterns) and `html/template`. The only dependency is `pgx/v5`.
- Templates and static files are embedded (`web/embed.go`). Rebuild or restart after changing them.
- Each page in `web/templates/pages/` is parsed with `layout.html` plus every partial. Partials are rendered on their own for htmx requests.
- Use `isPartialRequest` to decide between a fragment and a full page. History-restore requests need the full page. List responses set `Vary: HX-Request`.
- Migrations are numbered SQL files in `internal/db/migrations/`. They're applied once, in order, at startup. Never edit an applied migration. Add a new file instead.
- Search uses `store.PrefixTSQuery`, which strips everything except letters and digits. Keep user input out of raw tsquery syntax.
- Uploaded images get random names. Their type comes from file content (`http.DetectContentType`). Clean up the file whenever the DB write fails or the image is replaced or removed.
- CSP allows only `'self'` scripts, so don't add inline scripts or `hx-on` attributes. Put behaviour in `web/static/js/app.js`.
