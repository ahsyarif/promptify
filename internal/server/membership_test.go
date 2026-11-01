package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ahsyarif/promptify/internal/payment/midtrans"
	"github.com/ahsyarif/promptify/internal/store"
)

// notify posts a Midtrans notification signed with the test server key.
func (a *testApp) notify(n midtrans.Notification, headers map[string]string) int {
	a.t.Helper()
	if n.SignatureKey == "" {
		n.SignatureKey = a.midtrans.client.Signature(n.OrderID, n.StatusCode, n.GrossAmount)
	}
	b, _ := json.Marshal(n)
	h := map[string]string{"Content-Type": "application/json"}
	for k, v := range headers {
		h[k] = v
	}
	code, _, _ := a.do(a.client(), http.MethodPost, "/payments/midtrans/notification", bytes.NewReader(b), h)
	return code
}

// checkout starts a payment and returns the order ID from the redirect.
func (a *testApp) checkout(c *http.Client) string {
	a.t.Helper()
	code, h, body := a.postForm(c, "/membership/checkout", nil, nil)
	loc := h.Get("Location")
	if code != http.StatusSeeOther || !strings.HasPrefix(loc, "/membership/orders/") {
		a.t.Fatalf("checkout: code=%d location=%q\n%s", code, loc, body)
	}
	return strings.TrimPrefix(loc, "/membership/orders/")
}

func (a *testApp) isMember(email string) bool {
	a.t.Helper()
	u, err := a.store.UserByEmail(context.Background(), email)
	if err != nil {
		a.t.Fatal(err)
	}
	return u.IsMember()
}

func TestPremiumBodiesAreRedacted(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	const secret = "PREMIUMSECRET"
	older, err := app.store.CreatePrompt(ctx, store.PromptInput{ImagePath: "test.png", Model: "midjourney", Title: "Older gem", Description: "Teaser", NegativePrompt: "NEGATIVESECRET one", Body: secret + " one zebracorn", IsPremium: true})
	if err != nil {
		t.Fatal(err)
	}
	for i := range pageSize {
		app.store.CreatePrompt(ctx, store.PromptInput{ImagePath: "test.png", Model: "midjourney", Title: fmt.Sprintf("Free %02d", i), Body: "free body"})
	}
	newer, _ := app.store.CreatePrompt(ctx, store.PromptInput{ImagePath: "test.png", Model: "midjourney", Title: "Newer gem", Description: "Teaser", NegativePrompt: "NEGATIVESECRET two", Body: secret + " two"})
	app.store.UpdatePrompt(ctx, newer, store.PromptInput{ImagePath: "test.png", Model: "midjourney", Title: "Newer gem", Description: "Teaser", NegativePrompt: "NEGATIVESECRET two", Body: secret + " two", IsPremium: true})

	pages := func(c *http.Client) map[string]string {
		out := map[string]string{}
		for _, p := range []struct {
			path string
			htmx bool
		}{
			{"/", false},
			{"/?q=gem", true},
			{fmt.Sprintf("/?offset=%d", pageSize), true},
			{fmt.Sprintf("/prompts/%d", older), false},
			{fmt.Sprintf("/prompts/%d", newer), false},
			{"/bookmarks", false},
		} {
			code, body := app.get(c, p.path, p.htmx)
			if code != http.StatusOK {
				t.Fatalf("GET %s: code=%d", p.path, code)
			}
			out[p.path] = body
		}
		return out
	}

	anon, user := app.client(), app.signup("user@example.com")
	for name, c := range map[string]*http.Client{"anonymous": anon, "non-member": user} {
		app.do(c, http.MethodPost, fmt.Sprintf("/prompts/%d/bookmark", older), nil, map[string]string{"HX-Request": "true"})
		for path, body := range pages(c) {
			if strings.Contains(body, secret) || strings.Contains(body, "NEGATIVESECRET") {
				t.Errorf("%s: %s leaks a premium body", name, path)
			}
		}
		_, detail := app.get(c, fmt.Sprintf("/prompts/%d", older), false)
		if !strings.Contains(detail, "This is a Premium image prompt") || !strings.Contains(detail, "Rp49.000") || strings.Contains(detail, "data-copy") {
			t.Errorf("%s: detail page should show the unlock panel instead of the prompt", name)
		}
		if !strings.Contains(detail, "Older gem") || !strings.Contains(detail, "Teaser") {
			t.Errorf("%s: title and description stay public", name)
		}
		if _, home := app.get(c, "/", false); !strings.Contains(home, "card-lock") || !strings.Contains(home, "tag-premium") {
			t.Errorf("%s: cards should show the premium lock", name)
		}
		if _, res := app.get(c, "/?q=zebracorn", true); strings.Contains(res, "Older gem") {
			t.Errorf("%s: searching for a word in a premium body must not find it", name)
		}
	}

	for name, c := range map[string]*http.Client{"member": app.member("member@example.com"), "admin": app.admin()} {
		_, detail := app.get(c, fmt.Sprintf("/prompts/%d", older), false)
		if !strings.Contains(detail, secret+" one") || !strings.Contains(detail, "NEGATIVESECRET one") || !strings.Contains(detail, `data-copy="#negative-body"`) {
			t.Errorf("%s should see the premium body and copy button", name)
		}
		if _, home := app.get(c, "/", false); strings.Contains(home, secret+" two") || strings.Contains(home, "NEGATIVESECRET") {
			t.Errorf("%s cards must not contain prompt texts", name)
		}
		if _, res := app.get(c, "/?q=zebracorn", true); !strings.Contains(res, "Older gem") {
			t.Errorf("%s should be able to search premium bodies", name)
		}
	}
}

