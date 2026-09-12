package scraper

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const weiboFixture = `{"ok":1,"data":{"cards":[
	{"card_type":9,"mblog":{"id":"Q123","mid":"1","bid":"Q123ab","text":"Hello world <span class=\"url-icon\">x</span> <a href=\"/n/x\">link</a>","created_at":"Tue Sep 10 12:34:56 +0800 2026","source":"iPhone","user":{"screen_name":"Tester"}}},
	{"card_type":11,"card_group":[{"card_type":9,"mblog":{"id":"NOPE","text":"nested, ignored (item_root is one level)"}}]},
	{"card_type":9,"mblog":{"id":"Q124","bid":"Q124cd","text":"Second post with emoji <img class=\"face\" alt=\"[happy]\" /> inside","created_at":"2026-09-11T08:00:00+08:00"}},
	{"card_type":9,"mblog":{"id":"Q125","bid":"Q125ef","text":"","created_at":"not-a-date"}}
]}}`

func TestParseWeiboFixture(t *testing.T) {
	items, err := ParseJSON("https://m.weibo.cn/api/container/getIndex?type=uid", weiboFixture, WeiboSpec())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("want 3 items (card_group + empty skipped), got %d: %+v", len(items), items)
	}
	if !strings.HasPrefix(items[0].Title, "Hello world") {
		t.Fatalf("title not stripped: %q", items[0].Title)
	}
	if strings.Contains(items[0].Title, "<") {
		t.Fatalf("title still has HTML: %q", items[0].Title)
	}
	if items[0].URL != "https://m.weibo.cn/detail/Q123ab" {
		t.Fatalf("bad link template: %q", items[0].URL)
	}
	want := time.Date(2026, 9, 10, 12, 34, 56, 0, time.FixedZone("CST", 8*3600)).UTC()
	if !items[0].PublishedAt.Equal(want) {
		t.Fatalf("bad ctime parse: %v want %v", items[0].PublishedAt, want)
	}
	if items[1].URL != "https://m.weibo.cn/detail/Q124cd" {
		t.Fatalf("bad link 2: %q", items[1].URL)
	}
	// empty text + bad date still yields an item keyed by URL (discovery time)
	if items[2].URL != "https://m.weibo.cn/detail/Q125ef" {
		t.Fatalf("bad link 3: %q", items[2].URL)
	}
	if items[2].PublishedAt.IsZero() {
		t.Fatal("expected discovery-time fallback")
	}
}

func TestParseJSONGenericSpec(t *testing.T) {
	body := `{"data":{"posts":[{"heading":"Alpha release notes","body":"<p>Shipped</p>","slug":"alpha","ts":1757419200}]}}`
	spec := JSONSpec{ItemsPath: "data.posts", TitlePath: "heading", ContentPath: "body",
		LinkTemplate: "https://example.com/blog/{slug}", DatePath: "ts"}
	items, err := ParseJSON("https://example.com/api", body, spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("want 1, got %d", len(items))
	}
	if items[0].Title != "Alpha release notes" || items[0].URL != "https://example.com/blog/alpha" {
		t.Fatalf("bad mapping: %+v", items[0])
	}
	if items[0].PublishedAt.Year() != 2025 && items[0].PublishedAt.Year() != 2026 {
		t.Fatalf("bad unix date: %v", items[0].PublishedAt)
	}
}

func TestParseJSONLoginRedirect(t *testing.T) {
	_, err := ParseJSON("https://m.weibo.cn/api/x", `{"ok":-100,"url":"https://passport.weibo.com/sso/signin"}`, WeiboSpec())
	if !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("want ErrLoginRequired, got %v", err)
	}
	// generic APIs with other ok conventions must not trip the wall detector
	items, err := ParseJSON("https://x", `{"ok":0,"data":{"cards":[]}}`, JSONSpec{ItemsPath: "data.cards"})
	if errors.Is(err, ErrLoginRequired) {
		t.Fatalf("ok:0 must not be a login wall: %v", err)
	}
	if err != nil || len(items) != 0 {
		t.Fatalf("want empty items, nil error: %+v %v", items, err)
	}
}

func TestParseJSONBadPaths(t *testing.T) {
	if _, err := ParseJSON("https://x", `{"a":1}`, JSONSpec{ItemsPath: "data.cards"}); err == nil {
		t.Fatal("expected error for missing array")
	}
	if _, err := ParseJSON("https://x", `not json`, JSONSpec{ItemsPath: "x"}); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestFetchJSONLoginWall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<title>Sina Visitor System</title><script src=\"/js/visitor/mini_original.js\"></script>"))
	}))
	defer srv.Close()
	_, err := FetchJSON(srv.URL, nil, JSONSpec{ItemsPath: "data.cards"})
	if !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("want ErrLoginRequired, got %v", err)
	}
}

func TestFetchJSONRoundTrip(t *testing.T) {
	var gotCookie string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"cards":[{"mblog":{"id":"1","bid":"b1","text":"Hi there, testing cookies","created_at":"2026-09-01 10:00:00"}}]}}`))
	}))
	defer srv.Close()
	items, err := FetchJSON(srv.URL, map[string]string{"Cookie": "SUB=abc123"}, JSONSpec{
		ItemsPath: "data.cards", ItemRoot: "mblog", ContentPath: "text",
		LinkTemplate: "https://m.weibo.cn/detail/{bid}", DatePath: "created_at",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotCookie != "SUB=abc123" {
		t.Fatalf("cookie not forwarded: %q", gotCookie)
	}
	if len(items) != 1 || items[0].URL != "https://m.weibo.cn/detail/b1" {
		t.Fatalf("bad items: %+v", items)
	}
}

func TestWeiboAPIURL(t *testing.T) {
	u := WeiboAPIURL("6048569942")
	if !strings.Contains(u, "containerid=1076036048569942") {
		t.Fatalf("bad api url: %s", u)
	}
	if _, err := FetchWeibo("abc", nil); err == nil {
		t.Fatal("expected uid validation error")
	}
}

func TestParseHeaders(t *testing.T) {
	m, err := ParseHeaders(`{"Cookie":"SUB=x","X-Foo":"bar"}`)
	if err != nil || m["Cookie"] != "SUB=x" {
		t.Fatalf("bad parse: %v %v", m, err)
	}
	if _, err := ParseHeaders(`not json`); err == nil {
		t.Fatal("expected error")
	}
	if m, err := ParseHeaders(``); err != nil || m != nil {
		t.Fatal("empty should be nil,nil")
	}
}
