package api

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gofetchrss/internal/feeds"
	"gofetchrss/internal/picker"
	"gofetchrss/internal/poller"
	"gofetchrss/internal/scraper"
	"gofetchrss/internal/store"
)

type Server struct {
	Store *store.Store
	Base  string // e.g. http://localhost:9284 for absolute feed URLs in OPML
	WebFS embed.FS
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s *Server) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /api/sites", s.handleListSites)
	mux.HandleFunc("POST /api/sites", s.handleCreateSite)
	mux.HandleFunc("PUT /api/sites/{id}", s.handleUpdateSite)
	mux.HandleFunc("DELETE /api/sites/{id}", s.handleDeleteSite)
	mux.HandleFunc("POST /api/sites/{id}/refresh", s.handleRefreshSite)
	mux.HandleFunc("POST /api/preview", s.handlePreview)
	mux.HandleFunc("POST /api/detect", s.handleDetect)
	mux.HandleFunc("GET /pick", s.handlePick)
	mux.HandleFunc("GET /feeds/", s.handleFeed)
	mux.HandleFunc("GET /export.opml", s.handleOPML)
	mux.HandleFunc("GET /api/export.opml", s.handleOPML)

	// embedded static UI
	sub, err := fs.Sub(s.WebFS, "web")
	if err == nil {
		mux.Handle("GET /", http.FileServer(http.FS(sub)))
	}
}

func writeJSON(w http.ResponseWriter, v any, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleListSites(w http.ResponseWriter, r *http.Request) {
	sites, err := s.Store.ListSites()
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()}, 500)
		return
	}
	writeJSON(w, sites, 200)
}

type siteInput struct {
	Name            string `json:"name"`
	URL             string `json:"url"`
	ItemSelector    string `json:"item_selector"`
	TitleSelector   string `json:"title_selector"`
	LinkSelector    string `json:"link_selector"`
	ContentSelector string `json:"content_selector"`
	DateSelector    string `json:"date_selector"`
	PollMinutes     int    `json:"poll_minutes"`
	Enabled         *bool  `json:"enabled"`
	ExtractionMode  string `json:"extraction_mode"` // auto | manual
	SourceKind      string `json:"source_kind"`     // html | json | weibo
	APIURL          string `json:"api_url"`
	ItemsPath       string `json:"items_path"`
	ItemRoot        string `json:"item_root"`
	TitlePath       string `json:"title_path"`
	ContentPath     string `json:"content_path"`
	LinkPath        string `json:"link_path"`
	LinkTemplate    string `json:"link_template"`
	DatePath        string `json:"date_path"`
	DateFormat      string `json:"date_format"`
	Headers         string `json:"headers"` // JSON object, e.g. {"Cookie":"SUB=..."}
	WeiboUID        string `json:"weibo_uid"`
}

// Kind normalizes the source kind, defaulting to html.
func (in *siteInput) Kind() string {
	switch strings.ToLower(strings.TrimSpace(in.SourceKind)) {
	case "json":
		return "json"
	case "weibo":
		return "weibo"
	default:
		return "html"
	}
}

func validURL(u string) bool {
	return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")
}

func (in *siteInput) validate() string {
	if strings.TrimSpace(in.Name) == "" {
		return "name is required"
	}
	if in.PollMinutes < 5 {
		in.PollMinutes = 60
	}
	if in.PollMinutes > 60*24*7 {
		return "poll_minutes too large (max 1 week)"
	}
	if _, err := scraper.ParseHeaders(in.Headers); err != nil {
		return err.Error()
	}
	switch in.Kind() {
	case "json":
		if strings.TrimSpace(in.APIURL) == "" {
			return "api_url is required for JSON sources"
		}
		if !validURL(in.APIURL) {
			return "api_url must start with http:// or https://"
		}
		if strings.TrimSpace(in.ItemsPath) == "" {
			return "items_path is required for JSON sources (e.g. data.cards)"
		}
		return ""
	case "weibo":
		uid := strings.TrimSpace(in.WeiboUID)
		if uid == "" {
			return "weibo_uid is required (digits from m.weibo.cn/u/<uid>)"
		}
		for _, r := range uid {
			if r < '0' || r > '9' {
				return "weibo_uid must be numeric"
			}
		}
		return ""
	default: // html
		if strings.TrimSpace(in.URL) == "" {
			return "url is required"
		}
		if !validURL(in.URL) {
			return "url must start with http:// or https://"
		}
		if in.Mode() == "manual" && strings.TrimSpace(in.ItemSelector) == "" {
			return "item_selector is required in manual mode"
		}
		return ""
	}
}

