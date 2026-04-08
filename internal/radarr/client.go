package radarr

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golift.io/starr"
	starrradarr "golift.io/starr/radarr"
)

var ErrMovieNotFound = errors.New("movie not found")

const errRadarrClientNotConfigured = "radarr client is not configured"

const commandMovieSearch = "MoviesSearch"

type AddMovieDefaults struct {
	RootFolderPath     string
	QualityProfileID   int64
	QualityProfileName string
}

type CreateMovieRequest struct {
	Title          string
	TMDBID         int64
	SearchForMovie bool
}

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
		return nil, errors.New(errRadarrClientNotConfigured)
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

func (c *Client) ResolveAddMovieDefaults(ctx context.Context, rootFolderSelector, qualityProfileSelector string) (AddMovieDefaults, error) {
	if c == nil || c.api == nil {
		return AddMovieDefaults{}, errors.New(errRadarrClientNotConfigured)
	}

	rootFolder, err := c.resolveRootFolder(ctx, rootFolderSelector)
	if err != nil {
		return AddMovieDefaults{}, err
	}

	qualityProfile, err := c.resolveQualityProfile(ctx, qualityProfileSelector)
	if err != nil {
		return AddMovieDefaults{}, err
	}

	return AddMovieDefaults{
		RootFolderPath:     rootFolder.Path,
		QualityProfileID:   qualityProfile.ID,
		QualityProfileName: qualityProfile.Name,
	}, nil
}

func (c *Client) CreateMovie(ctx context.Context, request CreateMovieRequest, defaults AddMovieDefaults) (*starrradarr.Movie, error) {
	if c == nil || c.api == nil {
		return nil, errors.New(errRadarrClientNotConfigured)
	}
	if request.TMDBID == 0 {
		return nil, errors.New("tmdb id is required to add a Radarr movie")
	}

	candidate, err := c.lookupMovieCandidate(ctx, request.Title, request.TMDBID)
	if err != nil {
		return nil, err
	}

	input := &starrradarr.AddMovieInput{
		Title:               candidate.Title,
		TitleSlug:           candidate.TitleSlug,
		MinimumAvailability: candidate.MinimumAvailability,
		RootFolderPath:      defaults.RootFolderPath,
		TmdbID:              candidate.TmdbID,
		QualityProfileID:    defaults.QualityProfileID,
		Year:                candidate.Year,
		Images:              candidate.Images,
		Tags:                candidate.Tags,
		Monitored:           true,
		AddOptions: &starrradarr.AddMovieOptions{
			SearchForMovie: request.SearchForMovie,
		},
	}

	movie, err := c.api.AddMovieContext(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("adding Radarr movie %q: %w", candidate.Title, err)
	}

	return movie, nil
}

func (c *Client) PreviewCreateMovie(ctx context.Context, request CreateMovieRequest, defaults AddMovieDefaults) (string, error) {
	if c == nil || c.api == nil {
		return "", errors.New(errRadarrClientNotConfigured)
	}
	if request.TMDBID == 0 {
		return "", errors.New("tmdb id is required to add a Radarr movie")
	}

	candidate, err := c.lookupMovieCandidate(ctx, request.Title, request.TMDBID)
	if err != nil {
		return "", err
	}
	if defaults.RootFolderPath == "" {
		return "", errors.New("radarr root folder path is required")
	}
	if defaults.QualityProfileID == 0 {
		return "", errors.New("radarr quality profile id is required")
	}

	return candidate.Title, nil
}

func (c *Client) UpdateMovieMonitoring(ctx context.Context, movie *starrradarr.Movie, monitored bool) (*starrradarr.Movie, bool, error) {
	if c == nil || c.api == nil {
		return nil, false, errors.New(errRadarrClientNotConfigured)
	}
	if movie == nil {
		return nil, false, errors.New("movie is required to update Radarr monitoring")
	}
	if movie.Monitored == monitored {
		return movie, false, nil
	}

	storedMovie, err := c.api.GetMovieByIDContext(ctx, movie.ID)
	if err != nil {
		return nil, false, fmt.Errorf("getting Radarr movie %d: %w", movie.ID, err)
	}
	storedMovie.Monitored = monitored

	updated, err := c.api.UpdateMovieContext(ctx, storedMovie.ID, storedMovie, false)
	if err != nil {
		return nil, false, fmt.Errorf("updating Radarr movie %q: %w", movie.Title, err)
	}

	return updated, true, nil
}

func (c *Client) PreviewUpdateMovieMonitoring(movie *starrradarr.Movie, monitored bool) (bool, error) {
	if c == nil || c.api == nil {
		return false, errors.New(errRadarrClientNotConfigured)
	}
	if movie == nil {
		return false, errors.New("movie is required to update Radarr monitoring")
	}

	return movie.Monitored != monitored, nil
}

