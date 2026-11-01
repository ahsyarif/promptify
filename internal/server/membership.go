package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ahsyarif/promptify/internal/payment/midtrans"
	"github.com/ahsyarif/promptify/internal/store"
)

const (
	maxNotificationBytes = 64 << 10
	maxQRBytes           = 1 << 20
	welcomeURL           = "/membership?welcome=1"
)

func orderURL(orderID string) string {
	return "/membership/orders/" + url.PathEscape(orderID)
}

func (s *Server) handleMembership(w http.ResponseWriter, r *http.Request) {
	s.renderMembership(w, r, http.StatusOK, "")
}

func (s *Server) renderMembership(w http.ResponseWriter, r *http.Request, status int, formError string) {
	data, err := s.baseData(r, "Membership", "membership")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data.PaymentsEnabled = s.cfg.Midtrans != nil
	data.Welcome = r.URL.Query().Get("welcome") == "1" && data.User.IsMember()
	data.FormError = formError
	s.render(w, status, "membership", data)
}

func (s *Server) handleCheckout(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	if u == nil {
		http.Redirect(w, r, "/login?next="+url.QueryEscape("/membership"), http.StatusSeeOther)
		return
	}
	if s.canViewPremium(u) {
		http.Redirect(w, r, "/membership", http.StatusSeeOther)
		return
	}
	if s.cfg.Midtrans == nil {
		s.renderMembership(w, r, http.StatusServiceUnavailable, "Payments are not available right now.")
		return
	}

	if o, err := s.store.PendingOrderForUser(r.Context(), u.ID); err == nil {
		http.Redirect(w, r, orderURL(o.OrderID), http.StatusSeeOther)
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		s.serverError(w, r, err)
		return
	}

	suffix := make([]byte, 4)
	rand.Read(suffix)
	orderID := "PRM-" + strconv.FormatInt(u.ID, 10) + "-" + hex.EncodeToString(suffix)
	charge, err := s.cfg.Midtrans.ChargeQRIS(r.Context(), orderID, s.cfg.MembershipPrice, u.Email)
	if err != nil {
		s.logger.Error("start payment", "order_id", orderID, "err", err)
		s.renderMembership(w, r, http.StatusBadGateway, "Couldn't start the payment. Please try again.")
		return
	}
	o, err := s.store.CreateOrder(r.Context(), store.Order{
		OrderID:       orderID,
		UserID:        u.ID,
		Amount:        s.cfg.MembershipPrice,
		TransactionID: charge.TransactionID,
		QRURL:         charge.QRURL,
		QRString:      charge.QRString,
		ExpiresAt:     charge.ExpiresAt,
	})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, orderURL(o.OrderID), http.StatusSeeOther)
}

// ownedOrder loads the order in the path if it belongs to the current user.
// Everyone else gets a 404, so order IDs can't be probed.
func (s *Server) ownedOrder(w http.ResponseWriter, r *http.Request) (store.Order, bool) {
	u := currentUser(r)
	if u == nil {
		s.handleNotFound(w, r)
		return store.Order{}, false
	}
	o, err := s.store.OrderByOrderID(r.Context(), r.PathValue("orderID"))
	if errors.Is(err, store.ErrNotFound) || (err == nil && o.UserID != u.ID) {
		s.handleNotFound(w, r)
		return store.Order{}, false
	}
	if err != nil {
		s.serverError(w, r, err)
		return store.Order{}, false
	}
	return o, true
}

func (s *Server) handleOrder(w http.ResponseWriter, r *http.Request) {
	o, ok := s.ownedOrder(w, r)
	if !ok {
		return
	}
	if o.Status == store.OrderPaid {
		http.Redirect(w, r, welcomeURL, http.StatusSeeOther)
		return
	}
	data, err := s.baseData(r, "Complete your payment", "membership")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	data.Order = o
	s.render(w, http.StatusOK, "checkout", data)
}

