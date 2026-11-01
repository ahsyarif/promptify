package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ahsyarif/promptify/internal/store"
	"github.com/ahsyarif/promptify/internal/testdb"
)

func TestUsers(t *testing.T) {
	ctx := context.Background()
	st := store.New(testdb.New(t))

	u, err := st.CreateUser(ctx, "alice@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if u.ID == 0 || !u.HasPassword() || u.IsMember() {
		t.Fatalf("new user = %+v", u)
	}
	if _, err := st.CreateUser(ctx, "ALICE@example.com", "other"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate email with different case: err = %v, want ErrConflict", err)
	}
	if got, err := st.UserByEmail(ctx, "Alice@Example.com"); err != nil || got.ID != u.ID {
		t.Fatalf("UserByEmail is case-insensitive: got %+v, %v", got, err)
	}
	if _, err := st.UserByEmail(ctx, "nobody@example.com"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown email: err = %v", err)
	}

	g, err := st.CreateGoogleUser(ctx, "bob@example.com", "google-bob")
	if err != nil {
		t.Fatal(err)
	}
	if g.HasPassword() || g.GoogleSub != "google-bob" {
		t.Fatalf("google user = %+v", g)
	}
	if got, err := st.UserByGoogleSub(ctx, "google-bob"); err != nil || got.ID != g.ID {
		t.Fatalf("UserByGoogleSub: %+v, %v", got, err)
	}
	if _, err := st.CreateGoogleUser(ctx, "bob2@example.com", "google-bob"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate google sub: err = %v", err)
	}
}

func TestLinkGoogleDropsPasswordAndSessions(t *testing.T) {
	ctx := context.Background()
	st := store.New(testdb.New(t))

	u, err := st.CreateUser(ctx, "carol@example.com", "hash")
	if err != nil {
		t.Fatal(err)
	}
	token := []byte("token-hash")
	if err := st.CreateSession(ctx, u.ID, token, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	linked, err := st.LinkGoogle(ctx, u.ID, "google-carol")
	if err != nil {
		t.Fatal(err)
	}
	if linked.GoogleSub != "google-carol" || linked.HasPassword() {
		t.Fatalf("linked user = %+v, want google sub set and password cleared", linked)
	}
	if _, err := st.UserBySession(ctx, token); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("existing sessions should be revoked, err = %v", err)
	}
	if _, err := st.LinkGoogle(ctx, u.ID, "google-other"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("relinking to another google account: err = %v, want ErrConflict", err)
	}

	other, _ := st.CreateUser(ctx, "dave@example.com", "hash")
	if _, err := st.LinkGoogle(ctx, other.ID, "google-carol"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("google account already used elsewhere: err = %v, want ErrConflict", err)
	}
}

func TestSessions(t *testing.T) {
	ctx := context.Background()
	st := store.New(testdb.New(t))
	u, _ := st.CreateUser(ctx, "erin@example.com", "hash")

	live, expired := []byte("live"), []byte("expired")
	if err := st.CreateSession(ctx, u.ID, live, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSession(ctx, u.ID, expired, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if got, err := st.UserBySession(ctx, live); err != nil || got.ID != u.ID {
		t.Fatalf("live session: %+v, %v", got, err)
	}
	if _, err := st.UserBySession(ctx, expired); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired session: err = %v, want ErrNotFound", err)
	}
	if err := st.DeleteSession(ctx, live); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UserBySession(ctx, live); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted session: err = %v", err)
	}
}