func TestAdminCanTogglePremium(t *testing.T) {
	app := newTestApp(t)
	admin := app.admin()
	body, ctype := multipartBody(t, map[string]string{"model": "midjourney", "title": "Pro tip", "body": "secret sauce", "is_premium": "1"}, "test.png", pngBytes(t))
	_, h, _ := app.do(admin, http.MethodPost, "/prompts", body, map[string]string{"Content-Type": ctype})
	path := h.Get("Location")

	if _, page := app.get(app.client(), path, false); strings.Contains(page, "secret sauce") {
		t.Fatal("prompt created as premium should be locked for visitors")
	}
	if _, form := app.get(admin, path+"/edit", false); !strings.Contains(form, `value="1" checked`) {
		t.Fatal("edit form should keep the premium box ticked")
	}

	body, ctype = multipartBody(t, map[string]string{"model": "midjourney", "title": "Pro tip", "body": "secret sauce"}, "", nil)
	app.do(admin, http.MethodPost, path, body, map[string]string{"Content-Type": ctype})
	if _, page := app.get(app.client(), path, false); !strings.Contains(page, "secret sauce") {
		t.Fatal("unticking premium should make the prompt free")
	}
}

func TestMembershipPage(t *testing.T) {
	app := newTestApp(t)
	if _, page := app.get(app.client(), "/membership", false); !strings.Contains(page, "Rp49.000") || !strings.Contains(page, "Log in to unlock") {
		t.Fatal("visitors should see the price and a login button")
	}
	if _, page := app.get(app.signup("buyer@example.com"), "/membership", false); !strings.Contains(page, "Pay Rp49.000 with QRIS") {
		t.Fatal("logged-in users should see the pay button")
	}
	if _, page := app.get(app.admin(), "/membership", false); !strings.Contains(page, "As an admin") {
		t.Fatal("admins don't need to pay")
	}
	code, h, _ := app.postForm(app.client(), "/membership/checkout", nil, nil)
	if code != http.StatusSeeOther || h.Get("Location") != "/login?next=%2Fmembership" {
		t.Fatalf("anonymous checkout: code=%d location=%q", code, h.Get("Location"))
	}
}

