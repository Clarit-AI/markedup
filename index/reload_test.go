package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestReload_IsEquivalentToLoad verifies that Reload produces the same index
// as Load given the same inputs, and that calling it twice on the same root
// yields equivalent indexes.
func TestReload_IsEquivalentToLoad(t *testing.T) {
	dir := t.TempDir()

	// Seed with two simple pages.
	writeTestPage(t, dir, "a.md", "# Page A\n\nContent.\n")
	writeTestPage(t, dir, "b.md", "# Page B\n\n[[a]]\n")

	ctx := context.Background()

	loadRes, err := Load(ctx, dir, WithAutoEnrich(true))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	reloadRes, err := Reload(ctx, dir, WithAutoEnrich(true))
	if err != nil {
		t.Fatalf("Reload failed: %v", err)
	}

	if loadRes.Index.Pages() != reloadRes.Index.Pages() {
		t.Errorf("page counts differ: Load=%d Reload=%d",
			loadRes.Index.Pages(), reloadRes.Index.Pages())
	}
	if loadRes.Index.Relationships() != reloadRes.Index.Relationships() {
		t.Errorf("relationship counts differ: Load=%d Reload=%d",
			loadRes.Index.Relationships(), reloadRes.Index.Relationships())
	}
}

// TestReload_ReturnsFreshPointer confirms Reload does not return the same
// *KnowledgeIndex pointer as a prior Load — callers must be able to hold
// the old pointer while swapping in the new one.
func TestReload_ReturnsFreshPointer(t *testing.T) {
	dir := t.TempDir()
	writeTestPage(t, dir, "a.md", "# A\n")

	ctx := context.Background()
	first, err := Load(ctx, dir)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	second, err := Reload(ctx, dir)
	if err != nil {
		t.Fatalf("Reload failed: %v", err)
	}

	if first.Index == second.Index {
		t.Error("Reload returned same *KnowledgeIndex pointer; should be a fresh index")
	}

	// Old index must still be queryable after Reload.
	if _, ok := first.Index.Get("a"); !ok {
		t.Error("old index became invalid after Reload")
	}
}

func writeTestPage(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
