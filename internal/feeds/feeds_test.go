package feeds

import (
	"strings"
	"testing"
	"time"

	"gofetchrss/internal/store"
)

func sampleSite() store.Site {
	return store.Site{ID: "abc123", Name: "Example News", URL: "https://example.com/news"}
}

func sampleItems() []store.Item {
	return []store.Item{
		{Title: "First", URL: "https://example.com/a1", ContentHTML: "<p>Hi</p>", PublishedAt: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)},
		{Title: "Second", URL: "https://example.com/a2", PublishedAt: time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)},
	}
}

func TestBuildRSS(t *testing.T) {
	out, err := BuildRSS(sampleSite(), sampleItems())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<rss", "<channel>", "First", "https://example.com/a1", "<pubDate>"} {
		if !strings.Contains(out, want) {
			t.Fatalf("RSS missing %q\n%s", want, out)
		}
	}
}

func TestBuildAtom(t *testing.T) {
	out, err := BuildAtom(sampleSite(), sampleItems(), "http://localhost:9284/feeds/abc123.atom")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`<feed xmlns=`, "<entry>", "First", `rel="self"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("Atom missing %q\n%s", want, out)
		}
	}
}

func TestBuildJSON(t *testing.T) {
	out := BuildJSON(sampleSite(), sampleItems(), "http://localhost:9284/feeds/abc123.json")
	for _, want := range []string{"jsonfeed.org/version/1.1", `"title":"First"`, `"url":"https://example.com/a1"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("JSON feed missing %q\n%s", want, out)
		}
	}
}

func TestBuildOPML(t *testing.T) {
	out := BuildOPML([]store.Site{sampleSite()}, "http://localhost:9284")
	if !strings.Contains(out, "<opml") || !strings.Contains(out, "/feeds/abc123.xml") {
		t.Fatalf("bad OPML:\n%s", out)
	}
}
