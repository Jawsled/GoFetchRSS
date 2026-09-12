package scraper

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func mockWeiboHandshake(t *testing.T, hits *int) (home, config, gen string) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		*hits++
		http.SetCookie(w, &http.Cookie{Name: "WEIBOCN_FROM", Value: "x", Path: "/"})
		w.Write([]byte("<html></html>"))
	})
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		*hits++
		http.SetCookie(w, &http.Cookie{Name: "XSRF-TOKEN", Value: "tok123", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "_T_WM", Value: "wm", Path: "/"})
		w.Write([]byte("{}"))
	})
	mux.HandleFunc("/genvisitor2", func(w http.ResponseWriter, r *http.Request) {
		*hits++
		http.SetCookie(w, &http.Cookie{Name: "SUB", Value: "guest-sub", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "SUBP", Value: "guest-subp", Path: "/"})
		w.Write([]byte(`gen_callback({"retcode":20000000})`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL + "/", srv.URL + "/api/config", srv.URL + "/genvisitor2"
}

func TestVisitorHandshake(t *testing.T) {
	var hits int
	home, config, gen := mockWeiboHandshake(t, &hits)
	oldHome, oldConfig, oldGen := weiboHomeURL, weiboConfigURL, weiboGenVisitorURL
	oldTTL, oldMin := visitorTTL, visitorMinInterval
	weiboHomeURL, weiboConfigURL, weiboGenVisitorURL = home, config, gen
	visitorTTL, visitorMinInterval = time.Minute, 0
	t.Cleanup(func() {
		weiboHomeURL, weiboConfigURL, weiboGenVisitorURL = oldHome, oldConfig, oldGen
		visitorTTL, visitorMinInterval = oldTTL, oldMin
		InvalidateVisitorSession()
	})
	InvalidateVisitorSession()

	c, err := visitorSession()
	if err != nil {
		t.Fatal(err)
	}
	if c["SUB"] != "guest-sub" || c["XSRF-TOKEN"] != "tok123" {
		t.Fatalf("bad cookies: %v", c)
	}
	if hits != 3 {
		t.Fatalf("want 3 handshake requests, got %d", hits)
	}
	// second call uses cache: no new requests
	if _, err := visitorSession(); err != nil {
		t.Fatal(err)
	}
	if hits != 3 {
		t.Fatalf("cache not used, hits=%d", hits)
	}
	// headers builder prefers caller Cookie, else guest session
	h, err := weiboRequestHeaders(map[string]string{"Cookie": "SUB=mine"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h["Cookie"], "SUB=mine") {
		t.Fatalf("caller cookie must win: %v", h)
	}
	h2, err := weiboRequestHeaders(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h2["Cookie"], "SUB=guest-sub") || h2["X-XSRF-TOKEN"] != "tok123" {
		t.Fatalf("guest headers wrong: %v", h2)
	}
}

func TestVisitorHandshakeFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	t.Cleanup(srv.Close)
	oldHome, oldGen := weiboHomeURL, weiboGenVisitorURL
	weiboHomeURL, weiboGenVisitorURL = srv.URL+"/", srv.URL+"/genvisitor2"
	oldTTL, oldMin := visitorTTL, visitorMinInterval
	visitorTTL, visitorMinInterval = time.Minute, 0
	t.Cleanup(func() {
		weiboHomeURL, weiboGenVisitorURL = oldHome, oldGen
		visitorTTL, visitorMinInterval = oldTTL, oldMin
		InvalidateVisitorSession()
	})
	InvalidateVisitorSession()
	if _, err := visitorSession(); err == nil {
		t.Fatal("expected handshake error")
	}
}
