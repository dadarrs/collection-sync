package sonarr

import (
	"context"
	"errors"
	"time"

	"golift.io/starr"
	starrsonarr "golift.io/starr/sonarr"
)

// Client wraps the golift/starr Sonarr client.
type Client struct {
	api *starrsonarr.Sonarr
}

// New creates a Sonarr client for the given server URL and API key.
func New(url, apiKey string) *Client {
	cfg := starr.New(apiKey, url, 30*time.Second)
	return &Client{api: starrsonarr.New(cfg)}
}

// EnsureSeries adds the series to Sonarr if it does not already exist.
func (c *Client) EnsureSeries(ctx context.Context, title string, tvdbID int64) error {
	return errors.New("not implemented")
}
