package scraper

import (
	"strings"
	"testing"
)

func TestDetectBlogList(t *testing.T) {
	html := `<html><head></head><body><nav><a href="/home">Home page link here</a><a href="/about">About us page here</a></nav>
	<main>
	<article class="post"><h2><a href="/p1">First blog post title here</a></h2><p class="summary">Summary one text</p><time>2026-09-01</time></article>
	<article class="post"><h2><a href="/p2">Second blog post title here</a></h2><p class="summary">Summary two text</p><time>2026-09-02</time></article>
	<article class="post"><h2><a href="/p3">Third blog post title here</a></h2><p class="summary">Summary three text</p><time>2026-09-03</time></article>
	<article class="post"><h2><a href="/p4">Fourth blog post title here</a></h2></article>
	</main></body></html>`
	g, err := DetectHTML("https://example.com/blog", html)
	if err != nil {
		t.Fatal(err)
	}
	if g.Confidence == "low" || g.SampleCount < 3 {
		t.Fatalf("want >=3 items with med/high confidence, got %+v", g)
	}
	if !strings.Contains(g.ItemSelector, "article") {
		t.Fatalf("want article-ish selector, got %q", g.ItemSelector)
	}
	// preview must round-trip through ParseHTML
	items, err := ParseHTML("https://example.com/blog", html, Config{
		ItemSelector: g.ItemSelector, TitleSelector: g.TitleSelector, LinkSelector: g.LinkSelector,
	})
	if err != nil || len(items) < 3 {
		t.Fatalf("guessed selectors don't reproduce: %v n=%d", err, len(items))
	}
	if items[0].URL != "https://example.com/p1" {
		t.Fatalf("bad absolutized URL: %q", items[0].URL)
	}
}

func TestDetectNavHeavy(t *testing.T) {
	// 10 nav links + 3 real posts: nav must not win.
	var nav strings.Builder
	for i := 0; i < 10; i++ {
		nav.WriteString(`<a href="/nav` + string(rune('0'+i)) + `">Navigation section number ` + string(rune('0'+i)) + ` here</a>`)
	}
	html := `<html><body><nav>` + nav.String() + `</nav><main><div class="news-list">` +
		`<div class="news-item"><a href="/n1">Breaking news story number one here</a></div>` +
		`<div class="news-item"><a href="/n2">Breaking news story number two here</a></div>` +
		`<div class="news-item"><a href="/n3">Breaking news story number three here</a></div>` +
		`</div></main></body></html>`
	g, err := DetectHTML("https://example.com/", html)
	if err != nil {
		t.Fatal(err)
	}
	if g.SampleCount < 3 {
		t.Fatalf("want 3 news items, got %+v", g)
	}
	if !strings.Contains(g.ItemSelector, "news-item") {
		t.Fatalf("want .news-item selector, got %q", g.ItemSelector)
	}
}

func TestDetectFeedDiscovery(t *testing.T) {
	html := `<html><head><link rel="alternate" type="application/rss+xml" href="/feed.xml"></head><body>
	<div class="x"><a href="/a1">Alpha beta gamma delta epsilon zeta</a></div>
	<div class="x"><a href="/a2">Alpha beta gamma delta epsilon zeta 2</a></div>
	<div class="x"><a href="/a3">Alpha beta gamma delta epsilon zeta 3</a></div>
	</body></html>`
	g, err := DetectHTML("https://example.com/blog/", html)
	if err != nil {
		t.Fatal(err)
	}
	if g.FeedURL != "https://example.com/feed.xml" {
		t.Fatalf("want discovered feed, got %+v", g)
	}
}

func TestDetectTooFew(t *testing.T) {
	html := `<html><body><p>Just one lonely link: <a href="/only">Only article on this page here</a></p></body></html>`
	g, err := DetectHTML("https://example.com/", html)
	if err != nil {
		t.Fatal(err)
	}
	if g.Confidence != "low" || g.SampleCount >= 3 {
		t.Fatalf("want low-confidence <3 result, got %+v", g)
	}
}

