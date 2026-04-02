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

	alreadyRated, err := sc.GetMyRatedTmdbIDs()
	if err != nil {
		return 0, fmt.Errorf("fetch own ratings: %w", err)
	}

	downloadMode := cfg["download_mode"]
	added := 0
	for _, r := range ratings {
		if r.Rating.Rating < threshold {
			continue
		}
		if _, exists := monitored[r.TmdbID]; exists {
			continue
		}
		if _, exists := rejected[r.TmdbID]; exists {
			continue
		}
		if _, exists := alreadyRated[r.TmdbID]; exists {
			continue
		}
		if downloadMode == "manual" {
			info, err := rc.LookupByTmdbID(r.TmdbID)
			if err != nil {
				log.Printf("scheduler: lookup tmdb %d: %v", r.TmdbID, err)
				continue
			}
			if err := s.store.AddPendingMovie(r.TmdbID, info.Title, info.Year, r.Rating.Rating, info.PosterURL, info.ImdbID, r.SuggestedBy); err != nil {
				log.Printf("scheduler: queue pending tmdb %d: %v", r.TmdbID, err)
			} else {
				log.Printf("scheduler: queued %q (tmdb %d, rating %.1f) for manual review", info.Title, r.TmdbID, r.Rating.Rating)
			}
		} else {
			title, err := rc.AddMovie(r.TmdbID, qualityProfileID, rootFolder)
			if err != nil {
				log.Printf("scheduler: add tmdb %d: %v", r.TmdbID, err)
				continue
			}
			log.Printf("scheduler: added %q (tmdb %d, rating %.1f)", title, r.TmdbID, r.Rating.Rating)
			added++
		}
	}
	return added, nil
}

// attributedRating is a rating with the name of the friend who triggered it.
type attributedRating struct {
	specto.Rating
	SuggestedBy string
}

// collectRatings returns deduplicated ratings from the configured sync source.
// When multiple sources rate the same movie, the highest rating is kept.
func (s *Scheduler) collectRatings(sc *specto.Client, cfg map[string]string) ([]attributedRating, error) {
	friends, err := sc.GetFriends()
	if err != nil {
		return nil, fmt.Errorf("fetch friends: %w", err)
	}

	if cfg["sync_mode"] == "selected_friends" {
		raw := strings.TrimSpace(cfg["selected_friend_ids"])
		if raw == "" {
			return nil, nil
		}
		selected := make(map[string]bool)
		for _, id := range strings.Split(raw, ",") {
			selected[strings.TrimSpace(id)] = true
		}
		var filtered []specto.Friend
		for _, f := range friends {
			if selected[f.ID] {
				filtered = append(filtered, f)
			}
		}
		friends = filtered
	}

	return fetchAndMerge(sc, friends)
}

// fetchAndMerge fetches ratings for each friend and deduplicates by TmdbID,
// keeping the highest rating seen across all sources.
func fetchAndMerge(sc *specto.Client, friends []specto.Friend) ([]attributedRating, error) {
	type entry struct {
		rating      specto.Rating
		suggestedBy string
	}
	best := make(map[int]entry)
	for _, f := range friends {
		ratings, err := sc.GetMovieRatingsByUser(f.ID)
		if err != nil {
			log.Printf("scheduler: fetch ratings for user %s: %v", f.ID, err)
			continue
		}
		name := f.FullName
		if name == "" {
			name = f.Username
		}
		for _, r := range ratings {
			if e, ok := best[r.TmdbID]; !ok || r.Rating > e.rating.Rating {
				best[r.TmdbID] = entry{rating: r, suggestedBy: name}
			}
		}
	}
	result := make([]attributedRating, 0, len(best))
	for _, e := range best {
		result = append(result, attributedRating{Rating: e.rating, SuggestedBy: e.suggestedBy})
	}
	return result, nil
}
