package picker

import (
	"strings"
	"testing"
)

func TestMirrorPage(t *testing.T) {
	html := `<html><head><meta http-equiv="Content-Security-Policy" content="script-src 'self'"><link rel="alternate" type="application/rss+xml" href="/feed"></head>` +
		`<body><script>alert(1)</script><div class="post" onclick="location.href='/x'"><h2><a href="/a1">A fairly long article title here</a></h2></div></body></html>`
	out, err := MirrorPage("https://example.com/news", html)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "alert(1)") {
		t.Fatal("scripts must be stripped")
	}
	if strings.Contains(strings.ToLower(out), "content-security-policy") {
		t.Fatal("CSP meta must be stripped")
	}
	if strings.Contains(out, "onclick") {
		t.Fatal("inline handlers must be stripped")
	}
	if !strings.Contains(out, `<base href="https://example.com/news"`) {
		t.Fatalf("base missing:\n%s", out)
	}
	if strings.Contains(out, "<script") {
		t.Fatalf("no scripts may survive in the mirror:\n%s", out)
	}
	// relative link preserved (base fixes resolution, no rewriting needed)
	if !strings.Contains(out, `href="/a1"`) {
		t.Fatalf("content links must be preserved:\n%s", out)
	}
}

func TestMirrorPageKeepsExistingBase(t *testing.T) {
	html := `<html><head><base href="https://cdn.example.com/"></head><body><p>hi</p></body></html>`
	out, err := MirrorPage("https://example.com/", html)
	if err != nil {
		t.Fatal(err)
	}
	if c := strings.Count(out, "<base"); c != 1 {
		t.Fatalf("must not duplicate base, got %d:\n%s", c, out)
	}
	if !strings.Contains(out, "rspk-hl") {
		t.Fatalf("highlight style missing:\n%s", out)
	}
}

func TestMirrorPageBadURL(t *testing.T) {
	if _, err := MirrorPage("::bad::", "<p>hi</p>"); err == nil {
		t.Fatal("expected error for bad url")
	}
}
