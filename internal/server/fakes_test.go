package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/ahsyarif/promptify/internal/payment/midtrans"
)

var fakeQR = []byte("\x89PNG\r\n\x1a\nfake-qr")

// fakeMidtrans implements the parts of the Midtrans Core API the app uses.
// It is a real HTTP server, so the production client code runs unchanged.
type fakeMidtrans struct {
	client *midtrans.Client

	mu         sync.Mutex
	charges    []map[string]any
	statuses   map[string]string // order_id -> transaction_status
	failCharge bool
}

func newFakeMidtrans(t *testing.T) *fakeMidtrans {
	t.Helper()
	f := &fakeMidtrans{statuses: map[string]string{}}
	mux := http.NewServeMux()
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	mux.HandleFunc("POST /v2/charge", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.charges = append(f.charges, body)
		fail := f.failCharge
		f.mu.Unlock()
		if fail {
			writeJSON(w, map[string]any{"status_code": "500", "status_message": "Sorry, we encountered internal server error"})
			return
		}
		orderID := body["transaction_details"].(map[string]any)["order_id"].(string)
		writeJSON(w, map[string]any{
			"status_code":        "201",
			"status_message":     "QRIS transaction is created",
			"transaction_id":     "tx-" + orderID,
			"order_id":           orderID,
			"transaction_status": "pending",
			"qr_string":          "00020101021226" + orderID,
			"expiry_time":        time.Now().Add(15 * time.Minute).In(time.FixedZone("WIB", 7*3600)).Format(time.DateTime),
			"actions": []map[string]string{
				{"name": "generate-qr-code", "method": "GET", "url": ts.URL + "/v2/qris/tx-" + orderID + "/qr-code"},
			},
		})
	})
	mux.HandleFunc("GET /v2/{orderID}/status", func(w http.ResponseWriter, r *http.Request) {
		orderID := r.PathValue("orderID")
		f.mu.Lock()
		st, ok := f.statuses[orderID]
		f.mu.Unlock()
		if !ok {
			st = "pending"
		}
		writeJSON(w, map[string]any{
			"status_code":        "200",
			"transaction_status": st,
			"gross_amount":       fmt.Sprintf("%d.00", testPrice),
			"transaction_id":     "tx-" + orderID,
		})
	})
	mux.HandleFunc("GET /v2/qris/{tx}/qr-code", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(fakeQR)
	})

	f.client = &midtrans.Client{ServerKey: "SB-Mid-server-test", BaseURL: ts.URL, HTTP: ts.Client()}
	return f
}

func (f *fakeMidtrans) setStatus(orderID, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses[orderID] = status
}

func (f *fakeMidtrans) chargeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.charges)
}

func (f *fakeMidtrans) lastCharge() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.charges[len(f.charges)-1]
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