func (c *Client) SearchMovie(ctx context.Context, movieID int64) error {
	if c == nil || c.api == nil {
		return errors.New(errRadarrClientNotConfigured)
	}
	if movieID == 0 {
		return errors.New("movie id is required to queue a Radarr movie search")
	}

	_, err := c.api.SendCommandContext(ctx, &starrradarr.CommandRequest{
		Name:     commandMovieSearch,
		MovieIDs: []int64{movieID},
	})
	if err != nil {
		return fmt.Errorf("queueing Radarr search for movie %d: %w", movieID, err)
	}

	return nil
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

func (c *Client) lookupMovieCandidate(ctx context.Context, title string, tmdbID int64) (*starrradarr.Movie, error) {
	if tmdbID > 0 {
		candidate, err := c.api.LookupTMDBContext(ctx, tmdbID)
		if err != nil {
			return nil, fmt.Errorf("looking up Radarr movie candidate for tmdb id %d: %w", tmdbID, err)
		}
		if candidate != nil && candidate.TmdbID == tmdbID {
			return candidate, nil
		}
		return nil, fmt.Errorf("no Radarr lookup result found for tmdb id %d", tmdbID)
	}

	lookup, err := c.api.LookupContext(ctx, title)
	if err != nil {
		return nil, fmt.Errorf("looking up Radarr movie candidate for %q: %w", title, err)
	}

	normalizedTitle := normalizeTitle(title)
	for _, candidate := range lookup {
		if candidate != nil && titlesMatch(candidate, title, normalizedTitle) {
			return candidate, nil
		}
	}

	return nil, fmt.Errorf("no Radarr lookup result found for %q", title)
}

func (c *Client) resolveRootFolder(ctx context.Context, selector string) (*starrradarr.RootFolder, error) {
	rootFolders, err := c.api.GetRootFoldersContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing Radarr root folders: %w", err)
	}

	accessible := make([]*starrradarr.RootFolder, 0, len(rootFolders))
	for _, rootFolder := range rootFolders {
		if rootFolder != nil && rootFolder.Accessible && rootFolder.Path != "" {
			accessible = append(accessible, rootFolder)
		}
	}
	if len(accessible) == 0 {
		return nil, errors.New("no accessible Radarr root folders available")
	}

	selector = strings.TrimSpace(selector)
	if selector == "" {
		if len(accessible) == 1 {
			return accessible[0], nil
		}
		return nil, fmt.Errorf("multiple Radarr root folders available; set RADARR_ROOT_FOLDER to one of: %s", joinRootFolderPaths(accessible))
	}

	if rootFolder, ok := matchRootFolder(accessible, selector); ok {
		return rootFolder, nil
	}

	return nil, fmt.Errorf("Radarr root folder %q not found; available options: %s", selector, joinRootFolderPaths(accessible))
}

func (c *Client) resolveQualityProfile(ctx context.Context, selector string) (*starrradarr.QualityProfile, error) {
	profiles, err := c.api.GetQualityProfilesContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing Radarr quality profiles: %w", err)
	}
	if len(profiles) == 0 {
		return nil, errors.New("no Radarr quality profiles available")
	}

	selector = strings.TrimSpace(selector)
	if selector == "" {
		if len(profiles) == 1 {
			return profiles[0], nil
		}
		return nil, fmt.Errorf("multiple Radarr quality profiles available; set RADARR_QUALITY_PROFILE to one of: %s", joinQualityProfileNames(profiles))
	}

	if profile, ok := matchQualityProfile(profiles, selector); ok {
		return profile, nil
	}

	return nil, fmt.Errorf("Radarr quality profile %q not found; available options: %s", selector, joinQualityProfileNames(profiles))
}

func matchRootFolder(rootFolders []*starrradarr.RootFolder, selector string) (*starrradarr.RootFolder, bool) {
	if id, err := strconv.ParseInt(selector, 10, 64); err == nil {
		for _, rootFolder := range rootFolders {
			if rootFolder != nil && rootFolder.ID == id {
				return rootFolder, true
			}
		}
	}

	for _, rootFolder := range rootFolders {
		if rootFolder != nil && rootFolder.Path == selector {
			return rootFolder, true
		}
	}

	return nil, false
}

func matchQualityProfile(profiles []*starrradarr.QualityProfile, selector string) (*starrradarr.QualityProfile, bool) {
	if id, err := strconv.ParseInt(selector, 10, 64); err == nil {
		for _, profile := range profiles {
			if profile != nil && profile.ID == id {
				return profile, true
			}
		}
	}

	for _, profile := range profiles {
		if profile != nil && strings.EqualFold(profile.Name, selector) {
			return profile, true
		}
	}

	return nil, false
}

func joinRootFolderPaths(rootFolders []*starrradarr.RootFolder) string {
	paths := make([]string, 0, len(rootFolders))
	for _, rootFolder := range rootFolders {
		if rootFolder != nil {
			paths = append(paths, rootFolder.Path)
		}
	}
	return strings.Join(paths, ", ")
}

func joinQualityProfileNames(profiles []*starrradarr.QualityProfile) string {
	names := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		if profile != nil {
			names = append(names, profile.Name)
		}
	}
	return strings.Join(names, ", ")
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
