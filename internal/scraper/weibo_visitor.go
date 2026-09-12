package scraper

import (
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/publicsuffix"
)

// Visitor session management for m.weibo.cn.
//
// Weibo's API rejects anonymous calls (ok:-100 login redirect), but the
// visitor handshake mints a guest session over plain HTTP — no login, no
// JS (same trick as RSSHub's visitor-cookie fetcher, minus the Puppeteer):
//  1. GET  https://m.weibo.cn/                         (WEIBOCN_FROM)
//  2. GET  https://m.weibo.cn/api/config               (XSRF-TOKEN, MLOGIN, _T_WM, ...)
//  3. POST https://visitor.passport.weibo.cn/visitor/genvisitor2 (SUB, SUBP)
//
// Sessions are cached process-wide (shared by all Weibo sites) and refreshed
// on expiry or after an auth failure, with a cooldown to avoid hammering.

var (
	weiboHomeURL       = "https://m.weibo.cn/"
	weiboConfigURL     = "https://m.weibo.cn/api/config"
	weiboGenVisitorURL = "https://visitor.passport.weibo.cn/visitor/genvisitor2"

	visitorMu          sync.Mutex
	visitorCookies     map[string]string
	visitorExpiry      time.Time
	visitorLastAttempt time.Time
	visitorTTL         = 20 * time.Minute
	visitorMinInterval = 60 * time.Second // cooldown between handshakes
)

// visitorSession returns cached guest cookies or performs a fresh handshake.
func visitorSession() (map[string]string, error) {
	visitorMu.Lock()
	defer visitorMu.Unlock()
	if time.Now().Before(visitorExpiry) && len(visitorCookies) > 0 {
		out := make(map[string]string, len(visitorCookies))
		for k, v := range visitorCookies {
			out[k] = v
		}
		return out, nil
	}
	if time.Since(visitorLastAttempt) < visitorMinInterval {
		return nil, fmt.Errorf("visitor session cooling down, retry shortly")
	}
	visitorLastAttempt = time.Now()
	cookies, err := handshakeVisitor()
	if err != nil {
		return nil, err
	}
	visitorCookies = cookies
	visitorExpiry = time.Now().Add(visitorTTL)
	out := make(map[string]string, len(cookies))
	for k, v := range cookies {
		out[k] = v
	}
	return out, nil
}

// InvalidateVisitorSession drops the cached guest session so the next fetch
// performs a fresh handshake (called after an auth failure — self-healing).
func InvalidateVisitorSession() {
	visitorMu.Lock()
	defer visitorMu.Unlock()
	visitorCookies = nil
	visitorExpiry = time.Time{}
}

// handshakeVisitor runs the 3-step guest handshake with a cookie jar and
// returns the cookies relevant to m.weibo.cn API calls.
func handshakeVisitor() (map[string]string, error) {
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		return nil, err
	}
	client := &http.Client{Jar: jar, Timeout: 20 * time.Second}

	get := func(url, referer string) error {
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", weiboMobileUA)
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json,*/*")
		if referer != "" {
			req.Header.Set("Referer", referer)
		}
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("visitor handshake GET %s: %w", url, err)
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("visitor handshake GET %s: HTTP %d", url, resp.StatusCode)
		}
		return nil
	}

	if err := get(weiboHomeURL, ""); err != nil {
		return nil, err
	}
	if err := get(weiboConfigURL, weiboHomeURL); err != nil {
		return nil, err
	}

	form := strings.NewReader("cb=gen_callback")
	req, err := http.NewRequest("POST", weiboGenVisitorURL, form)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", weiboMobileUA)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", weiboHomeURL)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("visitor handshake genvisitor2: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !strings.Contains(string(raw), `"retcode":20000000`) {
		return nil, fmt.Errorf("visitor handshake genvisitor2 failed: HTTP %d", resp.StatusCode)
	}

	out := map[string]string{}
	u, _ := url.Parse(weiboHomeURL)
	for _, c := range jar.Cookies(u) {
		out[c.Name] = c.Value
	}
	if out["SUB"] == "" {
		return nil, fmt.Errorf("visitor handshake did not yield a SUB cookie")
	}
	return out, nil
}
