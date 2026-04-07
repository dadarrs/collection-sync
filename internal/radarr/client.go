package radarr

import (
	"context"
	"errors"
	"time"

	"golift.io/starr"
	starrradarr "golift.io/starr/radarr"
)

// Client wraps the golift/starr Radarr client.
type Client struct {
	api *starrradarr.Radarr
}

// New creates a Radarr client for the given server URL and API key.
func New(url, apiKey string) *Client {
	cfg := starr.New(apiKey, url, 30*time.Second)
	return &Client{api: starrradarr.New(cfg)}
}

// EnsureMovie adds the movie to Radarr if it does not already exist.
func (c *Client) EnsureMovie(ctx context.Context, title string, tmdbID int64) error {
	return errors.New("not implemented")
}
