# GoFetchRSS

A very simple Website-to-RSS converter written in Go, written natively for Windows.
No Docker, No WSL, No php / nginx / SQL server. just download and Go.
If you are on Linux and can run something like FreshRSS, that is more comprehensive and  better coded thatn this. But if you need Windows native, then this might be for you.

## Features

**Source types:**
- **Webpage :** Point it at a page plus CSS selectors and it extracts repeating items. Per-site poll interval, custom request headers/cookies.
- **JSON API.** Point it at any JSON endpoint with dot-path field mappings (items array, title/content/link/date, link templates like `https://…/{id}`).
- **Weibo user:** Enter just the numeric UID from `m.weibo.cn/u/<uid>`.

**Contents Extraction:**
- **Auto-detect (default):** Just paste Name + URL. The engine finds repeating link groups, prefers heading links over tag links, recognizes stretched-card layouts (whole card is one invisible link, title in a heading), and tells you when a site already publishes a native feed you should use directly.
- **Candidate picker:** When auto-detect finds several plausible lists, it offers each with sample titles and a Use button.
- **Visual picker:** "Pick visually" opens the page in an in-app preview (sandboxed iframe, scripts stripped, nothing runs). Hover highlights the exact element with a tag label, arrow keys / Parent-Child buttons / breadcrumb walk the tree, click assigns Item container, Title, and Link. Invisible overlay links are pierced automatically and suggested for the Link slot.

**Output formats:**
- `/feeds/{id}.xml` (RSS 2.0), `/feeds/{id}.atom`, `/feeds/{id}.json` (JSON Feed 1.1). Copy-icon buttons in the UI for each URL.
- `/export.opml` to bulk-import all generated feeds into your reader.
- Dedup by URL + title, keeps the newest 200 items per site.

**Background Checks: **
- Background polling every 30s tick with per-site intervals (minimum 5 minutes) and jitter.
- Sites that return zero items 3 times in a row, hit fetch errors, or hit a login wall get a "Needs review" flag instead of silently dying. Editing a site clears the flag.
- Single SQLite file (`data.db`), pure-Go driver, no CGO/gcc needed.

## Requirements

- Windows 10/11.
- To build from source: Go 1.27+.

## Installation

1. Build `GoFetchRSS.exe` (see Building). The app is portable, and creates / stores data in local directory.
2. Double-click it (or run it). It listens on `http://127.0.0.1:9284` by default.

Data lives next to the exe in `.\data\data.db`. For a permanent install, run with `--data-dir "%AppData%\GoFetchRSS"` so reinstalls do not touch your database.

## Usage

### HTML page

1. Click `+ Add site`, keep `Web page` selected.
2. Enter Name + Page URL. use `Autodetect` or open `Manual CSS selection` / `Visual picker`.
3. Save. The first poll runs within seconds. Copy the RSS/Atom/JSON URL into your reader.

### JSON API

1. New site, select **JSON API**.
2. Fill **API URL**, **Items path** (dot path to the array, e.g. `data.cards`), optional **Item root** (sub-object per item, e.g. `mblog`), and field paths for title/content/link/date. **Link template** supports `{field}` placeholders, e.g. `https://example.com/posts/{slug}`. Empty date format means auto-detect (RFC 3339, ctime, unix seconds/millis, and more).
3. **Test source** shows 5 mapped samples before you save.

### Weibo user

1. New site, select **Weibo user**, enter the numeric UID from the mobile profile URL (`m.weibo.cn/u/<uid>`).
2. Save. No cookie needed. The app performs the guest handshake (homepage, api/config for `XSRF-TOKEN`, `genvisitor2` for `SUB`/`SUBP`) and caches the session ~20 minutes, shared across all Weibo sites, with a cooldown between handshakes.
3. If Weibo rejects the session, the site flags Needs review and retries with a fresh session next poll. You may optionally paste your own session cookie into **Session headers** as `{"Cookie":"SUB=…"}`; a personal cookie always wins over the guest session.

### Session headers

Any source kind accepts extra request headers as a JSON object, e.g. `{"Cookie":"SUB=…","X-Foo":"bar"}`. Used for logged-in sessions, age gates, and API keys.

## HTTP API

Base URL defaults to `http://127.0.0.1:9284`. All site payloads are JSON (see `siteInput` in `internal/api/api.go` for every field).

