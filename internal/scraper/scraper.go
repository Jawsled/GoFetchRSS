package scraper

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36 GoFetchRSS/0.1"

var httpClient = &http.Client{Timeout: 20 * time.Second}

type ScrapedItem struct {
	Title       string
	URL         string
	ContentHTML string
	PublishedAt time.Time
}

type Config struct {
	SiteURL         string
	ItemSelector    string
	TitleSelector   string
	LinkSelector    string
	ContentSelector string
	DateSelector    string
}

// ScrapeURL fetches pageURL and extracts items using cfg selectors.
func ScrapeURL(pageURL string, cfg Config) ([]ScrapedItem, error) {
	if strings.TrimSpace(cfg.ItemSelector) == "" {
		return nil, fmt.Errorf("item_selector is required")
	}
	req, err := http.NewRequest("GET", pageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch %s: HTTP %d", pageURL, resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if ct != "" && !strings.Contains(strings.ToLower(ct), "html") && !strings.Contains(strings.ToLower(ct), "xml") && !strings.Contains(strings.ToLower(ct), "text") {
		// still try to parse; many sites omit header
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // 10MB cap
	if err != nil {
		return nil, err
	}
	return ParseHTML(pageURL, string(body), cfg)
}

// ParseHTML is pure (testable) extraction from html string.
func ParseHTML(pageURL, html string, cfg Config) ([]ScrapedItem, error) {
	if strings.TrimSpace(cfg.ItemSelector) == "" {
		return nil, fmt.Errorf("item_selector is required")
	}
	base, err := url.Parse(pageURL)
	if err != nil {
		return nil, err
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, err
	}
	out := []ScrapedItem{}
	doc.Find(cfg.ItemSelector).Each(func(_ int, sel *goquery.Selection) {
		title := extractText(sel, cfg.TitleSelector)
		link := extractLink(sel, cfg.LinkSelector, base)
		content := extractHTML(sel, cfg.ContentSelector, base)
		var published time.Time
		if strings.TrimSpace(cfg.DateSelector) != "" {
			raw := extractText(sel, cfg.DateSelector)
			if t := parseDate(raw); !t.IsZero() {
				published = t
			}
			if published.IsZero() {
				if dt, ok := sel.Find(cfg.DateSelector).First().Attr("datetime"); ok {
					if t := parseDate(dt); !t.IsZero() {
						published = t
					}
				}
			}
		}
		if published.IsZero() {
			published = time.Now().UTC()
		}
		if title == "" && link == "" {
			return // skip empty nodes
		}
		if title == "" {
			title = link
		}
		if link == "" {
			link = pageURL + "#" + fmt.Sprintf("%d", len(out))
		}
		out = append(out, ScrapedItem{Title: strings.TrimSpace(title), URL: link, ContentHTML: content, PublishedAt: published.UTC()})
		if len(out) >= 200 {
			return
		}
	})
	return out, nil
}

func extractText(scope *goquery.Selection, selector string) string {
	if strings.TrimSpace(selector) == "" {
		return strings.TrimSpace(scope.Find("a").First().Text())
	}
	el := scope.Find(selector).First()
	if el.Length() == 0 {
		// maybe the item itself matches
		if scope.Is(selector) {
			return strings.TrimSpace(scope.Text())
		}
		return ""
	}
	return strings.TrimSpace(el.Text())
}

func extractLink(scope *goquery.Selection, selector string, base *url.URL) string {
	var el *goquery.Selection
	if strings.TrimSpace(selector) == "" {
		el = scope.Find("a[href]").First()
		if el.Length() == 0 && scope.Is("a[href]") {
			el = scope
		}
	} else {
		el = scope.Find(selector).First()
		if el.Length() == 0 && scope.Is(selector) {
			el = scope
		}
	}
	if el == nil || el.Length() == 0 {
		return ""
	}
	href := ""
	if hrefAttr, ok := el.Attr("href"); ok {
		href = strings.TrimSpace(hrefAttr)
	} else {
		// selector may point at text node inside <a>
		a := el.Closest("a[href]")
		if a.Length() > 0 {
			href, _ = a.Attr("href")
		} else {
			a = el.Find("a[href]").First()
			if a.Length() > 0 {
				href, _ = a.Attr("href")
			}
		}
	}
	if href == "" || strings.HasPrefix(href, "javascript:") {
		return ""
	}
	return absolutize(base, href)
}

func extractHTML(scope *goquery.Selection, selector string, base *url.URL) string {
	if strings.TrimSpace(selector) == "" {
		return ""
	}
	el := scope.Find(selector).First()
	if el.Length() == 0 {
		return ""
	}
	html, err := el.Html()
	if err != nil {
		return ""
	}
	return absolutizeAttrs(html, base)
}

func absolutize(base *url.URL, href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	u, err := url.Parse(href)
	if err != nil {
		return href
	}
	return base.ResolveReference(u).String()
}

// absolutizeAttrs rewrites relative src/href in an HTML fragment to absolute.
func absolutizeAttrs(fragment string, base *url.URL) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader("<div>" + fragment + "</div>"))
	if err != nil {
		return fragment
	}
	doc.Find("[href]").Each(func(_ int, s *goquery.Selection) {
		if v, ok := s.Attr("href"); ok && v != "" && !strings.HasPrefix(v, "#") && !strings.HasPrefix(v, "mailto:") {
			s.SetAttr("href", absolutize(base, v))
		}
	})
	doc.Find("[src]").Each(func(_ int, s *goquery.Selection) {
		if v, ok := s.Attr("src"); ok && v != "" {
			s.SetAttr("src", absolutize(base, v))
		}
	})
	html, err := doc.Find("div").First().Html()
	if err != nil {
		return fragment
	}
	return html
}

var dateLayouts = []string{
	time.RFC3339, time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822,
	"2006-01-02T15:04:05Z07:00", "2006-01-02 15:04:05", "2006-01-02",
	"02 Jan 2006 15:04:05 MST", "02 Jan 2006", "Jan 2, 2006", "January 2, 2006",
	"2 Jan 2006", "2006/01/02", "01/02/2006",
}

func parseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