// Mode normalizes the extraction mode, defaulting to auto.
func (in *siteInput) Mode() string {
	m := strings.ToLower(strings.TrimSpace(in.ExtractionMode))
	if m == "manual" {
		return "manual"
	}
	return "auto"
}

func (s *Server) handleCreateSite(w http.ResponseWriter, r *http.Request) {
	var in siteInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeJSON(w, map[string]string{"error": "invalid JSON: " + err.Error()}, 400)
		return
	}
	if msg := in.validate(); msg != "" {
		writeJSON(w, map[string]string{"error": msg}, 400)
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	kind := in.Kind()
	mode := in.Mode()
	discoveredFeed := ""
	pageURL := strings.TrimSpace(in.URL)
	apiURL := strings.TrimSpace(in.APIURL)
	weiboUID := strings.TrimSpace(in.WeiboUID)
	if kind == "weibo" {
		pageURL = scraper.WeiboProfileURL(weiboUID)
		apiURL = scraper.WeiboAPIURL(weiboUID)
		mode = "manual" // no selector detection for API sources
	} else if kind == "json" {
		mode = "manual"
	}
	// Auto mode with no selectors: run detection server-side (HTML only).
	if kind == "html" && mode == "auto" && strings.TrimSpace(in.ItemSelector) == "" {
		guess, err := scraper.DetectURL(pageURL)
		if err != nil {
			writeJSON(w, map[string]string{"error": "auto-detect failed: " + err.Error()}, 502)
			return
		}
		if guess.SampleCount < 3 || guess.Confidence == "low" {
			writeJSON(w, map[string]any{"error": "auto-detect found fewer than 3 items — try Manual mode", "guess": guess}, 422)
			return
		}
		in.ItemSelector = guess.ItemSelector
		in.TitleSelector = guess.TitleSelector
		in.LinkSelector = guess.LinkSelector
		in.ContentSelector = guess.ContentSelector
		in.DateSelector = guess.DateSelector
		discoveredFeed = guess.FeedURL
	}
	st := &store.Site{
		ID: newID(), Name: strings.TrimSpace(in.Name), URL: pageURL,
		ItemSelector: strings.TrimSpace(in.ItemSelector), TitleSelector: strings.TrimSpace(in.TitleSelector),
		LinkSelector: strings.TrimSpace(in.LinkSelector), ContentSelector: strings.TrimSpace(in.ContentSelector),
		DateSelector: strings.TrimSpace(in.DateSelector), PollMinutes: in.PollMinutes, Enabled: enabled,
		ExtractionMode: mode, DiscoveredFeed: discoveredFeed, SourceKind: kind,
		APIURL: apiURL, ItemsPath: strings.TrimSpace(in.ItemsPath), ItemRoot: strings.TrimSpace(in.ItemRoot),
		TitlePath: strings.TrimSpace(in.TitlePath), ContentPath: strings.TrimSpace(in.ContentPath),
		LinkPath: strings.TrimSpace(in.LinkPath), LinkTemplate: strings.TrimSpace(in.LinkTemplate),
		DatePath: strings.TrimSpace(in.DatePath), DateFormat: strings.TrimSpace(in.DateFormat),
		Headers: strings.TrimSpace(in.Headers), WeiboUID: weiboUID,
	}
	if err := s.Store.CreateSite(st); err != nil {
		writeJSON(w, map[string]string{"error": err.Error()}, 500)
		return
	}
	// best-effort immediate first fetch (don't block failure)
	go func() { _ = poller.RefreshSite(s.Store, st.ID) }()
	writeJSON(w, st, 201)
}

