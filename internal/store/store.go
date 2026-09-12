package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const maxItemsPerSite = 200

type Site struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	URL             string     `json:"url"`
	ItemSelector    string     `json:"item_selector"`
	TitleSelector   string     `json:"title_selector"`
	LinkSelector    string     `json:"link_selector"`
	ContentSelector string     `json:"content_selector,omitempty"`
	DateSelector    string     `json:"date_selector,omitempty"`
	PollMinutes     int        `json:"poll_minutes"`
	Enabled         bool       `json:"enabled"`
	LastChecked     *time.Time `json:"last_checked,omitempty"`
	LastError       string     `json:"last_error,omitempty"`
	ItemCount       int        `json:"item_count,omitempty"`
	CreatedAt       time.Time  `json:"created_at,omitempty"`
	ExtractionMode  string     `json:"extraction_mode,omitempty"` // auto | manual
	DiscoveredFeed  string     `json:"discovered_feed,omitempty"` // native feed found via autodiscovery
	EmptyRuns       int        `json:"empty_runs,omitempty"`
	NeedsReview     bool       `json:"needs_review,omitempty"`
	SourceKind      string     `json:"source_kind,omitempty"` // html | json | weibo (default html)
	APIURL          string     `json:"api_url,omitempty"`     // JSON API endpoint (json kind; auto-built for weibo)
	ItemsPath       string     `json:"items_path,omitempty"`  // dot path to item array, e.g. data.cards
	ItemRoot        string     `json:"item_root,omitempty"`   // optional sub-path per item, e.g. mblog
	TitlePath       string     `json:"title_path,omitempty"`
	ContentPath     string     `json:"content_path,omitempty"`
	LinkPath        string     `json:"link_path,omitempty"`
	LinkTemplate    string     `json:"link_template,omitempty"` // e.g. https://m.weibo.cn/detail/{bid}
	DatePath        string     `json:"date_path,omitempty"`
	DateFormat      string     `json:"date_format,omitempty"` // Go layout; empty = auto-detect
	IDPath          string     `json:"id_path,omitempty"`
	Headers         string     `json:"headers,omitempty"` // JSON object of extra request headers (Cookie etc.)
	WeiboUID        string     `json:"weibo_uid,omitempty"`
}

type Item struct {
	ID          int64     `json:"id"`
	SiteID      string    `json:"site_id"`
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	ContentHTML string    `json:"content_html,omitempty"`
	PublishedAt time.Time `json:"published_at"`
	DedupHash   string    `json:"-"`
	CreatedAt   time.Time `json:"created_at"`
}

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS sites (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		url TEXT NOT NULL,
		item_selector TEXT NOT NULL,
		title_selector TEXT NOT NULL DEFAULT '',
		link_selector TEXT NOT NULL DEFAULT '',
		content_selector TEXT NOT NULL DEFAULT '',
		date_selector TEXT NOT NULL DEFAULT '',
		poll_minutes INTEGER NOT NULL DEFAULT 60,
		enabled INTEGER NOT NULL DEFAULT 1,
		last_checked TEXT,
		last_error TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL
	)`)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`CREATE TABLE IF NOT EXISTS items (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		site_id TEXT NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
		title TEXT NOT NULL,
		url TEXT NOT NULL,
		content_html TEXT NOT NULL DEFAULT '',
		published_at TEXT NOT NULL,
		dedup_hash TEXT NOT NULL,
		created_at TEXT NOT NULL,
		UNIQUE(site_id, dedup_hash)
	)`)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_items_site_time ON items(site_id, published_at DESC)`)
	if err != nil {
		return err
	}
	for _, col := range []string{
		`ALTER TABLE sites ADD COLUMN extraction_mode TEXT NOT NULL DEFAULT 'manual'`,
		`ALTER TABLE sites ADD COLUMN discovered_feed TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sites ADD COLUMN empty_runs INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE sites ADD COLUMN needs_review INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE sites ADD COLUMN source_kind TEXT NOT NULL DEFAULT 'html'`,
		`ALTER TABLE sites ADD COLUMN api_url TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sites ADD COLUMN items_path TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sites ADD COLUMN item_root TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sites ADD COLUMN title_path TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sites ADD COLUMN content_path TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sites ADD COLUMN link_path TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sites ADD COLUMN link_template TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sites ADD COLUMN date_path TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sites ADD COLUMN date_format TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sites ADD COLUMN id_path TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sites ADD COLUMN headers TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sites ADD COLUMN weibo_uid TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := s.db.Exec(col); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			// modernc sqlite returns "duplicate column name" on re-migrate; ignore it
		}
	}
	_, err = s.db.Exec(`PRAGMA foreign_keys = ON`)
	return err
}

func DedupHash(url, title string) string {
	h := sha256.Sum256([]byte(strings.TrimSpace(url) + "|" + strings.TrimSpace(title)))
	return hex.EncodeToString(h[:])
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	if t.IsZero() {
		t, _ = time.Parse("2006-01-02 15:04:05", s)
	}
	return t.UTC()
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func (s *Store) CreateSite(site *Site) error {
	site.CreatedAt = time.Now().UTC()
	if site.ExtractionMode == "" {
		site.ExtractionMode = "manual"
	}
	if site.SourceKind == "" {
		site.SourceKind = "html"
	}
	_, err := s.db.Exec(`INSERT INTO sites(id,name,url,item_selector,title_selector,link_selector,content_selector,date_selector,poll_minutes,enabled,created_at,extraction_mode,discovered_feed,empty_runs,needs_review,source_kind,api_url,items_path,item_root,title_path,content_path,link_path,link_template,date_path,date_format,id_path,headers,weibo_uid)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		site.ID, site.Name, site.URL, site.ItemSelector, site.TitleSelector, site.LinkSelector,
		site.ContentSelector, site.DateSelector, site.PollMinutes, boolToInt(site.Enabled), formatTime(site.CreatedAt),
		site.ExtractionMode, site.DiscoveredFeed, site.EmptyRuns, boolToInt(site.NeedsReview),
		site.SourceKind, site.APIURL, site.ItemsPath, site.ItemRoot, site.TitlePath, site.ContentPath,
		site.LinkPath, site.LinkTemplate, site.DatePath, site.DateFormat, site.IDPath, site.Headers, site.WeiboUID)
	return err
}

