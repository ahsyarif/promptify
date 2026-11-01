# promptify

A minimalist image prompt library for **Midjourney**, **Stable Diffusion**, **DALL·E** and **Flux**, built with **Go**, **PostgreSQL** and **htmx**.

- **Search**: live, prefix-matching full-text search (PostgreSQL `tsvector`, ranked by title > description > body)
- **Categories**: filter chips on the library page, a categories overview, and a form to add new ones
- **Image gallery**: every prompt requires an example image (JPEG, PNG, GIF or WebP up to 5 MB, type detected from file content). Uniform square crops in a responsive 3-, 2- or 1-column gallery; uncropped images on the two-column detail page
- **Generation settings**: required model, optional aspect ratio and negative prompt, with separate copy buttons for both prompt texts
- **Bookmarks**: save prompts with one click; saved prompts live on their own searchable page
- `{{placeholders}}` in prompts are highlighted, and each prompt has a copy-to-clipboard button
- **Accounts**: sign up with email and password, or continue with Google
- **Premium prompts and lifetime membership**: admins can mark a prompt Premium. Its title, description and image stay public, but only members can read the prompt and negative prompt. Membership is a one-time payment by QRIS (GoPay, OVO, DANA, ShopeePay, mobile banking) through Midtrans
- **Admins**: only accounts listed in `ADMIN_EMAILS` can add, edit or delete prompts and add categories

Browsing, searching and bookmarking need no account. Bookmarks are tied to an anonymous, random `visitor_id` cookie, so they're per browser. You only need to log in to buy a membership.

## Quick start (Docker)

```sh
docker compose up --build
```

Open <http://localhost:8080>. Migrations are applied automatically on boot. The library starts empty with six image-style categories: Photography, Illustration, 3D & Render, Anime & Manga, Logo & Icon, and Architecture & Interior. Admins upload prompts and their example images.

To manage content, put your email in `ADMIN_EMAILS` and sign up with it. Compose reads variables from a `.env` file next to `docker-compose.yml`:

```sh
ADMIN_EMAILS=you@example.com
MIDTRANS_SERVER_KEY=SB-Mid-server-...   # optional, enables payments
GOOGLE_CLIENT_ID=...                    # optional, enables Google login
GOOGLE_CLIENT_SECRET=...
```

## Upgrading an existing library

**Back up your database before starting this version.** Migration `004_image_library.sql` permanently deletes all existing prompts, their bookmarks and all categories. It replaces the categories with the six image styles and requires an example image for every new prompt. Users, sessions, orders and lifetime memberships are preserved. The reset runs once, during startup; subsequent restarts keep your new content.

The database migration cannot delete files. Old image files in `UPLOAD_DIR` (the Docker `uploads` volume) become orphaned after the reset. After backing them up, you can remove those old files manually. Do this before uploading new prompts, or identify only unreferenced files so that new example images are not deleted.

## Local development

Requirements: Go 1.27+, PostgreSQL 14+.

```sh
make db     # starts only PostgreSQL via docker compose (or use your own)
make run    # http://localhost:8080
```

Configuration is through environment variables:

| Variable               | Default   | Description                                                                 |
| ---------------------- | --------- | --------------------------------------------------------------------------- |
| `DATABASE_URL`         | required  | PostgreSQL connection string                                                |
| `ADDR`                 | `:8080`   | Listen address                                                              |
| `UPLOAD_DIR`           | `uploads` | Where uploaded images are stored                                            |
| `PUBLIC_URL`           |           | Public base URL, e.g. `https://prompts.example.com`. Required for Google login |
| `ADMIN_EMAILS`         |           | Comma-separated emails that may manage prompts and categories               |
| `MEMBERSHIP_PRICE`     | `49000`   | Lifetime membership price in whole rupiah                                   |
| `MIDTRANS_SERVER_KEY`  |           | Midtrans server key. Payments are disabled when empty                       |
| `MIDTRANS_PRODUCTION`  | `false`   | `true` uses the production API instead of the sandbox                       |
| `GOOGLE_CLIENT_ID`     |           | Google OAuth client ID. Google login is hidden when empty                   |
| `GOOGLE_CLIENT_SECRET` |           | Google OAuth client secret                                                  |

Templates and static assets are embedded into the binary, so restart `make run` after editing them.

Session cookies get the `Secure` flag when the request arrives over TLS or with `X-Forwarded-Proto: https`, so put the app behind HTTPS in production.

## Payments (Midtrans QRIS)

1. Create a [Midtrans](https://midtrans.com) account and copy the **sandbox** server key (`SB-Mid-server-…`) from *Settings → Access Keys* into `MIDTRANS_SERVER_KEY`.
2. In *Settings → Payment → Notification URL*, set the Payment Notification URL to `{PUBLIC_URL}/payments/midtrans/notification`.
3. Log in, open **Membership** and click **Pay with QRIS**. To pay in the sandbox, paste the QR image URL into the [Midtrans QRIS simulator](https://simulator.sandbox.midtrans.com/v2/qris/index).

The checkout page polls the order status every 3 seconds and asks Midtrans directly, so payments are picked up even when the notification URL can't reach you (for example on localhost). Notifications are verified by their SHA-512 signature and gross amount. A paid order is never downgraded, and a payment that settles after the QR code expired still grants membership.

When you go live, set `MIDTRANS_PRODUCTION=true` and use the production server key.

## Google login

Create an OAuth client of type *Web application* in the [Google Cloud console](https://console.cloud.google.com/apis/credentials) and add `{PUBLIC_URL}/auth/google/callback` as an authorised redirect URI. For local use, that's `http://localhost:8080/auth/google/callback` with `PUBLIC_URL=http://localhost:8080`.

The flow uses PKCE and a state cookie, and only accepts Google accounts with a verified email. If someone already signed up with a password using that email, the Google login takes over the account: the password is removed and existing sessions are logged out, because password signups don't verify email ownership.

## Tests

```sh
make test   # uses TEST_DATABASE_URL, defaulting to the local DATABASE_URL
```

Database tests create an isolated schema per test and drop it afterwards, so they never touch your data. They're skipped if `TEST_DATABASE_URL` isn't set.

## Project layout

```
cmd/server/            entrypoint: config, migrations, HTTP server, graceful shutdown
internal/db/           connection pool + embedded SQL migrations (schema and seed data)
internal/store/        data access: prompts, search, categories, bookmarks, users, sessions, orders
internal/server/       routes, handlers, middleware, image uploads, auth, Google login, membership
internal/payment/      Midtrans Core API client (QRIS charge, status, signatures)
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
| Payment status    | `GET /membership/orders/{id}/status` every 3s | status partial; `HX-Redirect` once paid |

Every page also works without JavaScript: forms fall back to normal GET/POST requests.
