package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	site := &Site{ID: "s1", Name: "Blog", URL: "https://example.com", ItemSelector: "article", TitleSelector: "h2", LinkSelector: "a", PollMinutes: 60, Enabled: true}
	if err := st.CreateSite(site); err != nil {
		t.Fatal(err)
	}
	due, err := st.DueSites(time.Now().UTC())
	if err != nil || len(due) != 1 {
		t.Fatalf("want 1 due site, got %v err=%v", len(due), err)
	}
	n, err := st.AddItems("s1", []Item{
		{Title: "A", URL: "https://example.com/a", PublishedAt: time.Now().UTC(), DedupHash: DedupHash("https://example.com/a", "A")},
		{Title: "A dup", URL: "https://example.com/a", PublishedAt: time.Now().UTC(), DedupHash: DedupHash("https://example.com/a", "A")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("dedup failed: inserted=%d want 1", n)
	}
	items, err := st.LatestItems("s1", 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("want 1 item, got %v err=%v", len(items), err)
	}
	if err := st.SetCheckResult("s1", time.Now().UTC(), ""); err != nil {
		t.Fatal(err)
	}
	due, _ = st.DueSites(time.Now().UTC())
	if len(due) != 0 {
		t.Fatal("site should not be due right after check")
	}
}
