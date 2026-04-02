package radarr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	http   *http.Client
	url    string
	apiKey string
}

func New(url, apiKey string) *Client {
	return &Client{
		http:   &http.Client{Timeout: 15 * time.Second},
		url:    strings.TrimRight(url, "/"),
		apiKey: apiKey,
	}
}

func (c *Client) do(method, path string, body any, out any) error {
	var bodyReader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(b)
	} else {
		bodyReader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, c.url+"/api/v3"+path, bodyReader)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("radarr %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("radarr %s %s: status %d", method, path, resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

type Movie struct {
	ID       int    `json:"id"`
	TmdbID   int    `json:"tmdbId"`
	Title    string `json:"title"`
	Year     int    `json:"year"`
	Monitored bool  `json:"monitored"`
}

// GetMonitoredTmdbIDs returns a set of TMDB IDs already in Radarr.
func (c *Client) GetMonitoredTmdbIDs() (map[int]struct{}, error) {
	var movies []Movie
	if err := c.do(http.MethodGet, "/movie", nil, &movies); err != nil {
		return nil, err
	}
	ids := make(map[int]struct{}, len(movies))
	for _, m := range movies {
		ids[m.TmdbID] = struct{}{}
	}
	return ids, nil
}

type lookupMovie struct {
	Title  string `json:"title"`
	Year   int    `json:"year"`
	TmdbID int    `json:"tmdbId"`
	Images []struct {
		CoverType string `json:"coverType"`
		RemoteURL string `json:"remoteUrl"`
	} `json:"images"`
	Ratings struct {
		Value float64 `json:"value"`
	} `json:"ratings"`
}

type addMovieRequest struct {
	Title            string      `json:"title"`
	Year             int         `json:"year"`
	TmdbID           int         `json:"tmdbId"`
	QualityProfileID int         `json:"qualityProfileId"`
	RootFolderPath   string      `json:"rootFolderPath"`
	Monitored        bool        `json:"monitored"`
	AddOptions       addOptions  `json:"addOptions"`
	Images           interface{} `json:"images"`
}

type addOptions struct {
	SearchForMovie bool `json:"searchForMovie"`
}

// LookupByTmdbID returns the title and release year for a TMDB ID without adding it to Radarr.
func (c *Client) LookupByTmdbID(tmdbID int) (title string, year int, err error) {
	var m lookupMovie
	if err = c.do(http.MethodGet, fmt.Sprintf("/movie/lookup/tmdb?tmdbId=%d", tmdbID), nil, &m); err != nil {
		return "", 0, fmt.Errorf("lookup tmdb %d: %w", tmdbID, err)
	}
	return m.Title, m.Year, nil
}

// AddMovie adds a movie to Radarr by TMDB ID. Returns the movie title.
func (c *Client) AddMovie(tmdbID, qualityProfileID int, rootFolderPath string) (string, error) {
	var m lookupMovie
	if err := c.do(http.MethodGet, fmt.Sprintf("/movie/lookup/tmdb?tmdbId=%d", tmdbID), nil, &m); err != nil {
		return "", fmt.Errorf("lookup tmdb %d: %w", tmdbID, err)
	}
	req := addMovieRequest{
		Title:            m.Title,
		Year:             m.Year,
		TmdbID:           tmdbID,
		QualityProfileID: qualityProfileID,
		RootFolderPath:   rootFolderPath,
		Monitored:        true,
		AddOptions:       addOptions{SearchForMovie: true},
		Images:           m.Images,
	}
	var added Movie
	if err := c.do(http.MethodPost, "/movie", req, &added); err != nil {
		return "", fmt.Errorf("add movie %q: %w", m.Title, err)
	}
	return m.Title, nil
}

// GetQualityProfiles returns available quality profiles (id → name).
func (c *Client) GetQualityProfiles() ([]QualityProfile, error) {
	var profiles []QualityProfile
	err := c.do(http.MethodGet, "/qualityprofile", nil, &profiles)
	return profiles, err
}

type QualityProfile struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// GetRootFolders returns configured root folders.
func (c *Client) GetRootFolders() ([]RootFolder, error) {
	var folders []RootFolder
	err := c.do(http.MethodGet, "/rootfolder", nil, &folders)
	return folders, err
}

type RootFolder struct {
	ID   int    `json:"id"`
	Path string `json:"path"`
}
