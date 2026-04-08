package radarr

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"golift.io/starr"
	starrradarr "golift.io/starr/radarr"
)

var ErrMovieNotFound = errors.New("movie not found")

type MovieMatch struct {
	Movie     *starrradarr.Movie
	MatchedBy string
}

// Client wraps the golift/starr Radarr client.
type Client struct {
	api *starrradarr.Radarr
}

// New creates a Radarr client for the given server URL and API key.
func New(url, apiKey string) *Client {
	cfg := starr.New(apiKey, url, 30*time.Second)
	return &Client{api: starrradarr.New(cfg)}
}

// FindMovie locates an existing Radarr movie by TMDB ID when available and
// falls back to matching an existing library entry by title.
func (c *Client) FindMovie(ctx context.Context, title string, tmdbID int64) (*MovieMatch, error) {
	if c == nil || c.api == nil {
		return nil, errors.New("radarr client is not configured")
	}

	if tmdbID > 0 {
		match, err := c.findMovieByTMDB(ctx, tmdbID)
		if err != nil {
			return nil, err
		}
		if match != nil {
			return match, nil
		}
	}

	if strings.TrimSpace(title) == "" {
		return nil, ErrMovieNotFound
	}

	return c.findMovieByTitle(ctx, title)
}

func (c *Client) findMovieByTMDB(ctx context.Context, tmdbID int64) (*MovieMatch, error) {
	movies, err := c.api.GetMovieContext(ctx, &starrradarr.GetMovie{TMDBID: tmdbID})
	if err != nil {
		return nil, fmt.Errorf("looking up radarr movie by tmdb id %d: %w", tmdbID, err)
	}

	for _, candidate := range movies {
		if candidate != nil && candidate.TmdbID == tmdbID {
			return &MovieMatch{Movie: candidate, MatchedBy: "tmdb"}, nil
		}
	}

	return nil, nil
}

func (c *Client) findMovieByTitle(ctx context.Context, title string) (*MovieMatch, error) {
	movies, err := c.api.GetMovieContext(ctx, &starrradarr.GetMovie{})
	if err != nil {
		return nil, fmt.Errorf("listing radarr movies: %w", err)
	}

	normalizedTitle := normalizeTitle(title)
	for _, candidate := range movies {
		if candidate == nil {
			continue
		}
		if titlesMatch(candidate, title, normalizedTitle) {
			return &MovieMatch{Movie: candidate, MatchedBy: "title"}, nil
		}
	}

	return nil, ErrMovieNotFound
}

func normalizeTitle(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	var builder strings.Builder
	builder.Grow(len(value))
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func titlesMatch(candidate *starrradarr.Movie, title, normalizedTitle string) bool {
	return strings.EqualFold(candidate.Title, title) || normalizeTitle(candidate.Title) == normalizedTitle
}
