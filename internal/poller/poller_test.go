package poller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"gofetchrss/internal/scraper"
	"gofetchrss/internal/store"
)

const mockWeibo = `{"ok":1,"data":{"cards":[
	{"card_type":9,"mblog":{"id":"M1","bid":"M1ab","text":"Mock post number one here","created_at":"2026-09-10 10:00:00"}},
	{"card_type":9,"mblog":{"id":"M2","bid":"M2cd","text":"Mock post number two here","created_at":"2026-09-11 10:00:00"}}
]}}`

func TestRefreshWeiboKind(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(mockWeibo))
	}))
	defer srv.Close()

	// Rewrite the weibo API host to the mock by using json kind with the
	// same field map the weibo preset uses.
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	site := &store.Site{ID: "w1", Name: "Mock Weibo", URL: srv.URL + "/u/1",
		SourceKind: "json", APIURL: srv.URL + "/api", ItemsPath: "data.cards",
		ItemRoot: "mblog", ContentPath: "text", LinkTemplate: srv.URL + "/detail/{bid}",
		DatePath: "created_at", PollMinutes: 60, Enabled: true}
	if err := st.CreateSite(site); err != nil {
		t.Fatal(err)
	}
	if err := RefreshSite(st, "w1"); err != nil {
		t.Fatal(err)
	}
	items, err := st.LatestItems("w1", 10)
	if err != nil || len(items) != 2 {
		t.Fatalf("want 2 items, got %v err=%v", len(items), err)
	}
	if items[0].URL != srv.URL+"/detail/M2cd" {
		t.Fatalf("bad link/newest-first: %+v", items[0])
	}
	got, err := st.GetSite("w1")
	if err != nil || got.NeedsReview {
		t.Fatalf("should not need review: %+v err=%v", got, err)
	}
}

func TestRefreshLoginWallFlagsReview(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<title>Sina Visitor System</title>"))
	}))
	defer srv.Close()

	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	site := &store.Site{ID: "w2", Name: "Walled", URL: srv.URL,
		SourceKind: "json", APIURL: srv.URL + "/api", ItemsPath: "data.cards",
		PollMinutes: 60, Enabled: true}
	if err := st.CreateSite(site); err != nil {
		t.Fatal(err)
	}
	err = RefreshSite(st, "w2")
	if !errors.Is(err, scraper.ErrLoginRequired) {
		t.Fatalf("want ErrLoginRequired, got %v", err)
	}
	got, err := st.GetSite("w2")
	if err != nil {
		t.Fatal(err)
	}
	if !got.NeedsReview {
		t.Fatal("login wall must flag needs_review")
	}
	if got.LastError == "" {
		t.Fatal("last_error must explain the login requirement")
	}
}
