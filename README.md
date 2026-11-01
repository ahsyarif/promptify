# promptify

A minimalist prompt library built with **Go**, **PostgreSQL** and **htmx**.

- **Search**: live, prefix-matching full-text search (PostgreSQL `tsvector`, ranked by title > description > body)
- **Categories**: filter chips on the library page, a categories overview, and a form to add new ones
- **Images**: optional image per prompt (JPEG, PNG, GIF or WebP up to 5 MB, type detected from file content)
- **Bookmarks**: save prompts with one click; saved prompts live on their own searchable page
- `{{placeholders}}` in prompts are highlighted, and each prompt has a copy-to-clipboard button

No accounts are required. Bookmarks are tied to an anonymous, random `visitor_id` cookie, so they're per browser.

## Quick start (Docker)

```sh
docker compose up --build
```

Open <http://localhost:8080>. Migrations and starter prompts are applied automatically on boot.

## Local development

Requirements: Go 1.27+, PostgreSQL 14+.

```sh
make db     # starts only PostgreSQL via docker compose (or use your own)
make run    # http://localhost:8080
```

Configuration is through environment variables:

| Variable       | Default   | Description                          |
| -------------- | --------- | ------------------------------------ |
| `DATABASE_URL` | required  | PostgreSQL connection string         |
| `ADDR`         | `:8080`   | Listen address                       |
| `UPLOAD_DIR`   | `uploads` | Where uploaded images are stored     |

Templates and static assets are embedded into the binary, so restart `make run` after editing them.

## Tests

```sh
make test   # uses TEST_DATABASE_URL, defaulting to the local DATABASE_URL
```

Database tests create an isolated schema per test and drop it afterwards, so they never touch your data. They're skipped if `TEST_DATABASE_URL` isn't set.

## Project layout

```
cmd/server/            entrypoint: config, migrations, HTTP server, graceful shutdown
internal/db/           connection pool + embedded SQL migrations (schema and seed data)
internal/store/        data access: prompts, search, categories, bookmarks
internal/server/       routes, handlers, middleware, image uploads
internal/testdb/       per-test PostgreSQL schemas
web/templates/         html/template layout, pages and htmx partials
web/static/            CSS, vendored htmx, a small app.js (copy, image preview, "/" to search)
```

## How the htmx bits work

| Interaction       | Request                                    | Response                                  |
| ----------------- | ------------------------------------------ | ----------------------------------------- |
| Typing in search, picking a category | `GET /?q=…&category=…` with `HX-Request` | `results` partial, URL pushed to history |
| Load more         | `GET /?…&offset=N`                         | next `cards` plus a new "Load more" link  |
| Bookmark toggle   | `POST /prompts/{id}/bookmark`              | new button + out-of-band nav counter      |
| Delete            | `POST /prompts/{id}/delete` (`hx-confirm`) | `HX-Redirect: /`                          |

Every page also works without JavaScript: forms fall back to normal GET/POST requests.
