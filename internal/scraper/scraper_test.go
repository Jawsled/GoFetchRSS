package scraper

import (
	"strings"
	"testing"
)

func TestParseHTMLBasic(t *testing.T) {
	html := `<html><body>
		<article class="post"><h2><a href="/a1">First post</a></h2><p class="summary">Hello <a href="/x">x</a></p></article>
		<article class="post"><h2><a href="https://other.com/a2">Second</a></h2></article>
		<article class="post"><h2>No link here</h2></article>
	</body></html>`
	items, err := ParseHTML("https://example.com/news", html, Config{
		ItemSelector: "article.post", TitleSelector: "h2", LinkSelector: "a", ContentSelector: ".summary",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("want 3 items, got %d", len(items))
	}
	if items[0].Title != "First post" {
		t.Fatalf("bad title: %q", items[0].Title)
	}
	if items[0].URL != "https://example.com/a1" {
		t.Fatalf("relative URL not absolutized: %q", items[0].URL)
	}
	if items[1].URL != "https://other.com/a2" {
		t.Fatalf("absolute URL changed: %q", items[1].URL)
	}
	if !strings.Contains(items[0].ContentHTML, `href="https://example.com/x"`) {
		t.Fatalf("content attrs not absolutized: %q", items[0].ContentHTML)
	}
}

func TestParseHTMLRequiresItemSelector(t *testing.T) {
	if _, err := ParseHTML("https://example.com", "<p>hi</p>", Config{}); err == nil {
		t.Fatal("expected error for empty item selector")
	}
}
