package scraper

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// Guess is the result of auto-detection for a page.
type Guess struct {
	ItemSelector    string        `json:"item_selector"`
	TitleSelector   string        `json:"title_selector"`
	LinkSelector    string        `json:"link_selector"`
	ContentSelector string        `json:"content_selector,omitempty"`
	DateSelector    string        `json:"date_selector,omitempty"`
	Confidence      string        `json:"confidence"` // high | medium | low
	SampleCount     int           `json:"sample_count"`
	FeedURL         string        `json:"feed_url,omitempty"` // native feed discovered via <link rel=alternate>
	Reason          string        `json:"reason,omitempty"`
	Preview         []ScrapedItem `json:"preview,omitempty"`
	Candidates      []Candidate   `json:"candidates,omitempty"` // other ranked groups to pick from
}

// Candidate is one repeating link group the user can choose instead.
type Candidate struct {
	ItemSelector  string        `json:"item_selector"`
	TitleSelector string        `json:"title_selector"`
	LinkSelector  string        `json:"link_selector"`
	Count         int           `json:"count"`
	Samples       []ScrapedItem `json:"samples"`
}

var classTokenRE = regexp.MustCompile(`[A-Za-z0-9_-]+`)

// FetchRaw fetches a page body (max 10MB). Shared by detect + scrape paths.
func FetchRaw(pageURL string) (string, error) {
	req, err := http.NewRequest("GET", pageURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("fetch %s: HTTP %d", pageURL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// DetectURL fetches pageURL and guesses selectors + preview.
func DetectURL(pageURL string) (*Guess, error) {
	html, err := FetchRaw(pageURL)
	if err != nil {
		return nil, err
	}
	return DetectHTML(pageURL, html)
}

// DetectHTML is pure (testable) auto-detection.
func DetectHTML(pageURL, html string) (*Guess, error) {
	base, err := url.Parse(pageURL)
	if err != nil {
		return nil, err
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, err
	}

	// 1. Native feed autodiscovery (cheap win — tell user to use it directly).
	feedURL := ""
	doc.Find(`link[rel="alternate"]`).Each(func(_ int, s *goquery.Selection) {
		if feedURL != "" {
			return
		}
		typ, _ := s.Attr("type")
		t := strings.ToLower(typ)
		if strings.Contains(t, "rss") || strings.Contains(t, "atom") || strings.Contains(t, "xml") || strings.Contains(t, "json") {
			if href, ok := s.Attr("href"); ok && href != "" {
				feedURL = absolutize(base, strings.TrimSpace(href))
			}
		}
	})

	// 2. Strip boilerplate so nav/footer links don't win.
	doc.Find("nav, header, footer, aside, script, style, noscript, form, .nav, .navbar, .menu, .sidebar, .pagination").Remove()

	// 3. Anchor clustering by parent signature.
	clusters := map[string]*linkCluster{}

	doc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		href = strings.TrimSpace(href)
		if href == "" || href == "#" || strings.HasPrefix(href, "javascript:") ||
			strings.HasPrefix(href, "mailto:") || strings.HasPrefix(href, "tel:") {
			return
		}
		text := strings.TrimSpace(a.Text())
		if len([]rune(text)) < 10 { // ignore pagination "1", "Next", icon links
			return
		}
		abs := absolutize(base, href)
		u, err := url.Parse(abs)
		if err != nil {
			return
		}
		// skip same-page anchors
		if u.Fragment != "" {
			withoutFrag := *u
			withoutFrag.Fragment = ""
			if withoutFrag.String() == strings.Split(pageURL, "#")[0] {
				return
			}
		}
		parent := containerFor(a)
		if parent.Length() == 0 {
			return
		}
		key := nodeSig(parent) + " < " + nodeSig(parent.Parent())
		sel := parentSelector(parent)
		c, ok := clusters[key]
		if !ok {
			c = &linkCluster{key: key, selector: sel, urls: map[string]struct{}{}, first: parent}
			clusters[key] = c
		}
		c.count++
		c.urls[abs] = struct{}{}
		c.textLenSum += len([]rune(text))
	})

	bestKey := ""
	bestScore := -1.0
	type ranked struct {
		key      string
		selector string
		score    float64
	}
	var order []ranked
	for k, c := range clusters {
		if s, ok := scoreCluster(c); ok {
			order = append(order, ranked{k, c.selector, s})
			if s > bestScore {
				bestScore = s
				bestKey = k
			}
		}
	}
	sort.Slice(order, func(i, j int) bool { return order[i].score > order[j].score })

	cfg := Config{TitleSelector: "a", LinkSelector: "a"}
	var reason string
	var preview []ScrapedItem
	won := false

	// Phase 1 winner: best text-link cluster, quality-gated.
	if bestKey != "" {
		cand := Config{ItemSelector: clusters[bestKey].selector, TitleSelector: "a", LinkSelector: "a"}
		cand = withGuessedSubs(doc, cand)
		if items, ok := validateCfg(pageURL, html, cand); ok {
			cfg, preview = cand, items
			reason = fmt.Sprintf("cluster %q matched %d links", bestKey, clusters[bestKey].count)
			won = true
		}
	}

	// Phase 2: stretched overlay links (whole card is one empty anchor,
	// title lives in a heading). Also feeds the candidate list.
	stretchCands := stretchCandidates(pageURL, html, doc, base)
	if !won {
		for _, sc := range stretchCands {
			cand := withGuessedSubs(doc, Config{ItemSelector: sc.ItemSelector, TitleSelector: sc.TitleSelector, LinkSelector: sc.LinkSelector})
			if items, ok := validateCfg(pageURL, html, cand); ok {
				cfg, preview = cand, items
				reason = fmt.Sprintf("stretched card links %q matched %d cards", sc.ItemSelector, sc.Count)
				won = true
				break
			}
		}
	}

	if !won {
		// 4. Fallback: trial common containers (quality-gated: rejects
		// pagination-like groups whose titles are bare numbers).
		for _, trial := range []string{"article", ".post", ".entry", ".item", "main li", "ul li", "ol li", "h2", "h3"} {
			n := doc.Find(trial).Length()
			if n < 3 || n > 300 {
				continue
			}
			cand := withGuessedSubs(doc, Config{ItemSelector: trial, TitleSelector: "a", LinkSelector: "a"})
			if items, ok := validateCfg(pageURL, html, cand); ok {
				cfg, preview = cand, items
				reason = fmt.Sprintf("fallback container %q matched %d blocks", trial, n)
				won = true
				break
			}
		}
		if !won {
			g := &Guess{Confidence: "low", Reason: "no repeating link group found (min 3 items)", FeedURL: feedURL}
			return g, nil
		}
	}
	g := &Guess{
		ItemSelector:    cfg.ItemSelector,
		TitleSelector:   cfg.TitleSelector,
		LinkSelector:    cfg.LinkSelector,
		ContentSelector: cfg.ContentSelector,
		DateSelector:    cfg.DateSelector,
		SampleCount:     len(preview),
		FeedURL:         feedURL,
		Reason:          reason,
	}
	if len(preview) > 5 {
		preview = preview[:5]
	}
	if preview == nil {
		preview = []ScrapedItem{}
	}
	g.Preview = preview
	switch {
	case len(preview) >= 5:
		g.Confidence = "high"
	case len(preview) >= 3:
		g.Confidence = "medium"
	default:
		g.Confidence = "low"
		g.Reason += " (fewer than 3 preview items)"
	}
	// Runner-up groups so the user can pick a different list in the UI.
	selOrder := make([]string, 0, len(order))
	for _, r := range order {
		selOrder = append(selOrder, r.selector)
	}
	g.Candidates = buildCandidates(pageURL, html, doc, selOrder, cfg.ItemSelector, stretchCands)
	return g, nil
}

// buildCandidates parses the top-ranked selectors (besides the winner) into
// pickable alternatives with sample titles, merged with stretched-card
// groups. Max 4, min 2 parsed items each.
func buildCandidates(pageURL, html string, doc *goquery.Document, selOrder []string, winnerSel string, stretch []Candidate) []Candidate {
	out := []Candidate{}
	seen := map[string]struct{}{winnerSel: {}}
	for _, sel := range selOrder {
		if len(out) >= 4 {
			break
		}
		if sel == "" {
			continue
		}
		if _, dup := seen[sel]; dup {
			continue
		}
		seen[sel] = struct{}{}
		tl := "a"
		if hl := guessHeadingLink(doc, sel); hl != "" {
			tl = hl
		}
		items, err := ParseHTML(pageURL, html, Config{ItemSelector: sel, TitleSelector: tl, LinkSelector: tl})
		if err != nil || len(items) < 2 {
			continue
		}
		samples := items
		if len(samples) > 3 {
			samples = samples[:3]
		}
		out = append(out, Candidate{
			ItemSelector: sel, TitleSelector: tl, LinkSelector: tl,
			Count: len(items), Samples: samples,
		})
	}
	for _, sc := range stretch {
		if len(out) >= 4 {
			break
		}
		if _, dup := seen[sc.ItemSelector]; dup {
			continue
		}
		seen[sc.ItemSelector] = struct{}{}
		out = append(out, sc)
	}
	if out == nil {
		out = []Candidate{}
	}
	return out
}

// withGuessedSubs fills title/link (heading-link preference) plus
// content/date sub-selectors for an item selector.
func withGuessedSubs(doc *goquery.Document, cfg Config) Config {
	if hl := guessHeadingLink(doc, cfg.ItemSelector); hl != "" {
		cfg.TitleSelector = hl
		cfg.LinkSelector = hl
	}
	cfg.ContentSelector, cfg.DateSelector = guessSubSelectors(doc, cfg.ItemSelector)
	return cfg
}

// validateCfg parses with cfg and requires real content: >=3 items, >=3
// with titles of >=10 runes (rejects pagination "1","2","3" groups), and
// >=2 distinct URLs.
func validateCfg(pageURL, html string, cfg Config) ([]ScrapedItem, bool) {
	if strings.TrimSpace(cfg.ItemSelector) == "" {
		return nil, false
	}
	items, err := ParseHTML(pageURL, html, cfg)
	if err != nil || len(items) < 3 {
		return nil, false
	}
	goodTitles, urls := 0, map[string]struct{}{}
	for _, it := range items {
		if len([]rune(strings.TrimSpace(it.Title))) >= 10 {
			goodTitles++
		}
		urls[it.URL] = struct{}{}
	}
	if goodTitles < 3 || len(urls) < 2 {
		return nil, false
	}
	return items, true
}

// stretchCandidates finds stretched-card groups: empty overlay anchors
// (whole card is one link, title in a heading). Returns validated
// candidates ordered by item count.
func stretchCandidates(pageURL, html string, doc *goquery.Document, base *url.URL) []Candidate {
	groups := map[string]*linkCluster{}
	doc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		href = strings.TrimSpace(href)
		if href == "" || href == "#" || strings.HasPrefix(href, "javascript:") ||
			strings.HasPrefix(href, "mailto:") || strings.HasPrefix(href, "tel:") {
			return
		}
		if strings.TrimSpace(a.Text()) != "" || a.Find("img").Length() > 0 {
			return // only text-less overlay links
		}
		abs := absolutize(base, href)
		u, err := url.Parse(abs)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return
		}
		if u.Fragment != "" {
			withoutFrag := *u
			withoutFrag.Fragment = ""
			if withoutFrag.String() == strings.Split(pageURL, "#")[0] {
				return
			}
		}
		var container *goquery.Selection
		var linkSel string
		if a.Find("h1,h2,h3,h4").Length() > 0 {
			// anchor wraps the whole card: the anchor IS the container
			container = a
			linkSel = ""
		} else {
			container = containerFor(a)
			linkSel = selfSelector(a)
		}
		if container.Length() == 0 {
			return
		}
		key := nodeSig(container) + " < " + nodeSig(container.Parent())
		sel := parentSelector(container)
		c, ok := groups[key]
		if !ok {
			c = &linkCluster{key: key, selector: sel, urls: map[string]struct{}{}, first: container, linkSel: linkSel}
			groups[key] = c
		}
		c.count++
		c.urls[abs] = struct{}{}
	})
	type scored struct {
		cfg   Config
		count int
	}
	var ranked []scored
	for _, c := range groups {
		if c.count < 3 || len(c.urls) < 2 {
			continue
		}
		titleSel := headingSelector(c.first)
		if titleSel == "" {
			continue // no headlined title in card: can't name items
		}
		link := c.linkSel
		if link == "" {
			link = "a" // container is the anchor itself
		}
		ranked = append(ranked, scored{Config{ItemSelector: c.selector, TitleSelector: titleSel, LinkSelector: link}, c.count})
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].count > ranked[j].count })
	out := []Candidate{}
	for _, r := range ranked {
		full := withGuessedSubs(doc, r.cfg)
		// keep explicit stretched title/link; only fill content/date
		full.TitleSelector = r.cfg.TitleSelector
		full.LinkSelector = r.cfg.LinkSelector
		items, ok := validateCfg(pageURL, html, full)
		if !ok {
			continue
		}
		samples := items
		if len(samples) > 3 {
			samples = samples[:3]
		}
		out = append(out, Candidate{
			ItemSelector: full.ItemSelector, TitleSelector: full.TitleSelector,
			LinkSelector: full.LinkSelector, Count: len(items), Samples: samples,
		})
	}
	if out == nil {
		out = []Candidate{}
	}
	return out
}

