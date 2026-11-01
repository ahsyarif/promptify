package store_test

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/ahsyarif/promptify/internal/store"
	"github.com/ahsyarif/promptify/internal/testdb"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestImageLibraryMigration(t *testing.T) {
	ctx := context.Background()
	st := store.New(testdb.New(t))
	cats, err := st.ListCategories(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var slugs []string
	for _, c := range cats {
		slugs = append(slugs, c.Slug)
		if c.Slug != store.Slugify(c.Name) {
			t.Errorf("category %q has slug %q", c.Name, c.Slug)
		}
	}
	sort.Strings(slugs)
	want := []string{"3d-render", "anime-manga", "architecture-interior", "illustration", "logo-icon", "photography"}
	if !reflect.DeepEqual(slugs, want) {
		t.Fatalf("slugs = %v, want %v", slugs, want)
	}
	ps, more, err := st.ListPrompts(ctx, store.ListParams{Limit: 12})
	if err != nil || len(ps) != 0 || more {
		t.Fatalf("library should start empty: %v %v %v", ps, more, err)
	}
	_, err = st.CreatePrompt(ctx, store.PromptInput{Title: "Missing image", Body: "b", Model: "flux"})
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.ConstraintName != "prompts_image_required" {
		t.Fatalf("missing image must fail CHECK constraint: %v", err)
	}
	if _, err := st.CreateCategory(ctx, "Photography", ""); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate photography: %v", err)
	}
}

func TestImageMetadataRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := store.New(testdb.New(t))
	in := store.PromptInput{Title: "Portrait", Body: "A portrait of {{subject}}", ImagePath: "portrait.png", Model: "stable-diffusion", NegativePrompt: "blurry, text", AspectRatio: "4:5"}
	id, err := st.CreatePrompt(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		got, err := st.GetPrompt(ctx, id, "")
		if err != nil {
			t.Fatal(err)
		}
		if got.Model != in.Model || got.NegativePrompt != in.NegativePrompt || got.AspectRatio != in.AspectRatio || got.ImagePath != in.ImagePath {
			t.Fatalf("metadata = %+v, want %+v", got, in)
		}
		ps, _, err := st.ListPrompts(ctx, store.ListParams{Limit: 12})
		if err != nil || len(ps) != 1 || ps[0].Model != in.Model || ps[0].NegativePrompt != in.NegativePrompt || ps[0].AspectRatio != in.AspectRatio {
			t.Fatalf("listing metadata: %+v %v", ps, err)
		}
	}
	check()
	in.Model, in.NegativePrompt, in.AspectRatio, in.ImagePath = "flux", "watermark", "16:9", "landscape.png"
	if err := st.UpdatePrompt(ctx, id, in); err != nil {
		t.Fatal(err)
	}
	check()
	in.NegativePrompt, in.AspectRatio = "", ""
	if err := st.UpdatePrompt(ctx, id, in); err != nil {
		t.Fatal(err)
	}
	check()
}
