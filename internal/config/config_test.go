package config

import (
	"os"
	"testing"
)

func TestLoadStringEnvTrimsQuotes(t *testing.T) {
	t.Setenv("PLEX_TV_COLLECTION", `"Quoted Collection"`)

	got := loadStringEnv("PLEX_TV_COLLECTION")
	if got != "Quoted Collection" {
		t.Fatalf("loadStringEnv() = %q, want %q", got, "Quoted Collection")
	}
}

func TestLoadBoolEnvTrimsQuotes(t *testing.T) {
	t.Setenv("SEARCH_ADDED", `"true"`)

	got, err := loadBoolEnv("SEARCH_ADDED")
	if err != nil {
		t.Fatalf("loadBoolEnv() error = %v", err)
	}
	if !got {
		t.Fatal("loadBoolEnv() = false, want true")
	}
}

func TestLoadUsesQuotedRuntimeEnv(t *testing.T) {
	t.Setenv("PLEX_URL", "http://example")
	t.Setenv("PLEX_TOKEN", "token")
	t.Setenv("PLEX_TV_COLLECTION", `"TV Series Seasons To Be Removed"`)
	t.Setenv("PLEX_MOVIE_COLLECTION", `"To Be Removed From Library"`)

	resetOptionalEnv(t,
		"SONARR_URL",
		"SONARR_API_KEY",
		"SONARR_ROOT_FOLDER",
		"SONARR_QUALITY_PROFILE",
		"RADARR_URL",
		"RADARR_API_KEY",
		"RADARR_ROOT_FOLDER",
		"RADARR_QUALITY_PROFILE",
		"SEARCH_ADDED",
		"SEARCH_EXISTING",
		"INTERVAL",
	)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.TVCollectionName != "TV Series Seasons To Be Removed" {
		t.Fatalf("TVCollectionName = %q", cfg.TVCollectionName)
	}
	if cfg.MovieCollectionName != "To Be Removed From Library" {
		t.Fatalf("MovieCollectionName = %q", cfg.MovieCollectionName)
	}
}

func resetOptionalEnv(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("Unsetenv(%q) error = %v", name, err)
		}
	}
}