func (s *Server) handleUpdateSite(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	existing, err := s.Store.GetSite(id)
	if err != nil {
		writeJSON(w, map[string]string{"error": "site not found"}, 404)
		return
	}
	var in siteInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeJSON(w, map[string]string{"error": "invalid JSON: " + err.Error()}, 400)
		return
	}
	if msg := in.validate(); msg != "" {
		writeJSON(w, map[string]string{"error": msg}, 400)
		return
	}
	existing.Name = strings.TrimSpace(in.Name)
	kind := in.Kind()
	if kind == "weibo" {
		existing.URL = scraper.WeiboProfileURL(strings.TrimSpace(in.WeiboUID))
		existing.APIURL = scraper.WeiboAPIURL(strings.TrimSpace(in.WeiboUID))
		existing.WeiboUID = strings.TrimSpace(in.WeiboUID)
	} else {
		existing.URL = strings.TrimSpace(in.URL)
		existing.APIURL = strings.TrimSpace(in.APIURL)
		existing.WeiboUID = ""
	}
	existing.SourceKind = kind
	existing.ItemsPath = strings.TrimSpace(in.ItemsPath)
	existing.ItemRoot = strings.TrimSpace(in.ItemRoot)
	existing.TitlePath = strings.TrimSpace(in.TitlePath)
	existing.ContentPath = strings.TrimSpace(in.ContentPath)
	existing.LinkPath = strings.TrimSpace(in.LinkPath)
	existing.LinkTemplate = strings.TrimSpace(in.LinkTemplate)
	existing.DatePath = strings.TrimSpace(in.DatePath)
	existing.DateFormat = strings.TrimSpace(in.DateFormat)
	existing.Headers = strings.TrimSpace(in.Headers)
	existing.ItemSelector = strings.TrimSpace(in.ItemSelector)
	existing.TitleSelector = strings.TrimSpace(in.TitleSelector)
	existing.LinkSelector = strings.TrimSpace(in.LinkSelector)
	existing.ContentSelector = strings.TrimSpace(in.ContentSelector)
	existing.DateSelector = strings.TrimSpace(in.DateSelector)
	existing.PollMinutes = in.PollMinutes
	if in.Enabled != nil {
		existing.Enabled = *in.Enabled
	}
	// Editing selectors clears the needs-review flag so a fixed config recovers.
	existing.EmptyRuns = 0
	existing.NeedsReview = false
	if m := in.Mode(); m != "" {
		existing.ExtractionMode = m
	}
	if err := s.Store.UpdateSite(existing); err != nil {
		writeJSON(w, map[string]string{"error": err.Error()}, 500)
		return
	}
	writeJSON(w, existing, 200)
}

func (s *Server) handleDeleteSite(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Store.DeleteSite(id); err != nil {
		writeJSON(w, map[string]string{"error": err.Error()}, 500)
		return
	}
	w.WriteHeader(204)
}

func (s *Server) handleRefreshSite(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.Store.GetSite(id); err != nil {
		writeJSON(w, map[string]string{"error": "site not found"}, 404)
		return
	}
	go func() { _ = poller.RefreshSite(s.Store, id) }()
	writeJSON(w, map[string]string{"status": "refresh started"}, 202)
}

func (s *Server) handleDetect(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, map[string]string{"error": "invalid JSON"}, 400)
		return
	}
	pageURL := strings.TrimSpace(body.URL)
	if pageURL == "" || (!strings.HasPrefix(pageURL, "http://") && !strings.HasPrefix(pageURL, "https://")) {
		writeJSON(w, map[string]string{"error": "url must start with http:// or https://"}, 400)
		return
	}
	guess, err := scraper.DetectURL(pageURL)
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()}, 502)
		return
	}
	writeJSON(w, guess, 200)
}