// selfSelector is parentSelector without parent qualification: usable as a
// relative selector for an element known to sit inside the container.
func selfSelector(s *goquery.Selection) string {
	if s == nil || s.Length() == 0 {
		return "a"
	}
	n := s.Get(0)
	t := strings.ToLower(n.Data)
	if t == "" {
		return "a"
	}
	if cls, ok := s.Attr("class"); ok {
		if tok := firstClassToken(cls); tok != "" {
			return t + "." + tok
		}
	}
	return t
}

// headingSelector returns a title selector for the first headlined heading
// (h1-h4 with >=10 runes of text) inside container, preferring
// title/heading-ish class tokens.
func headingSelector(container *goquery.Selection) string {
	if container == nil || container.Length() == 0 {
		return ""
	}
	for _, h := range []string{"h1", "h2", "h3", "h4"} {
		found := ""
		container.Find(h).Each(func(_ int, s *goquery.Selection) {
			if found != "" {
				return
			}
			if len([]rune(strings.TrimSpace(s.Text()))) < 10 {
				return
			}
			tok := ""
			if cls, ok := s.Attr("class"); ok {
				tok = preferTitleToken(cls)
			}
			if tok != "" {
				found = h + "." + tok
			} else {
				found = h
			}
		})
		if found != "" {
			return found
		}
	}
	return ""
}