func TestCheckoutAndStatusPoll(t *testing.T) {
	app := newTestApp(t)
	premium, _ := app.store.CreatePrompt(context.Background(), store.PromptInput{ImagePath: "test.png", Model: "midjourney", Title: "Gem", Body: "unlockable body", IsPremium: true})
	buyer := app.signup("buyer@example.com")

	orderID := app.checkout(buyer)
	if !strings.HasPrefix(orderID, "PRM-") {
		t.Fatalf("order id = %q", orderID)
	}
	charge := app.midtrans.lastCharge()
	td := charge["transaction_details"].(map[string]any)
	if charge["payment_type"] != "qris" || td["order_id"] != orderID || td["gross_amount"] != float64(testPrice) {
		t.Fatalf("charge sent to Midtrans = %v", charge)
	}
	if charge["customer_details"].(map[string]any)["email"] != "buyer@example.com" {
		t.Fatalf("charge should include the customer email: %v", charge)
	}

	if again := app.checkout(buyer); again != orderID || app.midtrans.chargeCount() != 1 {
		t.Fatalf("a second checkout should reuse the open order: %s vs %s, %d charges", again, orderID, app.midtrans.chargeCount())
	}

	orderPath := "/membership/orders/" + orderID
	code, page := app.get(buyer, orderPath, false)
	if code != http.StatusOK || !strings.Contains(page, orderPath+"/qr.png") || !strings.Contains(page, `hx-trigger="every 3s"`) || !strings.Contains(page, "Rp49.000") {
		t.Fatalf("order page: code=%d\n%s", code, page)
	}

	code, h, qr := app.do(buyer, http.MethodGet, orderPath+"/qr.png", nil, nil)
	if code != http.StatusOK || qr != string(fakeQR) || h.Get("Content-Type") != "image/png" || !strings.Contains(h.Get("Cache-Control"), "no-store") {
		t.Fatalf("qr proxy: code=%d type=%q cache=%q", code, h.Get("Content-Type"), h.Get("Cache-Control"))
	}
	for name, c := range map[string]*http.Client{"anonymous": app.client(), "another user": app.signup("other@example.com")} {
		for _, p := range []string{orderPath, orderPath + "/qr.png", orderPath + "/status"} {
			if code, _ := app.get(c, p, true); code != http.StatusNotFound {
				t.Errorf("%s GET %s: code=%d, want 404", name, p, code)
			}
		}
	}

	if code, h, _ := app.do(buyer, http.MethodGet, orderPath+"/status", nil, nil); code != http.StatusSeeOther || h.Get("Location") != orderPath {
		t.Fatalf("non-htmx status request should redirect to the order page, got %d", code)
	}
	code, h, body := app.do(buyer, http.MethodGet, orderPath+"/status", nil, map[string]string{"HX-Request": "true"})
	if code != http.StatusOK || !strings.Contains(body, "Waiting for payment") || h.Get("HX-Redirect") != "" {
		t.Fatalf("pending poll: code=%d HX-Redirect=%q\n%s", code, h.Get("HX-Redirect"), body)
	}

	app.midtrans.setStatus(orderID, "settlement")
	_, h, _ = app.do(buyer, http.MethodGet, orderPath+"/status", nil, map[string]string{"HX-Request": "true"})
	if h.Get("HX-Redirect") != welcomeURL {
		t.Fatalf("settled poll should redirect to %s, got %q", welcomeURL, h.Get("HX-Redirect"))
	}
	if !app.isMember("buyer@example.com") {
		t.Fatal("settlement seen by the poll should grant membership")
	}

	if _, page := app.get(buyer, welcomeURL, false); !strings.Contains(page, "welcome aboard") || !strings.Contains(page, "lifetime access since") {
		t.Fatal("membership page should welcome the new member")
	}
	if _, page := app.get(buyer, fmt.Sprintf("/prompts/%d", premium), false); !strings.Contains(page, "unlockable body") {
		t.Fatal("new members should see premium bodies")
	}
	if code, h, _ := app.postForm(buyer, "/membership/checkout", nil, nil); code != http.StatusSeeOther || h.Get("Location") != "/membership" {
		t.Fatalf("members can't buy twice: code=%d location=%q", code, h.Get("Location"))
	}
	if code, h, _ := app.do(buyer, http.MethodGet, orderPath, nil, nil); code != http.StatusSeeOther || h.Get("Location") != welcomeURL {
		t.Fatalf("a paid order page should redirect to the welcome page, got %d", code)
	}
	if code, _ := app.get(buyer, orderPath+"/qr.png", false); code != http.StatusNotFound {
		t.Fatalf("the QR of a paid order should no longer be served, got %d", code)
	}
}

func TestOrderExpiry(t *testing.T) {
	app := newTestApp(t)
	buyer := app.signup("late@example.com")
	u, _ := app.store.UserByEmail(context.Background(), "late@example.com")

	// Midtrans still says pending, but our copy of the deadline has passed.
	stale, _ := app.store.CreateOrder(context.Background(), store.Order{OrderID: "PRM-stale", UserID: u.ID, Amount: testPrice, ExpiresAt: time.Now().Add(-time.Minute)})
	_, body := app.get(buyer, "/membership/orders/"+stale.OrderID+"/status", true)
	if !strings.Contains(body, "expired") || !strings.Contains(body, "Generate a new QR code") || strings.Contains(body, "every 3s") {
		t.Fatalf("expired order should stop polling and offer a new QR:\n%s", body)
	}
	if !strings.Contains(body, `id="qr-panel" hx-swap-oob="true"`) {
		t.Fatal("the stale QR code should be removed from the page")
	}

	orderID := app.checkout(buyer)
	if orderID == stale.OrderID || app.midtrans.chargeCount() != 1 {
		t.Fatal("a new checkout should create a fresh charge")
	}
	app.midtrans.setStatus(orderID, "expire")
	if _, body := app.get(buyer, "/membership/orders/"+orderID+"/status", true); !strings.Contains(body, "expired") {
		t.Fatalf("Midtrans expiry should be reflected:\n%s", body)
	}
	if app.isMember("late@example.com") {
		t.Fatal("expired orders must not grant membership")
	}
}