// handleOrderStatus is polled by the checkout page. It asks Midtrans directly,
// so payments are picked up even where the webhook can't reach us (localhost).
func (s *Server) handleOrderStatus(w http.ResponseWriter, r *http.Request) {
	o, ok := s.ownedOrder(w, r)
	if !ok {
		return
	}
	if r.Header.Get("HX-Request") != "true" {
		http.Redirect(w, r, orderURL(o.OrderID), http.StatusSeeOther)
		return
	}
	o, err := s.refreshOrder(r.Context(), o)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if o.Status == store.OrderPaid {
		w.Header().Set("HX-Redirect", welcomeURL)
	}
	s.renderPartial(w, http.StatusOK, "order-status", viewData{Order: o})
}

func (s *Server) refreshOrder(ctx context.Context, o store.Order) (store.Order, error) {
	if o.Status != store.OrderPending || s.cfg.Midtrans == nil {
		return o, nil
	}
	status, txID := store.OrderPending, ""
	st, err := s.cfg.Midtrans.Status(ctx, o.OrderID)
	if err != nil {
		s.logger.Warn("check payment status", "order_id", o.OrderID, "err", err)
	} else {
		status, txID = midtrans.MapStatus(st.TransactionStatus, st.FraudStatus), st.TransactionID
	}
	if status == store.OrderPending && time.Now().After(o.ExpiresAt) {
		status = store.OrderExpired
	}
	if status == store.OrderPending {
		return o, nil
	}
	return s.store.ApplyOrderStatus(ctx, o.OrderID, status, txID)
}

// handleOrderQR proxies the QR image from Midtrans, because the CSP only
// allows images from our own origin.
func (s *Server) handleOrderQR(w http.ResponseWriter, r *http.Request) {
	o, ok := s.ownedOrder(w, r)
	if !ok {
		return
	}
	if o.Status != store.OrderPending || s.cfg.Midtrans == nil {
		s.handleNotFound(w, r)
		return
	}
	body, _, err := s.cfg.Midtrans.FetchQR(r.Context(), o.QRURL)
	if err != nil {
		s.logger.Error("fetch qr code", "order_id", o.OrderID, "err", err)
		http.Error(w, "QR code unavailable", http.StatusBadGateway)
		return
	}
	defer body.Close()
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, no-store")
	io.Copy(w, io.LimitReader(body, maxQRBytes))
}

// handleMidtransNotification receives Midtrans's HTTP notifications. It is
// exempt from cross-origin protection and authenticated by signature instead.
func (s *Server) handleMidtransNotification(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Midtrans == nil {
		s.handleNotFound(w, r)
		return
	}
	var n midtrans.Notification
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxNotificationBytes)).Decode(&n); err != nil {
		http.Error(w, "invalid notification", http.StatusBadRequest)
		return
	}
	if !s.cfg.Midtrans.VerifySignature(n) {
		s.logger.Warn("midtrans notification with bad signature", "order_id", n.OrderID)
		http.Error(w, "invalid signature", http.StatusForbidden)
		return
	}
	o, err := s.store.OrderByOrderID(r.Context(), n.OrderID)
	if errors.Is(err, store.ErrNotFound) {
		// Acknowledge, so test notifications from the Midtrans dashboard
		// aren't retried forever.
		s.logger.Info("midtrans notification for unknown order", "order_id", n.OrderID)
		w.WriteHeader(http.StatusOK)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if amount, ok := parseGrossAmount(n.GrossAmount); !ok || amount != o.Amount {
		s.logger.Error("midtrans notification amount mismatch", "order_id", o.OrderID, "gross_amount", n.GrossAmount, "want", o.Amount)
		http.Error(w, "amount mismatch", http.StatusBadRequest)
		return
	}
	o, err = s.store.ApplyOrderStatus(r.Context(), o.OrderID, midtrans.MapStatus(n.TransactionStatus, n.FraudStatus), n.TransactionID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.logger.Info("midtrans notification", "order_id", o.OrderID, "transaction_status", n.TransactionStatus, "order_status", o.Status)
	w.WriteHeader(http.StatusOK)
}

// parseGrossAmount parses Midtrans amounts such as "49000.00". Rupiah has no
// minor unit in practice, so any non-zero fraction is rejected.
func parseGrossAmount(s string) (int64, bool) {
	whole, frac, _ := strings.Cut(s, ".")
	if strings.Trim(frac, "0") != "" {
		return 0, false
	}
	n, err := strconv.ParseInt(whole, 10, 64)
	return n, err == nil
}