// preferTitleToken picks a title/heading-ish class token, else first stable.
func preferTitleToken(class string) string {
	fields := strings.Fields(class)
	for _, f := range fields {
		m := classTokenRE.FindString(f)
		if m == "" || len(m) < 2 {
			continue
		}
		l := strings.ToLower(m)
		if l == "active" || l == "current" {
			continue
		}
		if strings.Contains(l, "title") || strings.Contains(l, "heading") || strings.Contains(l, "headline") || strings.Contains(l, "name") {
			return m
		}
	}
	for _, f := range fields {
		m := classTokenRE.FindString(f)
		if m == "" || len(m) < 2 {
			continue
		}
		l := strings.ToLower(m)
		if l == "active" || l == "current" {
			continue
		}
		return m
	}
	return ""
}

// linkCluster is one group of links sharing a parent signature.
type linkCluster struct {
	key        string
	selector   string
	count      int
	urls       map[string]struct{}
	textLenSum int
	first      *goquery.Selection // a representative item container
	linkSel    string             // explicit link selector within container (phase 2: stretched overlay anchor)
}

// scoreCluster rates a link group; ok=false means not list-worthy
// (fewer than 3 links, not distinct, or wrong text shape).
func scoreCluster(c *linkCluster) (float64, bool) {
	distinct := len(c.urls)
	if c.count < 3 || distinct < 2 {
		return 0, false
	}
	avgLen := float64(c.textLenSum) / float64(c.count)
	if avgLen < 10 || avgLen > 400 { // menus vs blobs
		return 0, false
	}
	score := float64(c.count)*2 + float64(distinct)*3
	if avgLen >= 15 && avgLen <= 200 {
		score += 5
	}
	if c.count > 100 { // probably comments / spammy list
		score -= float64(c.count - 100)
	}
	return score, true
}

