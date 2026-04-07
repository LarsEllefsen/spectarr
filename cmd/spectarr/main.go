package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/larsellefsen/spectarr/internal/config"
	"github.com/larsellefsen/spectarr/internal/scheduler"
	"github.com/larsellefsen/spectarr/internal/web"
)

func main() {
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "./data"
	}
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}

	dbPath := filepath.Join(dataDir, "spectarr.db")
	store, err := config.Open(dbPath, dataDir)
	if err != nil {
		log.Fatalf("open config store: %v", err)
	}
	defer store.Close()

	sched := scheduler.New(store)
	sched.Start()
	defer sched.Stop()

	handler, err := web.NewHandler(store, sched)
	if err != nil {
		log.Fatalf("init web handler: %v", err)
	}

	addr := ":6969"
	log.Printf("spectarr listening on http://localhost%s", addr)
	if err := http.ListenAndServe(addr, handler.Routes()); err != nil {
		log.Fatalf("http server: %v", err)
	}
}
