package sonarr

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golift.io/starr"
	starrsonarr "golift.io/starr/sonarr"
)

var ErrSeriesNotFound = errors.New("series not found")

const errSonarrClientNotConfigured = "sonarr client is not configured"

const commandSeasonSearch = "SeasonSearch"

type AddSeriesDefaults struct {
	RootFolderPath     string
	QualityProfileID   int64
	QualityProfileName string
}

type CreateSeriesRequest struct {
	Title                    string
	TVDBID                   int64
	MonitorAll               bool
	MonitoredSeasons         []int
	SearchForMissingEpisodes bool
}

type SeriesMatch struct {
	Series    *starrsonarr.Series
	MatchedBy string
}

type sonarrAPI interface {
	AddSeriesContext(ctx context.Context, series *starrsonarr.AddSeriesInput) (*starrsonarr.Series, error)
	UpdateSeriesContext(ctx context.Context, series *starrsonarr.AddSeriesInput, moveFiles bool) (*starrsonarr.Series, error)
	SendCommandContext(ctx context.Context, cmd *starrsonarr.CommandRequest) (*starrsonarr.CommandResponse, error)
	GetSeriesContext(ctx context.Context, tvdbID int64) ([]*starrsonarr.Series, error)
	GetAllSeriesContext(ctx context.Context) ([]*starrsonarr.Series, error)
	GetSeriesLookupContext(ctx context.Context, term string, tvdbID int64) ([]*starrsonarr.Series, error)
	GetRootFoldersContext(ctx context.Context) ([]*starrsonarr.RootFolder, error)
	GetQualityProfilesContext(ctx context.Context) ([]*starrsonarr.QualityProfile, error)
}

// Client wraps the golift/starr Sonarr client.
type Client struct {
	api sonarrAPI
}

// New creates a Sonarr client for the given server URL and API key.
func New(url, apiKey string) *Client {
	cfg := starr.New(apiKey, url, 30*time.Second)
	return &Client{api: starrsonarr.New(cfg)}
}

// FindSeries locates an existing Sonarr series by TVDB ID when available and
// falls back to matching an existing library entry by title.
func (c *Client) FindSeries(ctx context.Context, title string, tvdbID int64) (*SeriesMatch, error) {
	if c == nil || c.api == nil {
		return nil, errors.New(errSonarrClientNotConfigured)
	}

	if tvdbID > 0 {
		match, err := c.findSeriesByTVDB(ctx, tvdbID)
		if err != nil {
			return nil, err
		}
		if match != nil {
			return match, nil
		}
	}

	if strings.TrimSpace(title) == "" {
		return nil, ErrSeriesNotFound
	}

	return c.findSeriesByTitle(ctx, title)
}

func (c *Client) ResolveAddSeriesDefaults(ctx context.Context, rootFolderSelector, qualityProfileSelector string) (AddSeriesDefaults, error) {
	if c == nil || c.api == nil {
		return AddSeriesDefaults{}, errors.New(errSonarrClientNotConfigured)
	}

	rootFolder, err := c.resolveRootFolder(ctx, rootFolderSelector)
	if err != nil {
		return AddSeriesDefaults{}, err
	}

	qualityProfile, err := c.resolveQualityProfile(ctx, qualityProfileSelector)
	if err != nil {
		return AddSeriesDefaults{}, err
	}

	return AddSeriesDefaults{
		RootFolderPath:     rootFolder.Path,
		QualityProfileID:   qualityProfile.ID,
		QualityProfileName: qualityProfile.Name,
	}, nil
}

func (c *Client) CreateSeries(ctx context.Context, request CreateSeriesRequest, defaults AddSeriesDefaults) (*starrsonarr.Series, error) {
	if c == nil || c.api == nil {
		return nil, errors.New(errSonarrClientNotConfigured)
	}
	if request.TVDBID == 0 {
		return nil, errors.New("tvdb id is required to add a Sonarr series")
	}

	candidate, err := c.lookupSeriesCandidate(ctx, request.Title, request.TVDBID)
	if err != nil {
		return nil, err
	}

	input, err := buildAddSeriesInput(candidate, request, defaults)
	if err != nil {
		return nil, err
	}

	series, err := c.api.AddSeriesContext(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("adding Sonarr series %q: %w", candidate.Title, err)
	}

	return series, nil
}

func (c *Client) PreviewCreateSeries(ctx context.Context, request CreateSeriesRequest, defaults AddSeriesDefaults) (string, error) {
	if c == nil || c.api == nil {
		return "", errors.New(errSonarrClientNotConfigured)
	}
	if request.TVDBID == 0 {
		return "", errors.New("tvdb id is required to add a Sonarr series")
	}

	candidate, err := c.lookupSeriesCandidate(ctx, request.Title, request.TVDBID)
	if err != nil {
		return "", err
	}
	if _, err := buildAddSeriesInput(candidate, request, defaults); err != nil {
		return "", err
	}

	return candidate.Title, nil
}