// containerFor picks the item container for an anchor: the direct parent,
// climbing through inline/heading wrappers (h2 > a is really the article).
func containerFor(a *goquery.Selection) *goquery.Selection {
	p := a.Parent()
	if p.Length() == 0 {
		return p
	}
	n := p.Get(0)
	tag := strings.ToLower(n.Data)
	switch tag {
	case "h1", "h2", "h3", "h4", "h5", "h6", "a", "span", "strong", "em", "small":
		gp := p.Parent()
		if gp.Length() > 0 {
			if gpn := gp.Get(0); gpn != nil {
				gt := strings.ToLower(gpn.Data)
				if gt != "" && gt != "body" && gt != "main" && gt != "html" {
					return gp
				}
			}
		}
	}
	return p
}

// nodeSig returns a stable grouping key like "article.post" or "li".
func nodeSig(s *goquery.Selection) string {
	if s == nil || s.Length() == 0 {
		return "?"
	}
	n := s.Get(0)
	tag := strings.ToLower(n.Data)
	if tag == "" {
		return "?"
	}
	if cls, ok := s.Attr("class"); ok {
		if tok := firstClassToken(cls); tok != "" {
			return tag + "." + tok
		}
	}
	return tag
}

// parentSelector builds a usable CSS selector for the item container.
func parentSelector(s *goquery.Selection) string {
	if s == nil || s.Length() == 0 {
		return ""
	}
	n := s.Get(0)
	tag := strings.ToLower(n.Data)
	sel := tag
	if cls, ok := s.Attr("class"); ok {
		if tok := firstClassToken(cls); tok != "" {
			sel = tag + "." + tok
		}
	}
	// qualify with parent when generic (li/div without class)
	if (tag == "li" || tag == "div" || tag == "section") && !strings.Contains(sel, ".") {
		p := s.Parent()
		if p.Length() > 0 {
			psel := nodeSig(p)
			if psel != "?" {
				return psel + " > " + sel
			}
		}
	}
	return sel
}

