package store_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/ahsyarif/promptify/internal/store"
	"github.com/ahsyarif/promptify/internal/testdb"
)

func TestPrefixTSQuery(t *testing.T) {
	cases := map[string]string{
		"":                       "",
		"   ":                    "",
		"code":                   "code:*",
		"Code Review":            "code:* & review:*",
		"sql!' | & :* drop":      "sql:* & drop:*",
		"résumé writer":          "résumé:* & writer:*",
		"gpt-4 prompts":          "gpt:* & 4:* & prompts:*",
		"!!!":                    "",
		"line\tbreak\nand space": "line:* & break:* & and:* & space:*",
	}
	for in, want := range cases {
		if got := store.PrefixTSQuery(in); got != want {
			t.Errorf("PrefixTSQuery(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Writing":           "writing",
		"  Data Science  ":  "data-science",
		"AI & ML / Ops":     "ai-ml-ops",
		"Café Prompts!":     "café-prompts",
		"---":               "",
		"multiple   spaces": "multiple-spaces",
	}
	for in, want := range cases {
		if got := store.Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func titles(ps []store.Prompt) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Title
	}
	return out
}

func TestListPrompts(t *testing.T) {
	ctx := context.Background()
	st := store.New(testdb.New(t))

	cat, err := st.CreateCategory(ctx, "Testing Things", "")
	if err != nil {
		t.Fatal(err)
	}
	mustCreate := func(in store.PromptInput) int64 {
		t.Helper()
		id, err := st.CreatePrompt(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	zebraID := mustCreate(store.PromptInput{Title: "Zebra stripes", Body: "Describe the pattern", CategoryID: &cat.ID})
	mustCreate(store.PromptInput{Title: "Unrelated", Body: "Something about zebras in the body", CategoryID: &cat.ID})

	t.Run("full-text search ranks title matches first", func(t *testing.T) {
		ps, _, err := st.ListPrompts(ctx, store.ListParams{Query: "zebra", Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(ps) != 2 || ps[0].ID != zebraID {
			t.Fatalf("got %v, want Zebra stripes first of 2", titles(ps))
		}
	})

	t.Run("prefix matching while typing", func(t *testing.T) {
		ps, _, err := st.ListPrompts(ctx, store.ListParams{Query: "zeb", Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(ps) != 2 {
			t.Fatalf("got %v, want 2 results", titles(ps))
		}
	})

	t.Run("special characters are safe", func(t *testing.T) {
		for _, q := range []string{"'; DROP TABLE prompts; --", "a & | ! :*", "100%", `\`, "_"} {
			if _, _, err := st.ListPrompts(ctx, store.ListParams{Query: q, Limit: 10}); err != nil {
				t.Errorf("query %q: %v", q, err)
			}
		}
	})

	t.Run("category filter", func(t *testing.T) {
		ps, _, err := st.ListPrompts(ctx, store.ListParams{CategorySlug: "testing-things", Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(ps) != 2 {
			t.Fatalf("got %v, want 2 prompts in category", titles(ps))
		}
		for _, p := range ps {
			if p.CategoryName != "Testing Things" {
				t.Errorf("prompt %q has category %q", p.Title, p.CategoryName)
			}
		}
	})

	t.Run("search combined with category", func(t *testing.T) {
		ps, _, err := st.ListPrompts(ctx, store.ListParams{Query: "editor", CategorySlug: "testing-things", Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(ps) != 0 {
			t.Fatalf("got %v, want no results", titles(ps))
		}
	})
}

func TestListPromptsPagination(t *testing.T) {
	ctx := context.Background()
	st := store.New(testdb.New(t))
	cat, err := st.CreateCategory(ctx, "Paged", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		if _, err := st.CreatePrompt(ctx, store.PromptInput{Title: fmt.Sprintf("P%d", i), Body: "b", CategoryID: &cat.ID}); err != nil {
			t.Fatal(err)
		}
	}

	seen := map[int64]bool{}
	params := store.ListParams{CategorySlug: "paged", Limit: 2}
	for page := 0; ; page++ {
		ps, hasMore, err := st.ListPrompts(ctx, params)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range ps {
			if seen[p.ID] {
				t.Fatalf("prompt %d returned twice", p.ID)
			}
			seen[p.ID] = true
		}
		if !hasMore {
			if page != 2 || len(ps) != 1 {
				t.Fatalf("ended on page %d with %d items, want page 2 with 1 item", page, len(ps))
			}
			break
		}
		params.Offset += params.Limit
	}
	if len(seen) != 5 {
		t.Fatalf("saw %d prompts, want 5", len(seen))
	}
}

func TestBookmarks(t *testing.T) {
	ctx := context.Background()
	st := store.New(testdb.New(t))
	id, err := st.CreatePrompt(ctx, store.PromptInput{Title: "Keep me", Body: "b"})
	if err != nil {
		t.Fatal(err)
	}
	alice, bob := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	saved, err := st.ToggleBookmark(ctx, alice, id)
	if err != nil || !saved {
		t.Fatalf("first toggle = %v, %v; want saved", saved, err)
	}

	p, err := st.GetPrompt(ctx, id, alice)
	if err != nil || !p.Bookmarked {
		t.Fatalf("alice should see bookmark: %+v %v", p, err)
	}
	p, err = st.GetPrompt(ctx, id, bob)
	if err != nil || p.Bookmarked {
		t.Fatalf("bob should not see alice's bookmark: %+v %v", p, err)
	}

	ps, _, err := st.ListPrompts(ctx, store.ListParams{VisitorID: alice, BookmarkedOnly: true, Limit: 10})
	if err != nil || len(ps) != 1 || ps[0].ID != id {
		t.Fatalf("alice bookmarks = %v, %v", titles(ps), err)
	}
	ps, _, err = st.ListPrompts(ctx, store.ListParams{VisitorID: bob, BookmarkedOnly: true, Limit: 10})
	if err != nil || len(ps) != 0 {
		t.Fatalf("bob bookmarks = %v, %v", titles(ps), err)
	}

	saved, err = st.ToggleBookmark(ctx, alice, id)
	if err != nil || saved {
		t.Fatalf("second toggle = %v, %v; want removed", saved, err)
	}
	if n, _ := st.CountBookmarks(ctx, alice); n != 0 {
		t.Fatalf("count after removal = %d", n)
	}

	if _, err := st.ToggleBookmark(ctx, alice, 999999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("bookmarking missing prompt: err = %v, want ErrNotFound", err)
	}

	st.ToggleBookmark(ctx, alice, id)
	if _, err := st.DeletePrompt(ctx, id); err != nil {
		t.Fatal(err)
	}
	if n, _ := st.CountBookmarks(ctx, alice); n != 0 {
		t.Fatalf("bookmarks should cascade on prompt delete, count = %d", n)
	}
}

func TestCreateCategoryConflict(t *testing.T) {
	ctx := context.Background()
	st := store.New(testdb.New(t))
	if _, err := st.CreateCategory(ctx, "Writing", ""); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate seeded category: err = %v, want ErrConflict", err)
	}
}

func TestPromptNotFound(t *testing.T) {
	ctx := context.Background()
	st := store.New(testdb.New(t))
	if _, err := st.GetPrompt(ctx, 999999, ""); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetPrompt: %v", err)
	}
	if err := st.UpdatePrompt(ctx, 999999, store.PromptInput{Title: "x", Body: "y"}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("UpdatePrompt: %v", err)
	}
	if _, err := st.DeletePrompt(ctx, 999999); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("DeletePrompt: %v", err)
	}
}