func TestDetectStretchedCards(t *testing.T) {
	// MUI-style cards: title in bare h3, whole card covered by one empty
	// overlay anchor. No usable text links exist on the page.
	var cards strings.Builder
	for _, p := range [][2]string{
		{"State of AI 2026: Your Definitive Guide to the Space", "/report/state-of-ai-2026"},
		{"State of Web 2026: Acquisition and Engagement Trends", "/report/state-of-web-2026"},
		{"State of Gaming 2026: Mobile, PC and Console Report", "/report/state-of-gaming-2026"},
		{"Japan Game Market Insights 2026 Full Report Here", "/report/japan-gaming-2026"},
	} {
		cards.WriteString(`<div class="MuiCard-root"><div class="MuiCard-content"><span>REPORT</span>` +
			`<h3 class="MuiTypography-root MuiTypography-h3">` + p[0] + `</h3>` +
			`<p>Some excerpt text for the card body here.</p>` +
			`<a class="mui-cardLink" href="` + p[1] + `"></a></div></div>`)
	}
	html := `<html><body><nav><a href="/home">Home page link here</a></nav><main><h2>Latest Resources</h2>` +
		cards.String() + `<ul class="pagination"><li><a href="/?page=1">1</a></li><li><a href="/?page=2">2</a></li><li><a href="/?page=3">3</a></li><li><a href="/?page=4">4</a></li></ul></main></body></html>`
	g, err := DetectHTML("https://example.com/resources", html)
	if err != nil {
		t.Fatal(err)
	}
	if g.SampleCount < 3 {
		t.Fatalf("want >=3 card items, got %+v", g)
	}
	if !strings.Contains(g.ItemSelector, "MuiCard") {
		t.Fatalf("want card container selector, got %q", g.ItemSelector)
	}
	for _, it := range g.Preview {
		if !strings.Contains(it.Title, "2026") {
			t.Fatalf("want real card titles, got %+v", it)
		}
		if !strings.Contains(it.URL, "/report/") {
			t.Fatalf("want report URLs, got %+v", it)
		}
	}
}

func TestDetectCandidatesTwoGroups(t *testing.T) {
	html := `<html><body><main>
	<article class="post"><h2><a href="/p1">First proper article title here</a></h2></article>
	<article class="post"><h2><a href="/p2">Second proper article title here</a></h2></article>
	<article class="post"><h2><a href="/p3">Third proper article title here</a></h2></article>
	<article class="post"><h2><a href="/p4">Fourth proper article title here</a></h2></article>
	</main><div class="trending">
	<div class="trend"><a href="/t1">Trending sidebar story number one</a></div>
	<div class="trend"><a href="/t2">Trending sidebar story number two</a></div>
	<div class="trend"><a href="/t3">Trending sidebar story number three</a></div>
	</div></body></html>`
	g, err := DetectHTML("https://example.com/", html)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(g.ItemSelector, "article") {
		t.Fatalf("winner should be articles, got %q", g.ItemSelector)
	}
	if len(g.Candidates) == 0 {
		t.Fatal("want at least one runner-up candidate")
	}
	found := false
	for _, c := range g.Candidates {
		if strings.Contains(c.ItemSelector, "trend") {
			found = true
			if len(c.Samples) < 2 {
				t.Fatalf("candidate needs samples, got %+v", c)
			}
			if !strings.Contains(c.Samples[0].Title, "Trending") {
				t.Fatalf("bad candidate sample: %+v", c.Samples[0])
			}
		}
		if c.ItemSelector == g.ItemSelector {
			t.Fatalf("winner listed as its own candidate: %q", c.ItemSelector)
		}
	}
	if !found {
		t.Fatalf("want trending group among candidates: %+v", g.Candidates)
	}
}