func (s *Store) ListSites() ([]Site, error) {
	rows, err := s.db.Query(`SELECT id,name,url,item_selector,title_selector,link_selector,content_selector,date_selector,poll_minutes,enabled,last_checked,last_error,created_at,extraction_mode,discovered_feed,empty_runs,needs_review,source_kind,api_url,items_path,item_root,title_path,content_path,link_path,link_template,date_path,date_format,id_path,headers,weibo_uid FROM sites ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Site
	for rows.Next() {
		var st Site
		var enabled, needsReview int
		var lastChecked, createdAt sql.NullString
		var lastErr string
		if err := rows.Scan(&st.ID, &st.Name, &st.URL, &st.ItemSelector, &st.TitleSelector, &st.LinkSelector, &st.ContentSelector, &st.DateSelector, &st.PollMinutes, &enabled, &lastChecked, &lastErr, &createdAt, &st.ExtractionMode, &st.DiscoveredFeed, &st.EmptyRuns, &needsReview, &st.SourceKind, &st.APIURL, &st.ItemsPath, &st.ItemRoot, &st.TitlePath, &st.ContentPath, &st.LinkPath, &st.LinkTemplate, &st.DatePath, &st.DateFormat, &st.IDPath, &st.Headers, &st.WeiboUID); err != nil {
			return nil, err
		}
		st.Enabled = enabled != 0
		st.NeedsReview = needsReview != 0
		st.LastError = lastErr
		if lastChecked.Valid && lastChecked.String != "" {
			t := parseTime(lastChecked.String)
			st.LastChecked = &t
		}
		st.CreatedAt = parseTime(createdAt.String)
		out = append(out, st)
	}
	if out == nil {
		out = []Site{}
	}
	// attach counts
	for i := range out {
		var n int
		_ = s.db.QueryRow(`SELECT COUNT(*) FROM items WHERE site_id=?`, out[i].ID).Scan(&n)
		out[i].ItemCount = n
	}
	return out, rows.Err()
}

func (s *Store) GetSite(id string) (*Site, error) {
	var st Site
	var enabled, needsReview int
	var lastChecked, createdAt sql.NullString
	err := s.db.QueryRow(`SELECT id,name,url,item_selector,title_selector,link_selector,content_selector,date_selector,poll_minutes,enabled,last_checked,last_error,created_at,extraction_mode,discovered_feed,empty_runs,needs_review,source_kind,api_url,items_path,item_root,title_path,content_path,link_path,link_template,date_path,date_format,id_path,headers,weibo_uid FROM sites WHERE id=?`, id).
		Scan(&st.ID, &st.Name, &st.URL, &st.ItemSelector, &st.TitleSelector, &st.LinkSelector, &st.ContentSelector, &st.DateSelector, &st.PollMinutes, &enabled, &lastChecked, &st.LastError, &createdAt, &st.ExtractionMode, &st.DiscoveredFeed, &st.EmptyRuns, &needsReview, &st.SourceKind, &st.APIURL, &st.ItemsPath, &st.ItemRoot, &st.TitlePath, &st.ContentPath, &st.LinkPath, &st.LinkTemplate, &st.DatePath, &st.DateFormat, &st.IDPath, &st.Headers, &st.WeiboUID)
	if err != nil {
		return nil, err
	}
	st.Enabled = enabled != 0
	st.NeedsReview = needsReview != 0
	if lastChecked.Valid && lastChecked.String != "" {
		t := parseTime(lastChecked.String)
		st.LastChecked = &t
	}
	st.CreatedAt = parseTime(createdAt.String)
	return &st, nil
}

func (s *Store) UpdateSite(st *Site) error {
	_, err := s.db.Exec(`UPDATE sites SET name=?,url=?,item_selector=?,title_selector=?,link_selector=?,content_selector=?,date_selector=?,poll_minutes=?,enabled=?,extraction_mode=?,discovered_feed=?,empty_runs=?,needs_review=?,source_kind=?,api_url=?,items_path=?,item_root=?,title_path=?,content_path=?,link_path=?,link_template=?,date_path=?,date_format=?,id_path=?,headers=?,weibo_uid=? WHERE id=?`,
		st.Name, st.URL, st.ItemSelector, st.TitleSelector, st.LinkSelector, st.ContentSelector, st.DateSelector, st.PollMinutes, boolToInt(st.Enabled), st.ExtractionMode, st.DiscoveredFeed, st.EmptyRuns, boolToInt(st.NeedsReview), st.SourceKind, st.APIURL, st.ItemsPath, st.ItemRoot, st.TitlePath, st.ContentPath, st.LinkPath, st.LinkTemplate, st.DatePath, st.DateFormat, st.IDPath, st.Headers, st.WeiboUID, st.ID)
	return err
}

// SetNeedsReview flags (or clears) a site for manual attention, e.g. when a
// login cookie expires.
func (s *Store) SetNeedsReview(id string, need bool) error {
	_, err := s.db.Exec(`UPDATE sites SET needs_review=? WHERE id=?`, boolToInt(need), id)
	return err
}

func (s *Store) DeleteSite(id string) error {
	_, err := s.db.Exec(`DELETE FROM items WHERE site_id=?`, id)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM sites WHERE id=?`, id)
	return err
}

