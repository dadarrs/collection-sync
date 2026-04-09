package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var configEnvNames = []string{
	"PLEX_URL",
	"PLEX_TOKEN",
	"SONARR_URL",
	"SONARR_API_KEY",
	"SONARR_ROOT_FOLDER",
	"SONARR_QUALITY_PROFILE",
	"RADARR_URL",
	"RADARR_API_KEY",
	"RADARR_ROOT_FOLDER",
	"RADARR_QUALITY_PROFILE",
	"PLEX_TV_COLLECTION",
	"PLEX_MOVIE_COLLECTION",
	"SEARCH_ADDED",
	"SEARCH_EXISTING",
	"INTERVAL",
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name            string
		env             map[string]string
		dotEnv          string
		wantTV          string
		wantMovie       string
		wantSearchAdded bool
		wantSearchExist bool
		wantErrContains []string
	}{
		{
			name:            "uses dot env when runtime env absent",
			dotEnv:          "PLEX_URL=http://plex\nPLEX_TOKEN=token\nPLEX_TV_COLLECTION=TV\nPLEX_MOVIE_COLLECTION=Movies\nSEARCH_ADDED=true\nSEARCH_EXISTING=true\n",
			wantTV:          "TV",
			wantMovie:       "Movies",
			wantSearchAdded: true,
			wantSearchExist: true,
		},
		{
			name: "runtime env takes precedence over dot env",
			env: map[string]string{
				"PLEX_URL":              "http://runtime",
				"PLEX_TOKEN":            "runtime-token",
				"PLEX_TV_COLLECTION":    "Runtime TV",
				"PLEX_MOVIE_COLLECTION": "Runtime Movies",
			},
			dotEnv:    "PLEX_URL=http://dotenv\nPLEX_TOKEN=dotenv-token\nPLEX_TV_COLLECTION=DotEnv TV\nPLEX_MOVIE_COLLECTION=DotEnv Movies\n",
			wantTV:    "Runtime TV",
			wantMovie: "Runtime Movies",
		},
		{
			name: "missing plex url",
			env: map[string]string{
				"PLEX_TOKEN": "token",
			},
			wantErrContains: []string{"required env var PLEX_URL is not set"},
		},
		{
			name: "missing plex token",
			env: map[string]string{
				"PLEX_URL": "http://plex",
			},
			wantErrContains: []string{"required env var PLEX_TOKEN is not set"},
		},
		{
			name:            "missing both required values joins errors",
			wantErrContains: []string{"required env var PLEX_URL is not set", "required env var PLEX_TOKEN is not set"},
		},
		{
			name: "invalid search added",
			env: map[string]string{
				"PLEX_URL":     "http://plex",
				"PLEX_TOKEN":   "token",
				"SEARCH_ADDED": "nope",
			},
			wantErrContains: []string{"parsing SEARCH_ADDED"},
		},
		{
			name: "invalid search existing",
			env: map[string]string{
				"PLEX_URL":        "http://plex",
				"PLEX_TOKEN":      "token",
				"SEARCH_EXISTING": "nope",
			},
			wantErrContains: []string{"parsing SEARCH_EXISTING"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetConfigEnvForTest(t)
			useTempWorkingDir(t)

			if tt.dotEnv != "" {
				writeFile(t, filepath.Join(".env"), tt.dotEnv)
			}
			for key, value := range tt.env {
				t.Setenv(key, value)
			}

			cfg, err := Load()
			if len(tt.wantErrContains) > 0 {
				if err == nil {
					t.Fatal("Load() error = nil, want error")
				}
				for _, want := range tt.wantErrContains {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("Load() error = %q, want substring %q", err.Error(), want)
					}
				}
				return
			}

			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.TVCollectionName != tt.wantTV {
				t.Fatalf("TVCollectionName = %q, want %q", cfg.TVCollectionName, tt.wantTV)
			}
			if cfg.MovieCollectionName != tt.wantMovie {
				t.Fatalf("MovieCollectionName = %q, want %q", cfg.MovieCollectionName, tt.wantMovie)
			}
			if cfg.SearchAdded != tt.wantSearchAdded {
				t.Fatalf("SearchAdded = %t, want %t", cfg.SearchAdded, tt.wantSearchAdded)
			}
			if cfg.SearchExisting != tt.wantSearchExist {
				t.Fatalf("SearchExisting = %t, want %t", cfg.SearchExisting, tt.wantSearchExist)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		cfg := &Config{PlexURL: "http://plex", PlexToken: "token"}
		if err := cfg.validate(); err != nil {
			t.Fatalf("validate() error = %v", err)
		}
	})

	t.Run("aggregates missing values", func(t *testing.T) {
		cfg := &Config{}
		err := cfg.validate()
		if err == nil {
			t.Fatal("validate() error = nil, want error")
		}
		for _, want := range []string{"PLEX_URL", "PLEX_TOKEN"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("validate() error = %q, want substring %q", err.Error(), want)
			}
		}
	})
}

