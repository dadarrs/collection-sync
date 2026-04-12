package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const defaultMaxItemsProcessedPerRun = 30

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

	RadarrURL            string
	RadarrAPIKey         string
	RadarrRootFolder     string
	RadarrQualityProfile string

	TVCollectionName    string
	MovieCollectionName string

	Interval                string
	MaxItemsProcessedPerRun int
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
	maxItemsProcessedPerRun, err := loadPositiveIntEnv("MAX_ITEMS_PROCESSED_PER_RUN", defaultMaxItemsProcessedPerRun)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		PlexURL:   loadStringEnv("PLEX_URL"),
		PlexToken: loadStringEnv("PLEX_TOKEN"),

		SonarrURL:            loadStringEnv("SONARR_URL"),
		SonarrAPIKey:         loadStringEnv("SONARR_API_KEY"),
		SonarrRootFolder:     loadStringEnv("SONARR_ROOT_FOLDER"),
		SonarrQualityProfile: loadStringEnv("SONARR_QUALITY_PROFILE"),
		SearchAdded:          searchAdded,
		SearchExisting:       searchExisting,

		RadarrURL:            loadStringEnv("RADARR_URL"),
		RadarrAPIKey:         loadStringEnv("RADARR_API_KEY"),
		RadarrRootFolder:     loadStringEnv("RADARR_ROOT_FOLDER"),
		RadarrQualityProfile: loadStringEnv("RADARR_QUALITY_PROFILE"),

		TVCollectionName:    loadStringEnv("PLEX_TV_COLLECTION"),
		MovieCollectionName: loadStringEnv("PLEX_MOVIE_COLLECTION"),

		Interval:                loadStringEnv("INTERVAL"),
		MaxItemsProcessedPerRun: maxItemsProcessedPerRun,
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
	value := loadStringEnv(name)
	if value == "" {
		return false, nil
	}

	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("parsing %s: %w", name, err)
	}

	return parsed, nil
}

func loadPositiveIntEnv(name string, defaultValue int) (int, error) {
	value := loadStringEnv(name)
	if value == "" {
		return defaultValue, nil
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parsing %s: %w", name, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s must be greater than 0", name)
	}

	return parsed, nil
}

func loadStringEnv(name string) string {
	return trimMatchingQuotes(strings.TrimSpace(os.Getenv(name)))
}
