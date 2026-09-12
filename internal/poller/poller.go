package poller

import (
	"errors"
	"log"
	"math/rand"
	"time"

	"gofetchrss/internal/scraper"
	"gofetchrss/internal/store"
)

// Start runs a background loop that polls due sites every interval.
func Start(st *store.Store, interval time.Duration, stop <-chan struct{}, refreshNow func(id string) error) {
	t := time.NewTicker(interval)
	defer t.Stop()
	// initial poll shortly after start
	go func() {
		time.Sleep(3 * time.Second)
		pollDue(st)
	}()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			pollDue(st)
		}
	}
}

func pollDue(st *store.Store) {
	due, err := st.DueSites(time.Now().UTC())
	if err != nil {
		log.Printf("poller: list due: %v", err)
		return
	}
	for _, site := range due {
		// small jitter to avoid thundering herd
		time.Sleep(time.Duration(rand.Intn(1500)) * time.Millisecond)
		if err := RefreshSite(st, site.ID); err != nil {
			log.Printf("poller: site %s (%s): %v", site.Name, site.URL, err)
		} else {
			log.Printf("poller: refreshed %s", site.Name)
		}
	}
}

// RefreshSite scrapes one site and stores new items.
func RefreshSite(st *store.Store, id string) error {
	site, err := st.GetSite(id)
	if err != nil {
		return err
	}
	scraped, err := ScrapeSite(site)
	now := time.Now().UTC()
	if err != nil {
		msg := err.Error()
		if errors.Is(err, scraper.ErrLoginRequired) {
			msg = "login required: guest session rejected (auto-retries next poll; or paste your own Cookie in site settings)"
			_ = st.SetNeedsReview(id, true)
		}
		_ = st.SetCheckResult(id, now, msg)
		return err
	}
	items := make([]store.Item, 0, len(scraped))
	for _, sc := range scraped {
		items = append(items, store.Item{
			Title:       sc.Title,
			URL:         sc.URL,
			ContentHTML: sc.ContentHTML,
			PublishedAt: sc.PublishedAt,
			DedupHash:   store.DedupHash(sc.URL, sc.Title),
		})
	}
	if _, err := st.AddItems(id, items); err != nil {
		_ = st.SetCheckResult(id, now, err.Error())
		return err
	}
	_ = st.NoteScrapeResult(id, len(scraped))
	return st.SetCheckResult(id, now, "")
}

// ScrapeSite fetches items according to the site's source kind.
func ScrapeSite(site *store.Site) ([]scraper.ScrapedItem, error) {
	switch site.SourceKind {
	case "json":
		headers, err := scraper.ParseHeaders(site.Headers)
		if err != nil {
			return nil, err
		}
		return scraper.FetchJSON(site.APIURL, headers, scraper.JSONSpec{
			ItemsPath: site.ItemsPath, ItemRoot: site.ItemRoot,
			TitlePath: site.TitlePath, ContentPath: site.ContentPath,
			LinkPath: site.LinkPath, LinkTemplate: site.LinkTemplate,
			DatePath: site.DatePath, DateFormat: site.DateFormat,
		})
	case "weibo":
		headers, err := scraper.ParseHeaders(site.Headers)
		if err != nil {
			return nil, err
		}
		return scraper.FetchWeibo(site.WeiboUID, headers)
	default: // html
		headers, err := scraper.ParseHeaders(site.Headers)
		if err != nil {
			return nil, err
		}
		if len(headers) == 0 {
			return scraper.ScrapeURL(site.URL, scraper.Config{
				SiteURL:         site.URL,
				ItemSelector:    site.ItemSelector,
				TitleSelector:   site.TitleSelector,
				LinkSelector:    site.LinkSelector,
				ContentSelector: site.ContentSelector,
				DateSelector:    site.DateSelector,
			})
		}
		return ScrapeURLWithHeaders(site, headers)
	}
}

// ScrapeURLWithHeaders scrapes HTML using custom request headers.
func ScrapeURLWithHeaders(site *store.Site, headers map[string]string) ([]scraper.ScrapedItem, error) {
	raw, _, err := scraper.FetchRawWithHeaders(site.URL, headers)
	if err != nil {
		return nil, err
	}
	return scraper.ParseHTML(site.URL, raw, scraper.Config{
		SiteURL:         site.URL,
		ItemSelector:    site.ItemSelector,
		TitleSelector:   site.TitleSelector,
		LinkSelector:    site.LinkSelector,
		ContentSelector: site.ContentSelector,
		DateSelector:    site.DateSelector,
	})
}

// Preview fetches without saving, for the "Test selectors" button.
func Preview(pageURL string, cfg scraper.Config) ([]scraper.ScrapedItem, error) {
	return scraper.ScrapeURL(pageURL, cfg)
}
