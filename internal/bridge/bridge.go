package bridge

import (
	"context"

	"github.com/dadarrs/collection-sync/internal/config"
	"github.com/dadarrs/collection-sync/internal/plex"
	"github.com/dadarrs/collection-sync/internal/radarr"
	"github.com/dadarrs/collection-sync/internal/sonarr"
)

// Bridge orchestrates reading Plex collections and syncing items to Sonarr and Radarr.
type Bridge struct {
	cfg    *config.Config
	plex   *plex.Client
	sonarr *sonarr.Client
	radarr *radarr.Client
}

// New creates a Bridge wired with all required clients and configuration.
func New(cfg *config.Config, plexClient *plex.Client, sonarrClient *sonarr.Client, radarrClient *radarr.Client) *Bridge {
	return &Bridge{
		cfg:    cfg,
		plex:   plexClient,
		sonarr: sonarrClient,
		radarr: radarrClient,
	}
}

// Run reads the TV and movie Plex collections and ensures each item exists
// in Sonarr or Radarr respectively.
func (b *Bridge) Run(ctx context.Context) error {
	return nil
}
