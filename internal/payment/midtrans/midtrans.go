// Package midtrans is a minimal client for the Midtrans Core API, covering
// QRIS charges, transaction status and HTTP notifications.
package midtrans

import (
	"bytes"
	"context"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	SandboxURL    = "https://api.sandbox.midtrans.com"
	ProductionURL = "https://api.midtrans.com"
)

const expiryMinutes = 15

// Midtrans reports times in Western Indonesia Time, which has no DST. A fixed
// zone avoids depending on tzdata, which minimal container images lack.
var jakarta = time.FixedZone("WIB", 7*60*60)

type Client struct {
	ServerKey string
	BaseURL   string
	HTTP      *http.Client
}

func New(serverKey string, production bool) *Client {
	base := SandboxURL
	if production {
		base = ProductionURL
	}
	return &Client{ServerKey: serverKey, BaseURL: base, HTTP: &http.Client{Timeout: 15 * time.Second}}
}

type Charge struct {
	TransactionID     string
	TransactionStatus string
	QRURL             string
	QRString          string
	ExpiresAt         time.Time
}

type apiResponse struct {
	StatusCode        string   `json:"status_code"`
	StatusMessage     string   `json:"status_message"`
	ValidationErrors  []string `json:"validation_messages"`
	TransactionID     string   `json:"transaction_id"`
	TransactionStatus string   `json:"transaction_status"`
	FraudStatus       string   `json:"fraud_status"`
	GrossAmount       string   `json:"gross_amount"`
	QRString          string   `json:"qr_string"`
	ExpiryTime        string   `json:"expiry_time"`
	Actions           []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"actions"`
}

func (r apiResponse) err(op string) error {
	msg := r.StatusMessage
	if len(r.ValidationErrors) > 0 {
		msg += ": " + strings.Join(r.ValidationErrors, "; ")
	}
	return fmt.Errorf("midtrans %s: status %s: %s", op, r.StatusCode, msg)
}

// ChargeQRIS creates a QRIS transaction that expires after 15 minutes.
func (c *Client) ChargeQRIS(ctx context.Context, orderID string, amount int64, email string) (Charge, error) {
	body := map[string]any{
		"payment_type":        "qris",
		"transaction_details": map[string]any{"order_id": orderID, "gross_amount": amount},
		"qris":                map[string]any{"acquirer": "gopay"},
		"custom_expiry":       map[string]any{"expiry_duration": expiryMinutes, "unit": "minute"},
		"customer_details":    map[string]any{"email": email},
	}
	var resp apiResponse
	if err := c.do(ctx, http.MethodPost, "/v2/charge", body, &resp); err != nil {
		return Charge{}, fmt.Errorf("midtrans charge: %w", err)
	}
	if resp.StatusCode != "201" {
		return Charge{}, resp.err("charge")
	}

	ch := Charge{
		TransactionID:     resp.TransactionID,
		TransactionStatus: resp.TransactionStatus,
		QRString:          resp.QRString,
		ExpiresAt:         time.Now().Add(expiryMinutes * time.Minute),
	}
	for _, a := range resp.Actions {
		if a.Name == "generate-qr-code" {
			ch.QRURL = a.URL
		}
	}
	if ch.QRURL == "" {
		return Charge{}, errors.New("midtrans charge: response has no generate-qr-code action")
	}
	if t, err := time.ParseInLocation(time.DateTime, resp.ExpiryTime, jakarta); err == nil {
		ch.ExpiresAt = t
	}
	return ch, nil
}

type StatusResult struct {
	TransactionStatus string
	FraudStatus       string
	StatusCode        string
	GrossAmount       string
	TransactionID     string
}

func (c *Client) Status(ctx context.Context, orderID string) (StatusResult, error) {
	var resp apiResponse
	if err := c.do(ctx, http.MethodGet, "/v2/"+url.PathEscape(orderID)+"/status", nil, &resp); err != nil {
		return StatusResult{}, fmt.Errorf("midtrans status: %w", err)
	}
	if resp.TransactionStatus == "" {
		return StatusResult{}, resp.err("status")
	}
	return StatusResult{
		TransactionStatus: resp.TransactionStatus,
		FraudStatus:       resp.FraudStatus,
		StatusCode:        resp.StatusCode,
		GrossAmount:       resp.GrossAmount,
		TransactionID:     resp.TransactionID,
	}, nil
}

// Notification is the payload Midtrans POSTs to the payment notification URL.
type Notification struct {
	OrderID           string `json:"order_id"`
	StatusCode        string `json:"status_code"`
	GrossAmount       string `json:"gross_amount"`
	SignatureKey      string `json:"signature_key"`
	TransactionStatus string `json:"transaction_status"`
	FraudStatus       string `json:"fraud_status"`
	TransactionID     string `json:"transaction_id"`
}

func (c *Client) Signature(orderID, statusCode, grossAmount string) string {
	sum := sha512.Sum512([]byte(orderID + statusCode + grossAmount + c.ServerKey))
	return hex.EncodeToString(sum[:])
}

// VerifySignature reports whether the notification was signed with our
// server key, i.e. that it really comes from Midtrans and wasn't altered.
func (c *Client) VerifySignature(n Notification) bool {
	want := c.Signature(n.OrderID, n.StatusCode, n.GrossAmount)
	return subtle.ConstantTimeCompare([]byte(want), []byte(strings.ToLower(n.SignatureKey))) == 1
}

// MapStatus converts a Midtrans transaction status into an order status:
// "pending", "paid", "expired" or "failed".
func MapStatus(transactionStatus, fraudStatus string) string {
	switch transactionStatus {
	case "settlement":
		return "paid"
	case "capture":
		if fraudStatus == "" || fraudStatus == "accept" {
			return "paid"
		}
		return "pending"
	case "expire":
		return "expired"
	case "cancel", "deny", "failure":
		return "failed"
	default:
		return "pending"
	}
}

// FetchQR downloads the QR code image for a charge. Only Midtrans hosts are
// allowed so the server can't be used as an open proxy.
func (c *Client) FetchQR(ctx context.Context, rawURL string) (io.ReadCloser, string, error) {
	if !c.allowedQRURL(rawURL) {
		return nil, "", fmt.Errorf("midtrans qr: refusing to fetch %q", rawURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("midtrans qr: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, "", fmt.Errorf("midtrans qr: unexpected HTTP status %d", resp.StatusCode)
	}
	return resp.Body, resp.Header.Get("Content-Type"), nil
}

func (c *Client) allowedQRURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.User != nil {
		return false
	}
	base, err := url.Parse(c.BaseURL)
	if err != nil {
		return false
	}
	if u.Scheme == base.Scheme && u.Host == base.Host {
		return true
	}
	host := u.Hostname()
	return u.Scheme == "https" && (host == "midtrans.com" || strings.HasSuffix(host, ".midtrans.com"))
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reqBody)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.ServerKey, "")
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
		return fmt.Errorf("HTTP %d: decode response: %w", resp.StatusCode, err)
	}
	return nil
}
