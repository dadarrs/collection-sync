package config

import (
	"errors"
	"fmt"
	"os"
)

// Config holds all runtime configuration loaded from environment variables.
type Config struct {
	PlexURL   string
	PlexToken string

	SonarrURL    string
	SonarrAPIKey string

	RadarrURL    string
	RadarrAPIKey string

	TVCollectionName    string
	MovieCollectionName string
}

// Load reads configuration from environment variables and validates all required values.
// For local development it first attempts to populate missing variables from a
// ".env" file in the working directory.
func Load() (*Config, error) {
	if err := loadDotEnv(".env"); err != nil {
		return nil, fmt.Errorf("loading .env: %w", err)
	}

	cfg := &Config{
		PlexURL:   os.Getenv("PLEX_URL"),
		PlexToken: os.Getenv("PLEX_TOKEN"),

		SonarrURL:    os.Getenv("SONARR_URL"),
		SonarrAPIKey: os.Getenv("SONARR_API_KEY"),

		RadarrURL:    os.Getenv("RADARR_URL"),
		RadarrAPIKey: os.Getenv("RADARR_API_KEY"),

		TVCollectionName:    os.Getenv("PLEX_TV_COLLECTION"),
		MovieCollectionName: os.Getenv("PLEX_MOVIE_COLLECTION"),
	}

	return cfg, cfg.validate()
}

func (c *Config) validate() error {
	required := []struct {
		name  string
		value string
	}{
		{"PLEX_URL", c.PlexURL},
		{"PLEX_TOKEN", c.PlexToken},
	}

	var errs []error
	for _, r := range required {
		if r.value == "" {
			errs = append(errs, fmt.Errorf("required env var %s is not set", r.name))
		}
	}

	return errors.Join(errs...)
}