func (c *Client) UpdateSeriesMonitoring(ctx context.Context, series *starrsonarr.Series, request CreateSeriesRequest) (*starrsonarr.Series, bool, error) {
	if c == nil || c.api == nil {
		return nil, false, errors.New(errSonarrClientNotConfigured)
	}
	if series == nil {
		return nil, false, errors.New("series is required to update Sonarr monitoring")
	}

	input, changed, err := buildUpdateSeriesInput(series, request)
	if err != nil {
		return nil, false, err
	}
	if !changed {
		return series, false, nil
	}

	updated, err := c.api.UpdateSeriesContext(ctx, input, false)
	if err != nil {
		return nil, false, fmt.Errorf("updating Sonarr series %q: %w", series.Title, err)
	}

	return updated, true, nil
}

func (c *Client) PreviewUpdateSeriesMonitoring(series *starrsonarr.Series, request CreateSeriesRequest) (bool, error) {
	if c == nil || c.api == nil {
		return false, errors.New(errSonarrClientNotConfigured)
	}
	if series == nil {
		return false, errors.New("series is required to update Sonarr monitoring")
	}

	_, changed, err := buildUpdateSeriesInput(series, request)
	if err != nil {
		return false, err
	}

	return changed, nil
}

func (c *Client) SearchSeason(ctx context.Context, seriesID int64, seasonNumber int) error {
	if c == nil || c.api == nil {
		return errors.New(errSonarrClientNotConfigured)
	}
	if seriesID == 0 {
		return errors.New("series id is required to queue a Sonarr season search")
	}
	if seasonNumber < 0 {
		return errors.New("season number must be zero or greater")
	}

	_, err := c.api.SendCommandContext(ctx, &starrsonarr.CommandRequest{
		Name:         commandSeasonSearch,
		SeriesID:     seriesID,
		SeasonNumber: seasonNumber,
	})
	if err != nil {
		return fmt.Errorf("queueing Sonarr season %d search for series %d: %w", seasonNumber, seriesID, err)
	}

	return nil
}

func (c *Client) findSeriesByTVDB(ctx context.Context, tvdbID int64) (*SeriesMatch, error) {
	series, err := c.api.GetSeriesContext(ctx, tvdbID)
	if err != nil {
		return nil, fmt.Errorf("looking up sonarr series by tvdb id %d: %w", tvdbID, err)
	}

	for _, candidate := range series {
		if candidate != nil && candidate.TvdbID == tvdbID {
			return &SeriesMatch{Series: candidate, MatchedBy: "tvdb"}, nil
		}
	}

	return nil, nil
}

func (c *Client) findSeriesByTitle(ctx context.Context, title string) (*SeriesMatch, error) {
	series, err := c.api.GetAllSeriesContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing sonarr series: %w", err)
	}

	normalizedTitle := normalizeTitle(title)
	for _, candidate := range series {
		if candidate == nil {
			continue
		}
		if titlesMatch(candidate, title, normalizedTitle) {
			return &SeriesMatch{Series: candidate, MatchedBy: "title"}, nil
		}
	}

	return nil, ErrSeriesNotFound
}

func (c *Client) lookupSeriesCandidate(ctx context.Context, title string, tvdbID int64) (*starrsonarr.Series, error) {
	lookup, err := c.api.GetSeriesLookupContext(ctx, title, tvdbID)
	if err != nil {
		return nil, fmt.Errorf("looking up Sonarr series candidate for %q: %w", title, err)
	}

	for _, candidate := range lookup {
		if candidate == nil {
			continue
		}
		if tvdbID > 0 && candidate.TvdbID == tvdbID {
			return candidate, nil
		}
		if tvdbID == 0 && titlesMatch(candidate, title, normalizeTitle(title)) {
			return candidate, nil
		}
	}

	if tvdbID > 0 {
		return nil, fmt.Errorf("no Sonarr lookup result found for tvdb id %d", tvdbID)
	}
	return nil, fmt.Errorf("no Sonarr lookup result found for %q", title)
}

