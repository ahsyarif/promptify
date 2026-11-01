package midtrans

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return &Client{ServerKey: "SB-Mid-server-test", BaseURL: ts.URL, HTTP: ts.Client()}
}

func TestChargeQRIS(t *testing.T) {
	var got map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/charge" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("SB-Mid-server-test:"))
		if r.Header.Get("Authorization") != wantAuth {
			t.Errorf("Authorization = %q, want %q", r.Header.Get("Authorization"), wantAuth)
		}
		json.NewDecoder(r.Body).Decode(&got)
		io.WriteString(w, `{
			"status_code": "201", "status_message": "QRIS transaction is created",
			"transaction_id": "tx-1", "order_id": "PRM-1-abc", "gross_amount": "49000.00",
			"transaction_status": "pending", "qr_string": "00020101021226",
			"expiry_time": "2026-11-01 17:15:00",
			"actions": [
				{"name": "generate-qr-code-v2", "method": "GET", "url": "https://api.sandbox.midtrans.com/v2/qris/tx-1/qr-code-v2"},
				{"name": "generate-qr-code", "method": "GET", "url": "https://api.sandbox.midtrans.com/v2/qris/tx-1/qr-code"}
			]}`)
	})

	ch, err := c.ChargeQRIS(context.Background(), "PRM-1-abc", 49000, "a@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got["payment_type"] != "qris" {
		t.Errorf("payment_type = %v", got["payment_type"])
	}
	td := got["transaction_details"].(map[string]any)
	if td["order_id"] != "PRM-1-abc" || td["gross_amount"] != float64(49000) {
		t.Errorf("transaction_details = %v", td)
	}
	if got["customer_details"].(map[string]any)["email"] != "a@example.com" {
		t.Errorf("customer_details = %v", got["customer_details"])
	}
	if ch.TransactionID != "tx-1" || ch.QRString != "00020101021226" {
		t.Errorf("charge = %+v", ch)
	}
	if ch.QRURL != "https://api.sandbox.midtrans.com/v2/qris/tx-1/qr-code" {
		t.Errorf("QRURL = %q, want the generate-qr-code action", ch.QRURL)
	}
	wantExpiry := time.Date(2026, 11, 1, 10, 15, 0, 0, time.UTC)
	if !ch.ExpiresAt.Equal(wantExpiry) {
		t.Errorf("ExpiresAt = %v, want %v (WIB is UTC+7)", ch.ExpiresAt.UTC(), wantExpiry)
	}
}

func TestChargeQRISMissingExpiryFallsBack(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"status_code":"201","transaction_id":"tx","actions":[{"name":"generate-qr-code","url":"https://x.midtrans.com/qr"}]}`)
	})
	ch, err := c.ChargeQRIS(context.Background(), "o", 1, "e@x.io")
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Until(ch.ExpiresAt); d < 14*time.Minute || d > 16*time.Minute {
		t.Errorf("fallback expiry in %v, want ~15m", d)
	}
}

func TestChargeQRISErrors(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"error status in body": func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"status_code":"401","status_message":"Unknown Merchant server_key/id"}`)
		},
		"validation error": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"status_code":"400","status_message":"One or more parameters in the payload is invalid.","validation_messages":["gross_amount is required"]}`)
		},
		"non-json": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			io.WriteString(w, `<html>bad gateway</html>`)
		},
		"no qr action": func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"status_code":"201","transaction_id":"tx","actions":[]}`)
		},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			c := newTestClient(t, h)
			if _, err := c.ChargeQRIS(context.Background(), "o", 1, "e@x.io"); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
	c := newTestClient(t, cases["validation error"])
	_, err := c.ChargeQRIS(context.Background(), "o", 1, "e@x.io")
	if !strings.Contains(err.Error(), "gross_amount is required") {
		t.Errorf("error should include validation messages: %v", err)
	}
}

func TestStatus(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/PRM-1-abc/status":
			io.WriteString(w, `{"status_code":"200","transaction_status":"settlement","fraud_status":"accept","gross_amount":"49000.00","transaction_id":"tx-1"}`)
		default:
			io.WriteString(w, `{"status_code":"404","status_message":"Transaction doesn't exist."}`)
		}
	})
	st, err := c.Status(context.Background(), "PRM-1-abc")
	if err != nil {
		t.Fatal(err)
	}
	if st.TransactionStatus != "settlement" || st.GrossAmount != "49000.00" || st.TransactionID != "tx-1" {
		t.Errorf("status = %+v", st)
	}
	if _, err := c.Status(context.Background(), "unknown"); err == nil || !strings.Contains(err.Error(), "doesn't exist") {
		t.Errorf("unknown order: err = %v", err)
	}
}

