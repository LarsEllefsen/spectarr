package specto

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const baseURL = "https://specto.bustbyte.no/api"

type Client struct {
	http         *http.Client
	accessToken  string
	refreshToken string
	tokenExpiry  time.Time
}

func New() *Client {
	return &Client{http: &http.Client{Timeout: 15 * time.Second}}
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type authResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
}

func (c *Client) Login(email, password string) error {
	body, _ := json.Marshal(loginRequest{Email: email, Password: password})
	resp, err := c.http.Post(baseURL+"/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("specto login: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("specto login: status %d", resp.StatusCode)
	}
	var ar authResponse
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return fmt.Errorf("specto login decode: %w", err)
	}
	c.accessToken = ar.AccessToken
	c.refreshToken = ar.RefreshToken
	c.tokenExpiry = time.Now().Add(14 * time.Minute) // 15m token, refresh at 14m
	return nil
}

func (c *Client) refresh() error {
	body, _ := json.Marshal(map[string]string{"refreshToken": c.refreshToken})
	resp, err := c.http.Post(baseURL+"/auth/refresh", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("specto refresh: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("specto refresh: status %d", resp.StatusCode)
	}
	var ar authResponse
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return fmt.Errorf("specto refresh decode: %w", err)
	}
	c.accessToken = ar.AccessToken
	c.refreshToken = ar.RefreshToken
	c.tokenExpiry = time.Now().Add(14 * time.Minute)
	return nil
}

func (c *Client) ensureToken() error {
	if time.Now().After(c.tokenExpiry) && c.refreshToken != "" {
		return c.refresh()
	}
	return nil
}

func (c *Client) get(path string, out any) error {
	if err := c.ensureToken(); err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodGet, baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("specto GET %s: status %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type Rating struct {
	ID        string  `json:"id"`
	TmdbID    int     `json:"tmdbId"`
	MediaType string  `json:"mediaType"`
	Rating    float64 `json:"rating"`
	Review    string  `json:"review"`
}

type ratingsPage struct {
	Data []Rating `json:"data"`
	Pagination struct {
		Page       int `json:"page"`
		TotalPages int `json:"totalPages"`
	} `json:"pagination"`
}

// GetMovieRatings fetches all MOVIE ratings for the authenticated user.
func (c *Client) GetMovieRatings() ([]Rating, error) {
	var all []Rating
	page := 1
	for {
		var rp ratingsPage
		path := fmt.Sprintf("/ratings?page=%d&limit=50", page)
		if err := c.get(path, &rp); err != nil {
			return nil, err
		}
		for _, r := range rp.Data {
			if r.MediaType == "MOVIE" {
				all = append(all, r)
			}
		}
		if page >= rp.Pagination.TotalPages {
			break
		}
		page++
	}
	return all, nil
}