func TestCheckoutMidtransError(t *testing.T) {
	app := newTestApp(t)
	app.midtrans.failCharge = true
	code, _, page := app.postForm(app.signup("unlucky@example.com"), "/membership/checkout", nil, nil)
	if code != http.StatusBadGateway || !strings.Contains(page, "Couldn&#39;t start the payment") {
		t.Fatalf("charge failure: code=%d", code)
	}
	u, _ := app.store.UserByEmail(context.Background(), "unlucky@example.com")
	if _, err := app.store.PendingOrderForUser(context.Background(), u.ID); err == nil {
		t.Fatal("no order should be stored when the charge fails")
	}
}

func TestPaymentsDisabled(t *testing.T) {
	app := newTestApp(t, func(c *Config) { c.Midtrans = nil })
	user := app.signup("someone@example.com")
	if _, page := app.get(user, "/membership", false); !strings.Contains(page, "Payments are not available") || strings.Contains(page, "with QRIS</button>") {
		t.Fatal("membership page should say payments are unavailable")
	}
	if code, _, _ := app.postForm(user, "/membership/checkout", nil, nil); code != http.StatusServiceUnavailable {
		t.Fatalf("checkout without Midtrans: code=%d, want 503", code)
	}
	if code, _, _ := app.do(app.client(), http.MethodPost, "/payments/midtrans/notification", strings.NewReader("{}"), nil); code != http.StatusNotFound {
		t.Fatalf("webhook without Midtrans: code=%d, want 404", code)
	}
}

func TestMidtransWebhook(t *testing.T) {
	app := newTestApp(t)
	premium, _ := app.store.CreatePrompt(context.Background(), store.PromptInput{ImagePath: "test.png", Model: "midjourney", Title: "Gem", Body: "webhook unlocked", IsPremium: true})
	buyer := app.signup("hooked@example.com")
	orderID := app.checkout(buyer)
	settled := midtrans.Notification{
		OrderID: orderID, StatusCode: "200", GrossAmount: fmt.Sprintf("%d.00", testPrice),
		TransactionStatus: "settlement", FraudStatus: "accept", TransactionID: "tx-webhook",
	}

	forged := settled
	forged.SignatureKey = strings.Repeat("ab", 64)
	if code := app.notify(forged, nil); code != http.StatusForbidden {
		t.Fatalf("bad signature: code=%d, want 403", code)
	}
	if code, _, _ := app.do(app.client(), http.MethodPost, "/payments/midtrans/notification", strings.NewReader("not json"), nil); code != http.StatusBadRequest {
		t.Fatalf("malformed body: code=%d, want 400", code)
	}
	cheap := settled
	cheap.GrossAmount = "1000.00"
	if code := app.notify(cheap, nil); code != http.StatusBadRequest {
		t.Fatalf("amount mismatch: code=%d, want 400", code)
	}
	unknown := settled
	unknown.OrderID = "dashboard-test"
	if code := app.notify(unknown, nil); code != http.StatusOK {
		t.Fatalf("unknown order should be acknowledged: code=%d", code)
	}
	if app.isMember("hooked@example.com") {
		t.Fatal("no notification so far should have granted membership")
	}

	if code := app.notify(settled, nil); code != http.StatusOK {
		t.Fatalf("valid settlement: code=%d", code)
	}
	if !app.isMember("hooked@example.com") {
		t.Fatal("settlement should grant membership")
	}
	o, _ := app.store.OrderByOrderID(context.Background(), orderID)
	if o.Status != store.OrderPaid || o.TransactionID != "tx-webhook" {
		t.Fatalf("order after settlement = %+v", o)
	}
	if _, page := app.get(buyer, fmt.Sprintf("/prompts/%d", premium), false); !strings.Contains(page, "webhook unlocked") {
		t.Fatal("premium body should be visible after the webhook")
	}

	if code := app.notify(settled, nil); code != http.StatusOK {
		t.Fatalf("replayed notification: code=%d", code)
	}
	expired := settled
	expired.StatusCode, expired.TransactionStatus = "202", "expire"
	app.notify(expired, nil)
	if o, _ := app.store.OrderByOrderID(context.Background(), orderID); o.Status != store.OrderPaid {
		t.Fatalf("a late expire notification must not undo a payment, status=%q", o.Status)
	}
}

func TestParseGrossAmount(t *testing.T) {
	cases := map[string]struct {
		want int64
		ok   bool
	}{
		"49000.00": {49000, true},
		"49000":    {49000, true},
		"49000.0":  {49000, true},
		"49000.50": {0, false},
		"":         {0, false},
		"abc":      {0, false},
		"1e5":      {0, false},
	}
	for in, c := range cases {
		got, ok := parseGrossAmount(in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parseGrossAmount(%q) = %d, %v; want %d, %v", in, got, ok, c.want, c.ok)
		}
	}
}

func TestOrderURLEscaping(t *testing.T) {
	if got := orderURL("PRM-1-ab/../x"); got != "/membership/orders/"+url.PathEscape("PRM-1-ab/../x") {
		t.Fatalf("orderURL = %q", got)
	}
}