func newOrder(t *testing.T, st *store.Store, userID int64, orderID string, expires time.Time) store.Order {
	t.Helper()
	o, err := st.CreateOrder(context.Background(), store.Order{
		OrderID: orderID, UserID: userID, Amount: 49000, QRURL: "https://x.midtrans.com/qr", ExpiresAt: expires,
	})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestApplyOrderStatus(t *testing.T) {
	ctx := context.Background()
	st := store.New(testdb.New(t))
	u, _ := st.CreateUser(ctx, "frank@example.com", "hash")
	o := newOrder(t, st, u.ID, "PRM-1", time.Now().Add(15*time.Minute))
	if o.Status != store.OrderPending {
		t.Fatalf("new order status = %q", o.Status)
	}

	if got, _ := st.ApplyOrderStatus(ctx, "PRM-1", store.OrderPending, ""); got.Status != store.OrderPending {
		t.Fatalf("pending stays pending, got %q", got.Status)
	}

	paid, err := st.ApplyOrderStatus(ctx, "PRM-1", store.OrderPaid, "tx-1")
	if err != nil {
		t.Fatal(err)
	}
	if paid.Status != store.OrderPaid || paid.PaidAt == nil || paid.TransactionID != "tx-1" {
		t.Fatalf("paid order = %+v", paid)
	}
	member, _ := st.UserByID(ctx, u.ID)
	if !member.IsMember() {
		t.Fatal("paying should grant membership")
	}
	since := *member.MemberSince

	again, err := st.ApplyOrderStatus(ctx, "PRM-1", store.OrderPaid, "tx-1")
	if err != nil || !again.PaidAt.Equal(*paid.PaidAt) {
		t.Fatalf("replaying paid must be a no-op: %+v, %v", again, err)
	}
	if got, _ := st.ApplyOrderStatus(ctx, "PRM-1", store.OrderExpired, ""); got.Status != store.OrderPaid {
		t.Fatalf("paid must never be downgraded, got %q", got.Status)
	}

	// A second paid order must not move the membership date.
	newOrder(t, st, u.ID, "PRM-2", time.Now().Add(15*time.Minute))
	if _, err := st.ApplyOrderStatus(ctx, "PRM-2", store.OrderPaid, ""); err != nil {
		t.Fatal(err)
	}
	if again, _ := st.UserByID(ctx, u.ID); !again.MemberSince.Equal(since) {
		t.Fatalf("member_since changed from %v to %v", since, *again.MemberSince)
	}

	if _, err := st.ApplyOrderStatus(ctx, "nope", store.OrderPaid, ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown order: err = %v", err)
	}
	if _, err := st.ApplyOrderStatus(ctx, "PRM-1", "bogus", ""); err == nil {
		t.Fatal("invalid status should be rejected")
	}
}

func TestApplyOrderStatusExpiry(t *testing.T) {
	ctx := context.Background()
	st := store.New(testdb.New(t))
	u, _ := st.CreateUser(ctx, "gina@example.com", "hash")
	newOrder(t, st, u.ID, "PRM-E", time.Now().Add(15*time.Minute))

	expired, err := st.ApplyOrderStatus(ctx, "PRM-E", store.OrderExpired, "")
	if err != nil || expired.Status != store.OrderExpired || expired.PaidAt != nil {
		t.Fatalf("pending -> expired: %+v, %v", expired, err)
	}
	if got, _ := st.ApplyOrderStatus(ctx, "PRM-E", store.OrderFailed, ""); got.Status != store.OrderExpired {
		t.Fatalf("expired should not become failed, got %q", got.Status)
	}
	if u, _ := st.UserByID(ctx, u.ID); u.IsMember() {
		t.Fatal("an expired order must not grant membership")
	}

	// Midtrans can settle a payment made right at the expiry edge after we
	// gave up on it locally; the money arrived, so it must still count.
	late, err := st.ApplyOrderStatus(ctx, "PRM-E", store.OrderPaid, "tx-late")
	if err != nil || late.Status != store.OrderPaid {
		t.Fatalf("expired -> paid: %+v, %v", late, err)
	}
	if u, _ := st.UserByID(ctx, u.ID); !u.IsMember() {
		t.Fatal("late settlement should grant membership")
	}
}

func TestPendingOrderForUser(t *testing.T) {
	ctx := context.Background()
	st := store.New(testdb.New(t))
	u, _ := st.CreateUser(ctx, "hank@example.com", "hash")
	other, _ := st.CreateUser(ctx, "ivy@example.com", "hash")

	if _, err := st.PendingOrderForUser(ctx, u.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("no orders: err = %v", err)
	}
	newOrder(t, st, u.ID, "almost-expired", time.Now().Add(30*time.Second))
	if _, err := st.PendingOrderForUser(ctx, u.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("an order with under a minute left should not be reused")
	}
	newOrder(t, st, u.ID, "fresh", time.Now().Add(10*time.Minute))
	newOrder(t, st, other.ID, "someone-else", time.Now().Add(10*time.Minute))
	got, err := st.PendingOrderForUser(ctx, u.ID)
	if err != nil || got.OrderID != "fresh" {
		t.Fatalf("PendingOrderForUser = %+v, %v", got, err)
	}
	st.ApplyOrderStatus(ctx, "fresh", store.OrderFailed, "")
	if _, err := st.PendingOrderForUser(ctx, u.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("failed orders are not pending")
	}
}

func TestPremiumFlagAndSearch(t *testing.T) {
	ctx := context.Background()
	st := store.New(testdb.New(t))

	id, err := st.CreatePrompt(ctx, store.PromptInput{ImagePath: "test.png", Model: "midjourney", Title: "Hidden gem", Description: "Public teaser", Body: "zanzibar secret quixotry", IsPremium: true})
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := st.GetPrompt(ctx, id, "v"); !p.IsPremium {
		t.Fatal("is_premium should round-trip through create")
	}
	if err := st.UpdatePrompt(ctx, id, store.PromptInput{ImagePath: "test.png", Model: "midjourney", Title: "Hidden gem", Body: "zanzibar secret quixotry", IsPremium: false}); err != nil {
		t.Fatal(err)
	}
	if p, _ := st.GetPrompt(ctx, id, "v"); p.IsPremium {
		t.Fatal("is_premium should round-trip through update")
	}
	st.UpdatePrompt(ctx, id, store.PromptInput{ImagePath: "test.png", Model: "midjourney", Title: "Hidden gem", Description: "Public teaser", Body: "zanzibar secret quixotry", IsPremium: true})
	st.CreatePrompt(ctx, store.PromptInput{ImagePath: "test.png", Model: "midjourney", Title: "Free one", Body: "zanzibar for everyone"})

	search := func(q string, hide bool) []string {
		t.Helper()
		ps, _, err := st.ListPrompts(ctx, store.ListParams{Query: q, VisitorID: "v", HidePremiumBodies: hide, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		return titles(ps)
	}
	if got := search("zanzibar", false); len(got) != 2 {
		t.Fatalf("members search bodies of all prompts, got %v", got)
	}
	if got := search("zanzibar", true); len(got) != 1 || got[0] != "Free one" {
		t.Fatalf("non-members must not match premium bodies, got %v", got)
	}
	if got := search("quixot", true); len(got) != 0 {
		t.Fatalf("prefix search must not probe premium bodies either, got %v", got)
	}
	if got := search("hidden", true); len(got) != 1 || got[0] != "Hidden gem" {
		t.Fatalf("premium titles stay searchable, got %v", got)
	}
	if got := search("teaser", true); len(got) != 1 {
		t.Fatalf("premium descriptions stay searchable, got %v", got)
	}
}