func firstClassToken(class string) string {
	for _, f := range strings.Fields(class) {
		if m := classTokenRE.FindString(f); m != "" && len(m) >= 2 {
			// skip utility-looking tokens
			l := strings.ToLower(m)
			if l == "active" || l == "current" {
				continue
			}
			return m
		}
	}
	return ""
}

// guessHeadingLink returns e.g. "h4 a" if item containers link their title
// from a heading; "" means the default first-anchor is fine.
func guessHeadingLink(doc *goquery.Document, itemSel string) string {
	first := doc.Find(itemSel).First()
	if first.Length() == 0 {
		return ""
	}
	for _, h := range []string{"h1 a", "h2 a", "h3 a", "h4 a"} {
		a := first.Find(h).First()
		if a.Length() == 0 {
			continue
		}
		if len([]rune(strings.TrimSpace(a.Text()))) >= 10 {
			return h
		}
	}
	return ""
}

// guessSubSelectors inspects the first item container for content/date hints.
func guessSubSelectors(doc *goquery.Document, itemSel string) (content, date string) {
	first := doc.Find(itemSel).First()
	if first.Length() == 0 {
		return "", ""
	}
	for _, cand := range []string{"p", ".summary", ".excerpt", ".content", ".description"} {
		if first.Find(cand).Length() > 0 {
			content = cand
			break
		}
	}
	for _, cand := range []string{"time", ".date", ".published", ".meta", ".byline"} {
		if first.Find(cand).Length() > 0 {
			date = cand
			break
		}
	}
	_ = time.Now
	return content, date
}