func buildAddSeriesInput(candidate *starrsonarr.Series, request CreateSeriesRequest, defaults AddSeriesDefaults) (*starrsonarr.AddSeriesInput, error) {
	input := &starrsonarr.AddSeriesInput{
		Monitored:         true,
		SeasonFolder:      candidate.SeasonFolder,
		UseSceneNumbering: candidate.UseSceneNumbering,
		LanguageProfileID: candidate.LanguageProfileID,
		QualityProfileID:  defaults.QualityProfileID,
		TvdbID:            candidate.TvdbID,
		ImdbID:            candidate.ImdbID,
		TvMazeID:          candidate.TvMazeID,
		TvRageID:          candidate.TvRageID,
		SeriesType:        candidate.SeriesType,
		Title:             candidate.Title,
		TitleSlug:         candidate.TitleSlug,
		RootFolderPath:    defaults.RootFolderPath,
		Images:            candidate.Images,
		AddOptions: &starrsonarr.AddSeriesOptions{
			SearchForMissingEpisodes: request.SearchForMissingEpisodes,
		},
	}

	if request.MonitorAll {
		input.AddOptions.Monitor = starrsonarr.MonitorAll
		return input, nil
	}

	seasons, err := buildMonitoredSeasons(candidate.Seasons, request.MonitoredSeasons)
	if err != nil {
		return nil, err
	}
	input.Seasons = seasons
	input.AddOptions.Monitor = starrsonarr.MonitorSkip
	return input, nil
}

func buildUpdateSeriesInput(series *starrsonarr.Series, request CreateSeriesRequest) (*starrsonarr.AddSeriesInput, bool, error) {
	seasons, changed, err := buildUpdatedSeasons(series.Seasons, request)
	if err != nil {
		return nil, false, err
	}

	monitored := series.Monitored || request.MonitorAll || len(request.MonitoredSeasons) > 0
	if monitored != series.Monitored {
		changed = true
	}

	input := &starrsonarr.AddSeriesInput{
		ID:                series.ID,
		Monitored:         monitored,
		SeasonFolder:      series.SeasonFolder,
		UseSceneNumbering: series.UseSceneNumbering,
		LanguageProfileID: series.LanguageProfileID,
		QualityProfileID:  series.QualityProfileID,
		TvdbID:            series.TvdbID,
		ImdbID:            series.ImdbID,
		TvMazeID:          series.TvMazeID,
		TvRageID:          series.TvRageID,
		Path:              series.Path,
		SeriesType:        series.SeriesType,
		Title:             series.Title,
		TitleSlug:         series.TitleSlug,
		RootFolderPath:    series.RootFolderPath,
		Tags:              series.Tags,
		Seasons:           seasons,
		Images:            series.Images,
	}

	return input, changed, nil
}

func buildMonitoredSeasons(candidateSeasons []*starrsonarr.Season, monitoredSeasons []int) ([]*starrsonarr.Season, error) {
	if len(candidateSeasons) == 0 {
		return nil, errors.New("sonarr lookup returned no seasons")
	}

	selected := make(map[int]struct{}, len(monitoredSeasons))
	for _, seasonNumber := range monitoredSeasons {
		selected[seasonNumber] = struct{}{}
	}

	found := make(map[int]struct{}, len(monitoredSeasons))
	seasons := make([]*starrsonarr.Season, 0, len(candidateSeasons))
	for _, candidateSeason := range candidateSeasons {
		if candidateSeason == nil {
			continue
		}
		_, monitored := selected[candidateSeason.SeasonNumber]
		if monitored {
			found[candidateSeason.SeasonNumber] = struct{}{}
		}
		seasons = append(seasons, &starrsonarr.Season{
			SeasonNumber: candidateSeason.SeasonNumber,
			Monitored:    monitored,
		})
	}

	var missing []int
	for seasonNumber := range selected {
		if _, ok := found[seasonNumber]; !ok {
			missing = append(missing, seasonNumber)
		}
	}
	if len(missing) > 0 {
		sort.Ints(missing)
		return nil, fmt.Errorf("sonarr lookup did not include requested seasons %s", joinSeasonNumbers(missing))
	}

	return seasons, nil
}