func TestVerifySignature(t *testing.T) {
	c := &Client{ServerKey: "SB-Mid-server-test"}
	n := Notification{OrderID: "PRM-1-abc", StatusCode: "200", GrossAmount: "49000.00"}
	n.SignatureKey = c.Signature(n.OrderID, n.StatusCode, n.GrossAmount)
	if len(n.SignatureKey) != 128 {
		t.Fatalf("signature should be SHA-512 hex, got %d chars", len(n.SignatureKey))
	}
	if !c.VerifySignature(n) {
		t.Fatal("valid signature rejected")
	}
	upper := n
	upper.SignatureKey = strings.ToUpper(n.SignatureKey)
	if !c.VerifySignature(upper) {
		t.Error("hex case should not matter")
	}

	for name, mutate := range map[string]func(*Notification){
		"amount":    func(n *Notification) { n.GrossAmount = "1.00" },
		"status":    func(n *Notification) { n.StatusCode = "201" },
		"order":     func(n *Notification) { n.OrderID = "PRM-2-abc" },
		"empty sig": func(n *Notification) { n.SignatureKey = "" },
		"wrong sig": func(n *Notification) { n.SignatureKey = strings.Repeat("0", 128) },
		"other key": func(n *Notification) {
			n.SignatureKey = (&Client{ServerKey: "x"}).Signature(n.OrderID, n.StatusCode, n.GrossAmount)
		},
	} {
		tampered := n
		mutate(&tampered)
		if c.VerifySignature(tampered) {
			t.Errorf("tampered %s accepted", name)
		}
	}
}

func TestSignatureKnownValue(t *testing.T) {
	// hashlib.sha512(b"order1" + b"200" + b"100000.00" + b"key").hexdigest()
	const want = "d31bfa5864bee4acb817d0c3c8cd1c52cc02512dd472e5fc8b1aae1b927bc2aee10266d065f6fe64248518470460c12ab8437a84ca46b7c3aa627ec2a4f26e0c"
	c := &Client{ServerKey: "key"}
	if got := c.Signature("order1", "200", "100000.00"); got != want {
		t.Errorf("Signature = %s, want %s", got, want)
	}
}

func TestMapStatus(t *testing.T) {
	cases := []struct{ tx, fraud, want string }{
		{"settlement", "", "paid"},
		{"settlement", "accept", "paid"},
		{"capture", "accept", "paid"},
		{"capture", "", "paid"},
		{"capture", "challenge", "pending"},
		{"pending", "", "pending"},
		{"authorize", "", "pending"},
		{"expire", "", "expired"},
		{"cancel", "", "failed"},
		{"deny", "", "failed"},
		{"failure", "", "failed"},
		{"refund", "", "pending"},
		{"", "", "pending"},
	}
	for _, c := range cases {
		if got := MapStatus(c.tx, c.fraud); got != c.want {
			t.Errorf("MapStatus(%q, %q) = %q, want %q", c.tx, c.fraud, got, c.want)
		}
	}
}

func TestFetchQR(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nfake")
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(png)
	})

	body, ctype, err := c.FetchQR(context.Background(), c.BaseURL+"/v2/qris/tx-1/qr-code")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(body)
	body.Close()
	if string(b) != string(png) || ctype != "image/png" {
		t.Errorf("got %q (%s)", b, ctype)
	}

	for _, u := range []string{
		"https://evil.example.com/qr.png",
		"https://midtrans.com.evil.com/qr.png",
		"http://api.sandbox.midtrans.com/qr.png",
		"https://user:pass@api.midtrans.com/qr.png",
		"file:///etc/passwd",
		"http://169.254.169.254/latest/meta-data",
		"::not a url",
	} {
		if _, _, err := c.FetchQR(context.Background(), u); err == nil {
			t.Errorf("FetchQR(%q) should be refused", u)
		}
	}
	if !c.allowedQRURL("https://api.sandbox.midtrans.com/v2/qris/tx/qr-code") || !c.allowedQRURL("https://api.midtrans.com/v2/qris/tx/qr-code") {
		t.Error("Midtrans hosts should be allowed")
	}
}