func TestLoadDotEnv(t *testing.T) {
	t.Run("missing file returns nil", func(t *testing.T) {
		resetConfigEnvForTest(t)
		if err := loadDotEnv(filepath.Join(t.TempDir(), "missing.env")); err != nil {
			t.Fatalf("loadDotEnv() error = %v", err)
		}
	})

	t.Run("ignores malformed comments and blanks and keeps existing env", func(t *testing.T) {
		resetConfigEnvForTest(t)
		t.Setenv("PLEX_URL", "http://runtime")

		path := filepath.Join(t.TempDir(), ".env")
		writeFile(t, path, strings.Join([]string{
			"# comment",
			"",
			"malformed-line",
			"PLEX_URL=http://dotenv",
			"PLEX_TOKEN='quoted-token'",
			"PLEX_TV_COLLECTION = \"TV Collection\"",
		}, "\n"))

		if err := loadDotEnv(path); err != nil {
			t.Fatalf("loadDotEnv() error = %v", err)
		}

		if got := os.Getenv("PLEX_URL"); got != "http://runtime" {
			t.Fatalf("PLEX_URL = %q, want runtime value", got)
		}
		if got := os.Getenv("PLEX_TOKEN"); got != "quoted-token" {
			t.Fatalf("PLEX_TOKEN = %q, want quoted-token", got)
		}
		if got := os.Getenv("PLEX_TV_COLLECTION"); got != "TV Collection" {
			t.Fatalf("PLEX_TV_COLLECTION = %q, want TV Collection", got)
		}
	})

	t.Run("scanner error is returned", func(t *testing.T) {
		resetConfigEnvForTest(t)
		path := filepath.Join(t.TempDir(), ".env")
		writeFile(t, path, fmt.Sprintf("PLEX_URL=%s\n", strings.Repeat("a", 70*1024)))

		err := loadDotEnv(path)
		if err == nil {
			t.Fatal("loadDotEnv() error = nil, want scanner error")
		}
	})
}

func TestParseDotEnvLine(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		wantKey   string
		wantValue string
		wantOK    bool
	}{
		{name: "plain", line: "KEY=VALUE", wantKey: "KEY", wantValue: "VALUE", wantOK: true},
		{name: "spaces", line: " KEY = VALUE ", wantKey: "KEY", wantValue: "VALUE", wantOK: true},
		{name: "quoted", line: "KEY=\"quoted\"", wantKey: "KEY", wantValue: "quoted", wantOK: true},
		{name: "comment", line: "# comment", wantOK: false},
		{name: "blank", line: "  ", wantOK: false},
		{name: "missing equals", line: "KEY", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotKey, gotValue, gotOK := parseDotEnvLine(tt.line)
			if gotKey != tt.wantKey || gotValue != tt.wantValue || gotOK != tt.wantOK {
				t.Fatalf("parseDotEnvLine(%q) = (%q, %q, %t), want (%q, %q, %t)", tt.line, gotKey, gotValue, gotOK, tt.wantKey, tt.wantValue, tt.wantOK)
			}
		})
	}
}

func TestTrimMatchingQuotes(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "double quotes", value: "\"value\"", want: "value"},
		{name: "single quotes", value: "'value'", want: "value"},
		{name: "mismatched", value: "\"value'", want: "\"value'"},
		{name: "short", value: "a", want: "a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := trimMatchingQuotes(tt.value); got != tt.want {
				t.Fatalf("trimMatchingQuotes(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestParseHumanDuration(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		want      time.Duration
		wantError string
	}{
		{name: "empty", input: "", want: 0},
		{name: "minutes", input: "10m", want: 10 * time.Minute},
		{name: "hours", input: "1h", want: time.Hour},
		{name: "seconds", input: "30s", want: 30 * time.Second},
		{name: "days", input: "3d", want: 72 * time.Hour},
		{name: "zero", input: "0", wantError: "must be positive"},
		{name: "negative", input: "-1h", wantError: "must be positive"},
		{name: "overflow days", input: fmt.Sprintf("%dd", int64(time.Duration(1<<63-1)/(24*time.Hour))+1), wantError: "exceeds maximum supported duration"},
		{name: "invalid", input: "tomorrow", wantError: "expected a duration"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseHumanDuration(tt.input)
			if tt.wantError != "" {
				if err == nil {
					t.Fatal("ParseHumanDuration() error = nil, want error")
				}
				if !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("ParseHumanDuration() error = %q, want substring %q", err.Error(), tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseHumanDuration() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("ParseHumanDuration(%q) = %s, want %s", tt.input, got, tt.want)
			}
		})
	}
}

func resetConfigEnvForTest(t *testing.T) {
	t.Helper()
	for _, name := range configEnvNames {
		if value, ok := os.LookupEnv(name); ok {
			t.Setenv(name, value)
		}
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("Unsetenv(%q) error = %v", name, err)
		}
	}
}

func useTempWorkingDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func TestLoadDotEnvScannerErrorType(t *testing.T) {
	resetConfigEnvForTest(t)
	path := filepath.Join(t.TempDir(), ".env")
	writeFile(t, path, fmt.Sprintf("PLEX_URL=%s\n", strings.Repeat("b", 70*1024)))

	err := loadDotEnv(path)
	if err == nil {
		t.Fatal("loadDotEnv() error = nil, want error")
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("loadDotEnv() error = %v, want scanner error", err)
	}
}
