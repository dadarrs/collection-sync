package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds all runtime configuration loaded from environment variables.
type Config struct {
	PlexURL   string
	PlexToken string

	SonarrURL            string
	SonarrAPIKey         string
	SonarrRootFolder     string
	SonarrQualityProfile string
	SearchAdded          bool
	SearchExisting       bool

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

	searchAdded, err := loadBoolEnv("SEARCH_ADDED")
	if err != nil {
		return nil, err
	}
	searchExisting, err := loadBoolEnv("SEARCH_EXISTING")
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		PlexURL:   os.Getenv("PLEX_URL"),
		PlexToken: os.Getenv("PLEX_TOKEN"),

		SonarrURL:            os.Getenv("SONARR_URL"),
		SonarrAPIKey:         os.Getenv("SONARR_API_KEY"),
		SonarrRootFolder:     os.Getenv("SONARR_ROOT_FOLDER"),
		SonarrQualityProfile: os.Getenv("SONARR_QUALITY_PROFILE"),
		SearchAdded:          searchAdded,
		SearchExisting:       searchExisting,

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

func loadBoolEnv(name string) (bool, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return false, nil
	}

	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("parsing %s: %w", name, err)
	}

	return parsed, nil
}