func buildUpdatedSeasons(existingSeasons []*starrsonarr.Season, request CreateSeriesRequest) ([]*starrsonarr.Season, bool, error) {
	if len(existingSeasons) == 0 {
		return nil, false, errors.New("sonarr series has no seasons to update")
	}

	selected := make(map[int]struct{}, len(request.MonitoredSeasons))
	for _, seasonNumber := range request.MonitoredSeasons {
		selected[seasonNumber] = struct{}{}
	}

	found := make(map[int]struct{}, len(request.MonitoredSeasons))
	seasons := make([]*starrsonarr.Season, 0, len(existingSeasons))
	changed := false
	for _, existingSeason := range existingSeasons {
		if existingSeason == nil {
			continue
		}

		monitored := existingSeason.Monitored
		if request.MonitorAll {
			monitored = true
		} else if _, ok := selected[existingSeason.SeasonNumber]; ok {
			monitored = true
			found[existingSeason.SeasonNumber] = struct{}{}
		}

		if monitored != existingSeason.Monitored {
			changed = true
		}
		seasons = append(seasons, &starrsonarr.Season{
			SeasonNumber: existingSeason.SeasonNumber,
			Monitored:    monitored,
		})
	}

	var missing []int
	for seasonNumber := range selected {
		if _, ok := found[seasonNumber]; !ok {
			missing = append(missing, seasonNumber)
		}
	}
	if len(missing) > 0 {
		sort.Ints(missing)
		return nil, false, fmt.Errorf("sonarr series did not include requested seasons %s", joinSeasonNumbers(missing))
	}

	return seasons, changed, nil
}

func (c *Client) resolveRootFolder(ctx context.Context, selector string) (*starrsonarr.RootFolder, error) {
	rootFolders, err := c.api.GetRootFoldersContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing Sonarr root folders: %w", err)
	}

	accessible := make([]*starrsonarr.RootFolder, 0, len(rootFolders))
	for _, rootFolder := range rootFolders {
		if rootFolder != nil && rootFolder.Accessible && rootFolder.Path != "" {
			accessible = append(accessible, rootFolder)
		}
	}
	if len(accessible) == 0 {
		return nil, errors.New("no accessible Sonarr root folders available")
	}

	selector = strings.TrimSpace(selector)
	if selector == "" {
		if len(accessible) == 1 {
			return accessible[0], nil
		}
		return nil, fmt.Errorf("multiple Sonarr root folders available; set SONARR_ROOT_FOLDER to one of: %s", joinRootFolderPaths(accessible))
	}

	if rootFolder, ok := matchRootFolder(accessible, selector); ok {
		return rootFolder, nil
	}

	return nil, fmt.Errorf("sonarr root folder %q not found; available options: %s", selector, joinRootFolderPaths(accessible))
}

func (c *Client) resolveQualityProfile(ctx context.Context, selector string) (*starrsonarr.QualityProfile, error) {
	profiles, err := c.api.GetQualityProfilesContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing Sonarr quality profiles: %w", err)
	}
	if len(profiles) == 0 {
		return nil, errors.New("no Sonarr quality profiles available")
	}

	selector = strings.TrimSpace(selector)
	if selector == "" {
		if len(profiles) == 1 {
			return profiles[0], nil
		}
		return nil, fmt.Errorf("multiple Sonarr quality profiles available; set SONARR_QUALITY_PROFILE to one of: %s", joinQualityProfileNames(profiles))
	}

	if profile, ok := matchQualityProfile(profiles, selector); ok {
		return profile, nil
	}

	return nil, fmt.Errorf("sonarr quality profile %q not found; available options: %s", selector, joinQualityProfileNames(profiles))
}

// FindSeason returns the season metadata for the requested season number.
func FindSeason(series *starrsonarr.Series, seasonNumber int) (*starrsonarr.Season, bool) {
	if series == nil {
		return nil, false
	}

	for _, season := range series.Seasons {
		if season != nil && season.SeasonNumber == seasonNumber {
			return season, true
		}
	}

	return nil, false
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

func titlesMatch(candidate *starrsonarr.Series, title, normalizedTitle string) bool {
	return strings.EqualFold(candidate.Title, title) || normalizeTitle(candidate.Title) == normalizedTitle || normalizeTitle(candidate.CleanTitle) == normalizedTitle
}

func matchRootFolder(rootFolders []*starrsonarr.RootFolder, selector string) (*starrsonarr.RootFolder, bool) {
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

func matchQualityProfile(profiles []*starrsonarr.QualityProfile, selector string) (*starrsonarr.QualityProfile, bool) {
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

func joinRootFolderPaths(rootFolders []*starrsonarr.RootFolder) string {
	paths := make([]string, 0, len(rootFolders))
	for _, rootFolder := range rootFolders {
		if rootFolder != nil {
			paths = append(paths, rootFolder.Path)
		}
	}
	return strings.Join(paths, ", ")
}

func joinQualityProfileNames(profiles []*starrsonarr.QualityProfile) string {
	names := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		if profile != nil {
			names = append(names, profile.Name)
		}
	}
	return strings.Join(names, ", ")
}

func joinSeasonNumbers(seasonNumbers []int) string {
	parts := make([]string, 0, len(seasonNumbers))
	for _, seasonNumber := range seasonNumbers {
		parts = append(parts, strconv.Itoa(seasonNumber))
	}
	return strings.Join(parts, ", ")
}
