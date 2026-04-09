package bridge

import (
	"context"
	"testing"

	"github.com/dadarrs/collection-sync/internal/config"
	"github.com/dadarrs/collection-sync/internal/plex"
	"github.com/dadarrs/collection-sync/internal/radarr"
	"github.com/dadarrs/collection-sync/internal/sonarr"
)

func TestNew(t *testing.T) {
	cfg := &config.Config{PlexURL: "http://plex"}
	plexClient := plex.New("http://plex", "token")
	sonarrClient := sonarr.New("http://sonarr", "key")
	radarrClient := radarr.New("http://radarr", "key")

	bridge := New(cfg, plexClient, sonarrClient, radarrClient)
	if bridge.cfg != cfg || bridge.plex != plexClient || bridge.sonarr != sonarrClient || bridge.radarr != radarrClient {
		t.Fatalf("New() = %+v, want injected dependencies preserved", bridge)
	}
}

func TestRun(t *testing.T) {
	bridge := &Bridge{}
	if err := bridge.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
}
