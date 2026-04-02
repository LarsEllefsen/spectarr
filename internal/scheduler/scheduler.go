package scheduler

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/larsellefsen/spectarr/internal/config"
	"github.com/larsellefsen/spectarr/internal/radarr"
	"github.com/larsellefsen/spectarr/internal/specto"
)

type Scheduler struct {
	store  *config.Store
	mu     sync.Mutex
	stop   chan struct{}
	manual chan struct{}
}

func New(store *config.Store) *Scheduler {
	return &Scheduler{
		store:  store,
		stop:   make(chan struct{}),
		manual: make(chan struct{}, 1),
	}
}

// Start runs the scheduler in a background goroutine.
func (s *Scheduler) Start() {
	go s.loop()
}

// Stop shuts down the scheduler.
func (s *Scheduler) Stop() {
	close(s.stop)
}

// TriggerNow requests an immediate sync run (non-blocking).
func (s *Scheduler) TriggerNow() {
	select {
	case s.manual <- struct{}{}:
	default:
	}
}

func (s *Scheduler) loop() {
	// Run once at startup.
	s.runAndLog()

	for {
		interval := time.Duration(s.store.GetInt("poll_interval_minutes")) * time.Minute
		if interval <= 0 {
			interval = 60 * time.Minute
		}
		t := time.NewTimer(interval)
		select {
		case <-s.stop:
			t.Stop()
			return
		case <-t.C:
			s.runAndLog()
		case <-s.manual:
			t.Stop()
			s.runAndLog()
		}
	}
}

func (s *Scheduler) runAndLog() {
	s.mu.Lock()
	defer s.mu.Unlock()
	added, err := s.run()
	if logErr := s.store.WriteRunLog(added, err); logErr != nil {
		log.Printf("scheduler: write run log: %v", logErr)
	}
	if err != nil {
		log.Printf("scheduler: sync error: %v", err)
	} else {
		log.Printf("scheduler: sync complete, %d movie(s) added", added)
	}
}

func (s *Scheduler) run() (int, error) {
	cfg := s.store.GetAll()

	email := cfg["specto_email"]
	password := cfg["specto_password"]
	if email == "" || password == "" {
		return 0, fmt.Errorf("Specto credentials not configured")
	}

	radarrURL := cfg["radarr_url"]
	radarrKey := cfg["radarr_api_key"]
	if radarrURL == "" || radarrKey == "" {
		return 0, fmt.Errorf("Radarr URL/API key not configured")
	}

	threshold := s.store.GetFloat("rating_threshold")
	qualityProfileID := s.store.GetInt("radarr_quality_profile_id")
	rootFolder := cfg["radarr_root_folder_path"]

	sc := specto.New()
	if err := sc.Login(email, password); err != nil {
		return 0, fmt.Errorf("specto login: %w", err)
	}

	ratings, err := sc.GetMovieRatings()
	if err != nil {
		return 0, fmt.Errorf("fetch ratings: %w", err)
	}

	rc := radarr.New(radarrURL, radarrKey)
	monitored, err := rc.GetMonitoredTmdbIDs()
	if err != nil {
		return 0, fmt.Errorf("fetch radarr movies: %w", err)
	}

	added := 0
	for _, r := range ratings {
		if r.Rating < threshold {
			continue
		}
		if _, exists := monitored[r.TmdbID]; exists {
			continue
		}
		title, err := rc.AddMovie(r.TmdbID, qualityProfileID, rootFolder)
		if err != nil {
			log.Printf("scheduler: add tmdb %d: %v", r.TmdbID, err)
			continue
		}
		log.Printf("scheduler: added %q (tmdb %d, rating %.1f)", title, r.TmdbID, r.Rating)
		added++
	}
	return added, nil
}
