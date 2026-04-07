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
	configDir := "/config"
	if err := os.MkdirAll(configDir, 0755); err != nil {
		log.Fatalf("create config dir: %v", err)
	}

	dbPath := filepath.Join(configDir, "spectarr.db")
	store, err := config.Open(dbPath, configDir)
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