// handlePick serves the target page as a bare mirror for the in-app picker
// iframe (same origin, sandboxed). See internal/picker.
func (s *Server) handlePick(w http.ResponseWriter, r *http.Request) {
	pageURL := strings.TrimSpace(r.URL.Query().Get("url"))
	if pageURL == "" || (!strings.HasPrefix(pageURL, "http://") && !strings.HasPrefix(pageURL, "https://")) {
		http.Error(w, "usage: /pick?url=https://example.com/page", 400)
		return
	}
	raw, err := scraper.FetchRaw(pageURL)
	if err != nil {
		http.Error(w, "fetch failed: "+err.Error(), 502)
		return
	}
	out, err := picker.MirrorPage(pageURL, raw)
	if err != nil {
		http.Error(w, "mirror failed: "+err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(out))
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	var in siteInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeJSON(w, map[string]string{"error": "invalid JSON"}, 400)
		return
	}
	var items []scraper.ScrapedItem
	var err error
	switch in.Kind() {
	case "json", "weibo":
		uid := strings.TrimSpace(in.WeiboUID)
		apiURL := strings.TrimSpace(in.APIURL)
		if in.Kind() == "weibo" {
			if uid == "" {
				writeJSON(w, map[string]string{"error": "weibo_uid required"}, 400)
				return
			}
			apiURL = scraper.WeiboAPIURL(uid)
		}
		if apiURL == "" || strings.TrimSpace(in.ItemsPath) == "" && in.Kind() == "json" {
			if in.Kind() == "json" {
				writeJSON(w, map[string]string{"error": "api_url and items_path required"}, 400)
				return
			}
		}
		tmp := &store.Site{SourceKind: in.Kind(), URL: strings.TrimSpace(in.URL),
			APIURL: apiURL, ItemsPath: strings.TrimSpace(in.ItemsPath), ItemRoot: strings.TrimSpace(in.ItemRoot),
			TitlePath: strings.TrimSpace(in.TitlePath), ContentPath: strings.TrimSpace(in.ContentPath),
			LinkPath: strings.TrimSpace(in.LinkPath), LinkTemplate: strings.TrimSpace(in.LinkTemplate),
			DatePath: strings.TrimSpace(in.DatePath), DateFormat: strings.TrimSpace(in.DateFormat),
			Headers: strings.TrimSpace(in.Headers), WeiboUID: uid}
		items, err = poller.ScrapeSite(tmp)
	default: // html
		if strings.TrimSpace(in.URL) == "" || strings.TrimSpace(in.ItemSelector) == "" {
			writeJSON(w, map[string]string{"error": "url and item_selector required"}, 400)
			return
		}
		items, err = poller.Preview(in.URL, scraper.Config{
			SiteURL: in.URL, ItemSelector: in.ItemSelector, TitleSelector: in.TitleSelector,
			LinkSelector: in.LinkSelector, ContentSelector: in.ContentSelector, DateSelector: in.DateSelector,
		})
	}
	if err != nil {
		code := 502
		if errors.Is(err, scraper.ErrLoginRequired) {
			err = errors.New("login required and guest session failed — retry shortly, or paste your own session Cookie in headers")
			code = 401
		}
		writeJSON(w, map[string]string{"error": err.Error()}, code)
		return
	}
	if items == nil {
		items = []scraper.ScrapedItem{}
	}
	if len(items) > 5 {
		items = items[:5]
	}
	writeJSON(w, items, 200)
}

func feedLimit(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || n <= 0 || n > 200 {
		return 50
	}
	return n
}

func (s *Server) handleFeed(w http.ResponseWriter, r *http.Request) {
	// path: /feeds/<id>.xml | .atom | .json
	rest := strings.TrimPrefix(r.URL.Path, "/feeds/")
	var id, ext string
	if i := strings.LastIndex(rest, "."); i > 0 {
		id, ext = rest[:i], rest[i+1:]
	} else {
		http.NotFound(w, r)
		return
	}
	// guard against slashes: id must be single segment
	if strings.Contains(id, "/") || id == "" {
		http.NotFound(w, r)
		return
	}
	// stash id for sub-handlers that read PathValue("id")
	r.SetPathValue("id", id)
	switch ext {
	case "xml":
		s.handleFeedXML(w, r)
	case "atom":
		s.handleFeedAtom(w, r)
	case "json":
		s.handleFeedJSON(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleFeedXML(w http.ResponseWriter, r *http.Request) {
	site, err := s.Store.GetSite(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	items, _ := s.Store.LatestItems(site.ID, feedLimit(r))
	out, err := feeds.BuildRSS(*site, items)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	w.Write([]byte(out))
}

func (s *Server) handleFeedAtom(w http.ResponseWriter, r *http.Request) {
	site, err := s.Store.GetSite(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	items, _ := s.Store.LatestItems(site.ID, feedLimit(r))
	selfURL := s.Base + "/feeds/" + site.ID + ".atom"
	out, err := feeds.BuildAtom(*site, items, selfURL)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/atom+xml; charset=utf-8")
	w.Write([]byte(out))
}

func (s *Server) handleFeedJSON(w http.ResponseWriter, r *http.Request) {
	site, err := s.Store.GetSite(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	items, _ := s.Store.LatestItems(site.ID, feedLimit(r))
	out := feeds.BuildJSON(*site, items, s.Base+"/feeds/"+site.ID+".json")
	w.Header().Set("Content-Type", "application/feed+json; charset=utf-8")
	w.Write([]byte(out))
}

func (s *Server) handleOPML(w http.ResponseWriter, r *http.Request) {
	sites, err := s.Store.ListSites()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	base := s.Base
	if base == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	_ = time.Now
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="gofetchrss.opml"`)
	w.Write([]byte(feeds.BuildOPML(sites, base)))
}
