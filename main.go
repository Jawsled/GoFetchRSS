package main

import (
	"embed"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"gofetchrss/internal/api"
	"gofetchrss/internal/poller"
	"gofetchrss/internal/store"
)

//go:embed web/*
var webFS embed.FS

func main() {
	addr := flag.String("addr", "127.0.0.1:9284", "listen address (use 127.0.0.1 to avoid firewall prompts)")
	dataDir := flag.String("data-dir", "./data", "directory for data.db (use %AppData%/GoFetchRSS for permanent install)")
	flag.Parse()

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		log.Fatal(err)
	}
	dbPath := filepath.Join(*dataDir, "data.db")
	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("open db %s: %v", dbPath, err)
	}
	defer st.Close()

	base := "http://" + *addr
	srv := &api.Server{Store: st, Base: base, WebFS: webFS}
	mux := http.NewServeMux()
	srv.Routes(mux)

	stop := make(chan struct{})
	defer close(stop)
	go poller.Start(st, 30*time.Second, stop, func(id string) error {
		return poller.RefreshSite(st, id)
	})

	fmt.Printf("GoFetchRSS listening on http://%s\n", *addr)
	fmt.Printf("Open the UI to add sites, then paste feed URLs like http://%s/feeds/<id>.xml into your reader.\n", *addr)
	fmt.Printf("Data: %s\n", dbPath)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