| Method | Path | Purpose |
|---|---|---|
| GET | `/healthz` | Liveness check, returns `ok` |
| GET | `/api/sites` | List sites (with item counts and status) |
| POST | `/api/sites` | Create site (runs auto-detect for HTML when selectors omitted) |
| PUT | `/api/sites/{id}` | Update site (clears review flag) |
| DELETE | `/api/sites/{id}` | Delete site and its items |
| POST | `/api/sites/{id}/refresh` | Trigger an immediate poll (async, 202) |
| POST | `/api/preview` | Dry-run extraction, returns up to 5 sample items |
| POST | `/api/detect` | Auto-detect selectors + candidates for a URL |
| GET | `/pick?url=…` | Sanitized page mirror that powers the visual picker |
| GET | `/feeds/{id}.xml` | RSS 2.0 feed (`?limit=N`, default 50, max 200) |
| GET | `/feeds/{id}.atom` | Atom feed |
| GET | `/feeds/{id}.json` | JSON Feed 1.1 |
| GET | `/export.opml` | OPML download of all feeds |

Error convention: JSON `{"error":"…"}` with an appropriate status (400 validation, 401 login required, 422 weak auto-detect with a `guess` payload, 502 fetch failure).

## Configuration

Flags (all optional):

| Flag | Default | Purpose |
|---|---|---|
| `--addr` | `127.0.0.1:9284` | Listen address. Keep `127.0.0.1` for local-only (no firewall prompt). Use `0.0.0.0:9284` to serve your LAN (opens firewall scope, no auth in v1, so prefer localhost). |
| `--data-dir` | `./data` | Directory holding `data.db`. |

There is no config file and no authentication in v1. The security model is localhost-only binding.

## Building from source

```pwsh
# from the project root
go mod tidy
go vet ./...
go test ./...
go build -o GoFetchRSS.exe .
```

The result is a single self-contained exe (~18 MB). The web UI in `web/` is embedded via `go:embed`, so rebuild after any `web/` change. SQLite uses the pure-Go `modernc.org/sqlite` driver, so no C compiler is needed on Windows.

## Development

Project layout:

```
main.go                    # flags, embedded UI, server + poller startup
internal/store/            # SQLite: sites, items, dedup, retention, review flags
internal/scraper/          # HTML scraping (goquery), auto-detect, JSON extractor,
                           # Weibo API mapping + guest session manager
internal/poller/           # 30s scheduler loop, per-kind refresh, empty-run tracking
internal/feeds/            # RSS 2.0 / Atom / JSON Feed / OPML serializers
internal/api/              # HTTP routes, validation, preview/detect/pick handlers
internal/picker/           # page mirror transform (base fix, script/CSP/handler strip)
web/                       # UI: index.html, app.js, style.css, pick/picker-modal.js
```

Useful commands:

```pwsh
go test ./...                              # all unit tests
go test ./internal/scraper/ -run TestDetect -v   # auto-detect suite
node --check web/app.js                    # syntax-check UI scripts
node --check web/pick/picker-modal.js
```

Node-based checks exist for the picker helpers too: `picker-modal.js` exposes `window._rspkTest` (selector generation, overlay piercing, stretched-link suggestion) exercisable under node with a stub DOM.

A Playwright E2E harness (kept outside the repo, needs `playwright-core` plus a local Chromium) has covered: modal open, mirror load, hover piercing, click-to-assign, stretched-link autosuggest, title assignment, and "use selectors" form fill against a live hard site (sensortower.com/resources).

Conventions: stdlib + goquery + modernc sqlite only for runtime deps; pure functions (`ParseHTML`, `DetectHTML`, `ParseJSON`, `MirrorPage`) stay testable without network; E2E-unfriendly timing (slow subresources) is handled with interactive-ready polling rather than load-event waits.

## Troubleshooting

- **"Needs review: found 0 items 3+ times"**: the site changed markup or went JS-only. Re-run Auto-detect or Pick visually and save.
- **"login required …"**: session expired or the endpoint is login-walled. For Weibo the guest session refreshes automatically next poll; if it persists, paste your own cookie into Session headers.
- **"auto-detect found fewer than 3 items"**: the page may need JS, or the list uses an unusual structure. Try Pick visually or the candidate list from a Test run.
- **Feed shows URL-like titles**: the title selector matched an empty element; pick the heading inside the card instead (title falls back to the URL when empty).
- **Port in use**: start with `--addr 127.0.0.1:9285` (and update the feed URLs you pasted into your reader).
- **Slow first poll on big pages**: fetch cap is 10 MB; very heavy pages take a few seconds on first refresh.

## Current limitation
- No JS support for simpler implementation. if the site doesn't render even partially without JS disabled (you can test with uBO), this tool cannot see it either.

## Acknowledgement
- Weibo mobile cookie generation is inspired by [RSSHub](https://github.com/DIYgod/RSSHub)'s implementation. Thank you for  your amazing work.

## Disclosure on LLM use
- A lot of features were implemented with help of Qwen3.8.