func (s *Store) DueSites(now time.Time) ([]Site, error) {
	all, err := s.ListSites()
	if err != nil {
		return nil, err
	}
	var due []Site
	for _, st := range all {
		if !st.Enabled {
			continue
		}
		if st.LastChecked == nil {
			due = append(due, st)
			continue
		}
		if now.Sub(*st.LastChecked) >= time.Duration(st.PollMinutes)*time.Minute {
			due = append(due, st)
		}
	}
	return due, nil
}

func (s *Store) SetCheckResult(id string, checked time.Time, errMsg string) error {
	_, err := s.db.Exec(`UPDATE sites SET last_checked=?, last_error=? WHERE id=?`, formatTime(checked), errMsg, id)
	return err
}

// NoteScrapeResult tracks consecutive empty scrapes: resets on success,
// flags needs_review after 3 straight empty runs (user-approved default).
func (s *Store) NoteScrapeResult(id string, found int) error {
	if found > 0 {
		_, err := s.db.Exec(`UPDATE sites SET empty_runs=0, needs_review=0 WHERE id=?`, id)
		return err
	}
	_, err := s.db.Exec(`UPDATE sites SET empty_runs=empty_runs+1, needs_review=CASE WHEN empty_runs+1>=3 THEN 1 ELSE needs_review END WHERE id=?`, id)
	return err
}

// AddItems inserts new items, ignoring duplicates. Returns number inserted.
func (s *Store) AddItems(siteID string, items []Item) (int, error) {
	now := formatTime(time.Now().UTC())
	inserted := 0
	for _, it := range items {
		hash := it.DedupHash
		if hash == "" {
			hash = DedupHash(it.URL, it.Title)
		}
		pub := it.PublishedAt
		if pub.IsZero() {
			pub = time.Now().UTC()
		}
		res, err := s.db.Exec(`INSERT OR IGNORE INTO items(site_id,title,url,content_html,published_at,dedup_hash,created_at) VALUES(?,?,?,?,?,?,?)`,
			siteID, it.Title, it.URL, it.ContentHTML, formatTime(pub), hash, now)
		if err != nil {
			return inserted, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			inserted++
		}
	}
	// retention: keep newest N per site
	_, _ = s.db.Exec(`DELETE FROM items WHERE site_id=? AND id NOT IN (SELECT id FROM items WHERE site_id=? ORDER BY published_at DESC, id DESC LIMIT ?)`, siteID, siteID, maxItemsPerSite)
	return inserted, nil
}

func (s *Store) LatestItems(siteID string, limit int) ([]Item, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT id,title,url,content_html,published_at,dedup_hash,created_at FROM items WHERE site_id=? ORDER BY published_at DESC, id DESC LIMIT ?`, siteID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var it Item
		var pub, created string
		if err := rows.Scan(&it.ID, &it.Title, &it.URL, &it.ContentHTML, &pub, &it.DedupHash, &created); err != nil {
			return nil, err
		}
		it.SiteID = siteID
		it.PublishedAt = parseTime(pub)
		it.CreatedAt = parseTime(created)
		out = append(out, it)
	}
	if out == nil {
		out = []Item{}
	}
	return out, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
