package scheduler

import (
	"fmt"
	"log"
	"strings"
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

	ratings, err := s.collectRatings(sc, cfg)
	if err != nil {
		return 0, err
	}

	rc := radarr.New(radarrURL, radarrKey)
	monitored, err := rc.GetMonitoredTmdbIDs()
	if err != nil {
		return 0, fmt.Errorf("fetch radarr movies: %w", err)
	}

	rejected, err := s.store.GetRejectedTmdbIDs()
	if err != nil {
		return 0, fmt.Errorf("fetch rejected movies: %w", err)
	}

	downloadMode := cfg["download_mode"]
	added := 0
	for _, r := range ratings {
		if r.Rating < threshold {
			continue
		}
		if _, exists := monitored[r.TmdbID]; exists {
			continue
		}
		if _, exists := rejected[r.TmdbID]; exists {
			continue
		}
		if downloadMode == "manual" {
			title, year, err := rc.LookupByTmdbID(r.TmdbID)
			if err != nil {
				log.Printf("scheduler: lookup tmdb %d: %v", r.TmdbID, err)
				continue
			}
			if err := s.store.AddPendingMovie(r.TmdbID, title, year, r.Rating); err != nil {
				log.Printf("scheduler: queue pending tmdb %d: %v", r.TmdbID, err)
			} else {
				log.Printf("scheduler: queued %q (tmdb %d, rating %.1f) for manual review", title, r.TmdbID, r.Rating)
			}
		} else {
			title, err := rc.AddMovie(r.TmdbID, qualityProfileID, rootFolder)
			if err != nil {
				log.Printf("scheduler: add tmdb %d: %v", r.TmdbID, err)
				continue
			}
			log.Printf("scheduler: added %q (tmdb %d, rating %.1f)", title, r.TmdbID, r.Rating)
			added++
		}
	}
	return added, nil
}

// collectRatings returns deduplicated ratings from the configured sync source.
// When multiple sources rate the same movie, the highest rating is kept.
func (s *Scheduler) collectRatings(sc *specto.Client, cfg map[string]string) ([]specto.Rating, error) {
	if cfg["sync_mode"] == "selected_friends" {
		raw := strings.TrimSpace(cfg["selected_friend_ids"])
		if raw == "" {
			return nil, nil
		}
		return fetchAndMerge(sc, strings.Split(raw, ","))
	}

	// default: all_friends
	friends, err := sc.GetFriends()
	if err != nil {
		return nil, fmt.Errorf("fetch friends: %w", err)
	}
	ids := make([]string, len(friends))
	for i, f := range friends {
		ids[i] = f.ID
	}
	return fetchAndMerge(sc, ids)
}

// fetchAndMerge fetches ratings for each user ID and deduplicates by TmdbID,
// keeping the highest rating seen across all sources.
func fetchAndMerge(sc *specto.Client, userIDs []string) ([]specto.Rating, error) {
	log.Printf("scheduler: fetch ratings for users")
	best := make(map[int]specto.Rating)
	for _, id := range userIDs {
		log.Printf("scheduler: fetch ratings for user %s", id)
		ratings, err := sc.GetMovieRatingsByUser(strings.TrimSpace(id))
		if err != nil {
			log.Printf("scheduler: fetch ratings for user %s: %v", id, err)
			continue
		}
		for _, r := range ratings {
			if existing, ok := best[r.TmdbID]; !ok || r.Rating > existing.Rating {
				best[r.TmdbID] = r
			}
		}
	}
	result := make([]specto.Rating, 0, len(best))
	for _, r := range best {
		result = append(result, r)
	}
	return result, nil
}
