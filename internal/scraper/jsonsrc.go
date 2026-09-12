package scraper

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// ErrLoginRequired signals a login wall / expired session cookie: the server
// returned a challenge page (e.g. Sina Visitor System) instead of content.
// Callers should flag the site for review rather than emptying its feed.
var ErrLoginRequired = errors.New("login required: server returned an auth challenge (cookie expired or missing?)")

// loginWallMarkers match known auth-challenge shells.
var loginWallMarkers = []string{
	"Sina Visitor System",
	"visitor/mini_original.js",
	"wbBotDetector",
}

// isLoginWall reports whether a fetched body is an auth challenge.
func isLoginWall(body string) bool {
	for _, m := range loginWallMarkers {
		if strings.Contains(body, m) {
			return true
		}
	}
	return false
}

// FetchRawWithHeaders fetches a URL with extra request headers (Cookie etc.),
// returning body and content type. Login walls yield ErrLoginRequired.
func FetchRawWithHeaders(pageURL string, headers map[string]string) (body, contentType string, err error) {
	req, err := http.NewRequest("GET", pageURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	for k, v := range headers {
		if strings.EqualFold(k, "User-Agent") && v == "" {
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("fetch %s: HTTP %d", pageURL, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return "", "", err
	}
	if isLoginWall(string(raw)) {
		return "", "", fmt.Errorf("%w (at %s)", ErrLoginRequired, pageURL)
	}
	return string(raw), resp.Header.Get("Content-Type"), nil
}

// ParseHeaders parses the site Headers column (JSON object) into a map.
func ParseHeaders(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, fmt.Errorf("headers must be a JSON object like {\"Cookie\": \"SUB=...\"}: %w", err)
	}
	return m, nil
}

// JSONSpec maps a JSON API response to feed items. Paths are dot-separated
// keys (numeric segments index arrays), e.g. ItemsPath "data.cards".
type JSONSpec struct {
	ItemsPath    string
	ItemRoot     string // optional sub-object per item, e.g. "mblog"; items lacking it are skipped
	TitlePath    string // empty = derive from content text
	ContentPath  string
	LinkPath     string
	LinkTemplate string // e.g. "https://m.weibo.cn/detail/{bid}"; {name} resolved against the item
	DatePath     string
	DateFormat   string // Go layout; empty = auto-detect
}

// FetchJSON GETs apiURL with headers and maps the response via spec.
func FetchJSON(apiURL string, headers map[string]string, spec JSONSpec) ([]ScrapedItem, error) {
	if strings.TrimSpace(spec.ItemsPath) == "" {
		return nil, fmt.Errorf("items_path is required")
	}
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json, text/javascript, */*")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch %s: HTTP %d", apiURL, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, err
	}
	if isLoginWall(string(raw)) {
		return nil, fmt.Errorf("%w (at %s)", ErrLoginRequired, apiURL)
	}
	return ParseJSON(apiURL, string(raw), spec)
}

// ParseJSON is the pure (testable) JSON-to-items mapping.
func ParseJSON(pageURL, body string, spec JSONSpec) ([]ScrapedItem, error) {
	var doc any
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return nil, fmt.Errorf("invalid JSON response: %w", err)
	}
	if m, ok := doc.(map[string]any); ok {
		// Weibo-style login redirect: {"ok":-100,"url":"https://passport..."}.
		// Matched narrowly (-100 only) so generic APIs using ok:0/true pass.
		if okFlag, present := m["ok"]; present {
			if f, isNum := okFlag.(float64); isNum && f == -100 {
				return nil, fmt.Errorf("%w (api returned ok=-100)", ErrLoginRequired)
			}
		}
	}
	rawItems, ok := dig(doc, spec.ItemsPath).([]any)
	if !ok {
		return nil, fmt.Errorf("items_path %q matched no array", spec.ItemsPath)
	}
	base, _ := url.Parse(pageURL)
	out := []ScrapedItem{}
	for _, ri := range rawItems {
		item := ri
		if strings.TrimSpace(spec.ItemRoot) != "" {
			sub := dig(item, spec.ItemRoot)
			if sub == nil {
				continue // e.g. non-post cards without mblog
			}
			item = sub
		}
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		content := getStr(m, spec.ContentPath)
		title := getStr(m, spec.TitlePath)
		if title == "" {
			title = truncateRunes(textify(content), 140)
		}
		title = strings.TrimSpace(title)
		link := ""
		if strings.TrimSpace(spec.LinkTemplate) != "" {
			link = expandTemplate(spec.LinkTemplate, m)
		} else {
			link = getStr(m, spec.LinkPath)
		}
		if base != nil && link != "" {
			link = absolutize(base, link)
		}
		var published time.Time
		if ds := getStr(m, spec.DatePath); ds != "" {
			published = parseJSONDate(ds, spec.DateFormat)
		}
		if published.IsZero() {
			published = time.Now().UTC()
		}
		if title == "" && link == "" {
			continue
		}
		if title == "" {
			title = link
		}
		if link == "" {
			link = pageURL + "#" + fmt.Sprintf("%d", len(out))
		}
		out = append(out, ScrapedItem{Title: title, URL: link, ContentHTML: content, PublishedAt: published.UTC()})
		if len(out) >= 200 {
			break
		}
	}
	return out, nil
}

// dig walks dot-separated keys (numeric segments index arrays).
func dig(v any, path string) any {
	cur := v
	for _, seg := range strings.Split(strings.TrimSpace(path), ".") {
		if seg == "" {
			continue
		}
		switch c := cur.(type) {
		case map[string]any:
			cur = c[seg]
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(c) {
				return nil
			}
			cur = c[i]
		default:
			return nil
		}
		if cur == nil {
			return nil
		}
	}
	return cur
}

func getStr(item map[string]any, path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	switch v := dig(item, path).(type) {
	case nil:
		return ""
	case string:
		return v
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	default:
		return ""
	}
}

var templateVarRE = regexp.MustCompile(`\{([A-Za-z0-9_.]+)\}`)

// expandTemplate resolves {field} placeholders against the item.
func expandTemplate(tmpl string, item map[string]any) string {
	return templateVarRE.ReplaceAllStringFunc(tmpl, func(m string) string {
		name := templateVarRE.FindStringSubmatch(m)[1]
		return getStr(item, name)
	})
}

// parseJSONDate parses API dates: explicit Go layout, known layouts
// (incl. Weibo ctime), or unix seconds/millis.
func parseJSONDate(s, format string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	if format != "" {
		if t, err := time.Parse(format, s); err == nil {
			return t.UTC()
		}
	}
	for _, l := range []string{
		time.RFC3339, "2006-01-02T15:04:05Z07:00", "2006-01-02T15:04:05.000Z07:00",
		time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822,
		"Mon Jan 2 15:04:05 -0700 2006", // Weibo ctime
		"2006-01-02 15:04:05", "2006-01-02",
	} {
		if t, err := time.Parse(l, s); err == nil {
			return t.UTC()
		}
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		switch {
		case n > 1e14:
			return time.UnixMilli(n).UTC()
		case n > 1e12:
			return time.UnixMilli(n).UTC()
		case n > 1e9:
			return time.Unix(n, 0).UTC()
		}
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil && f > 1e9 {
		return time.Unix(int64(f), 0).UTC()
	}
	return time.Time{}
}

// textify strips HTML to plain text for title derivation.
func textify(html string) string {
	if strings.TrimSpace(html) == "" {
		return ""
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return strings.TrimSpace(regexp.MustCompile(`<[^>]*>`).ReplaceAllString(html, " "))
	}
	t := strings.TrimSpace(doc.Text())
	return regexp.MustCompile(`\s+`).ReplaceAllString(t, " ")
}

func truncateRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

// ---- Weibo (m.weibo.cn) preset ----

const weiboMobileUA = "Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Mobile Safari/537.36"

// WeiboAPIURL builds the public container API URL for a user UID.
// Pattern matches RSSHub's weibo.user route (containerid 107603{uid}).
func WeiboAPIURL(uid string) string {
	uid = strings.TrimSpace(uid)
	return "https://m.weibo.cn/api/container/getIndex?type=uid&value=" + uid + "&containerid=107603" + uid
}

// WeiboProfileURL is the shareable page URL for a UID.
func WeiboProfileURL(uid string) string {
	return "https://m.weibo.cn/u/" + strings.TrimSpace(uid)
}

// WeiboHeaders returns base headers for the Weibo API merged with user
// headers (where an optional personal session Cookie lives).
func WeiboHeaders(extra map[string]string) map[string]string {
	h := map[string]string{
		"User-Agent":       weiboMobileUA,
		"Referer":          "https://m.weibo.cn/",
		"Accept":           "application/json, text/plain, */*",
		"X-Requested-With": "XMLHttpRequest",
		"MWeibo-Pwa":       "1",
	}
	for k, v := range extra {
		h[k] = v
	}
	return h
}

// WeiboSpec maps getIndex cards to items. Title derives from post text;
// link uses the short bid (m.weibo.cn/detail/{bid}).
func WeiboSpec() JSONSpec {
	return JSONSpec{
		ItemsPath:    "data.cards",
		ItemRoot:     "mblog",
		ContentPath:  "text",
		LinkTemplate: "https://m.weibo.cn/detail/{bid}",
		DatePath:     "created_at",
	}
}

// FetchWeibo fetches one page of a user's posts. No login needed: unless the
// caller supplies their own session Cookie, a guest visitor session is
// minted automatically (cached ~20 min, refreshed on expiry/failure).
func FetchWeibo(uid string, headers map[string]string) ([]ScrapedItem, error) {
	uid = strings.TrimSpace(uid)
	if uid == "" {
		return nil, fmt.Errorf("weibo uid is required")
	}
	if !regexp.MustCompile(`^\d+$`).MatchString(uid) {
		return nil, fmt.Errorf("weibo uid must be numeric (see the digits in m.weibo.cn/u/<uid>)")
	}
	reqHeaders, err := weiboRequestHeaders(headers)
	if err != nil {
		return nil, err
	}
	items, err := FetchJSON(WeiboAPIURL(uid), reqHeaders, WeiboSpec())
	if err != nil && errors.Is(err, ErrLoginRequired) {
		// Guest session may have expired server-side: drop it so the next
		// fetch handshakes fresh (self-healing), then report for review.
		InvalidateVisitorSession()
	}
	return items, err
}

// weiboRequestHeaders builds API headers: caller Cookie wins, otherwise the
// guest visitor session (Cookie assembled from the jar + X-XSRF-TOKEN).
func weiboRequestHeaders(user map[string]string) (map[string]string, error) {
	base := WeiboHeaders(user)
	if strings.TrimSpace(user["Cookie"]) != "" || strings.TrimSpace(user["cookie"]) != "" {
		return base, nil
	}
	guest, err := visitorSession()
	if err != nil {
		return nil, fmt.Errorf("weibo visitor session: %w", err)
	}
	var parts []string
	for _, name := range []string{"SUB", "SUBP", "_T_WM", "MLOGIN", "WEIBOCN_FROM", "mweibo_short_token", "SRF", "SRT"} {
		if v := guest[name]; v != "" {
			parts = append(parts, name+"="+v)
		}
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("weibo visitor session yielded no usable cookies")
	}
	base["Cookie"] = strings.Join(parts, "; ")
	if xsrf := guest["XSRF-TOKEN"]; xsrf != "" {
		base["X-XSRF-TOKEN"] = xsrf
	}
	return base, nil
}
