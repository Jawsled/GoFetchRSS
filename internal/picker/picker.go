package picker

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// MirrorPage transforms fetched target HTML into an embeddable preview for
// the in-app picker iframe: fixes relative URLs via <base>, strips scripts /
// CSP metas / inline event handlers (foreign code never runs, picking never
// navigates). The iframe is additionally sandboxed, and all picker logic
// runs in our first-party page — nothing injected runs in page context, so
// content blockers have nothing to act on. Pure function, unit-tested.
func MirrorPage(pageURL, html string) (string, error) {
	if _, err := url.ParseRequestURI(pageURL); err != nil {
		return "", fmt.Errorf("bad url: %w", err)
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return "", err
	}

	// 1. <base> so relative links/images/stylesheets resolve to the target.
	if doc.Find("head base[href]").Length() == 0 {
		if doc.Find("head").Length() == 0 {
			doc.Find("html").PrependHtml("<head></head>")
		}
		base := strings.ReplaceAll(pageURL, `"`, "%22")
		doc.Find("head").First().PrependHtml(`<base href="` + base + `">`)
	}

	// 2. Strip scripts + CSP metas: foreign JS must not run in our origin,
	// and CSP could block our injected picker script.
	doc.Find("script").Remove()
	doc.Find("meta[http-equiv]").Each(func(_ int, s *goquery.Selection) {
		if eq, ok := s.Attr("http-equiv"); ok {
			l := strings.ToLower(strings.TrimSpace(eq))
			if l == "content-security-policy" || l == "content-security-policy-report-only" {
				s.Remove()
			}
		}
	})

	// 3. Strip inline event handlers (onclick="location.href=..." would
	// navigate away from the mirror despite click interception).
	doc.Find("*").Each(func(_ int, s *goquery.Selection) {
		n := s.Get(0)
		kept := n.Attr[:0]
		for _, a := range n.Attr {
			if strings.HasPrefix(strings.ToLower(a.Key), "on") {
				continue
			}
			kept = append(kept, a)
		}
		n.Attr = kept
	})

	if doc.Find("body").Length() == 0 {
		doc.Find("html").AppendHtml("<body></body>")
	}
	// 4. Highlight-ring style for elements the parent picker marks.
	// Picker UI itself lives in our page (not injected), so blockers ignore it.
	doc.Find("head").First().AppendHtml(
		`<style>.rspk-hl{outline:2px solid #2563eb !important;outline-offset:1px;cursor:crosshair}</style>`)

	out, err := doc.Html()
	if err != nil {
		return "", err
	}
	return out, nil
}
