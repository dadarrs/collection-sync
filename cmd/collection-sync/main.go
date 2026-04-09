package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/alecthomas/kong"
	starrradarr "golift.io/starr/radarr"
	starrsonarr "golift.io/starr/sonarr"

	"github.com/dadarrs/collection-sync/internal/config"
	"github.com/dadarrs/collection-sync/internal/plex"
	"github.com/dadarrs/collection-sync/internal/radarr"
	"github.com/dadarrs/collection-sync/internal/sonarr"
)

const (
	statusCountFormat   = "%s: %d\n"
	totalMoviesFormat   = "\nTotal: %d movies\n"
	statusAdded         = "added"
	statusExisting      = "existing"
	statusFailed        = "failed"
	statusPresent       = "present"
	statusMissingMovie  = "missing-movie"
	statusSkipped       = "skipped"
	statusMissingSeries = "missing-series"
	statusMissingSeason = "missing-season"
	statusUnmonitored   = "unmonitored"
	statusUpdated       = "updated"
	statusWouldAdd      = "would-add"
	statusWouldUpdate   = "would-update"
)

type CLI struct {
	Run    RunCmd    `cmd:"" help:"Sync TV and movie collections. Repeats on INTERVAL if set."`
	TV     TVCmd     `cmd:"" help:"TV show and season operations."`
	Movies MoviesCmd `cmd:"" help:"Movie operations."`
}

type TVCmd struct {
	List  ListTVCmd  `cmd:"" help:"List TV shows/seasons in the TV collection."`
	Check CheckTVCmd `cmd:"" help:"Check TV shows/seasons from Plex against Sonarr."`
	Sync  SyncTVCmd  `cmd:"" help:"Add missing TV shows from Plex into Sonarr."`
}

type MoviesCmd struct {
	List  ListMoviesCmd  `cmd:"" help:"List movies in the movie collection."`
	Check CheckMoviesCmd `cmd:"" help:"Check movies from Plex against Radarr."`
	Sync  SyncMoviesCmd  `cmd:"" help:"Add missing movies from Plex into Radarr."`
}

type RunCmd struct {
	DryRun bool `help:"Preview sync changes without modifying Sonarr or Radarr."`
}

type plexService interface {
	FindCollectionByName(ctx context.Context, name string) (string, error)
	GetCollectionItems(ctx context.Context, collectionKey string) ([]plex.Item, error)
}

type sonarrService interface {
	FindSeries(ctx context.Context, title string, tvdbID int64) (*sonarr.SeriesMatch, error)
	ResolveAddSeriesDefaults(ctx context.Context, rootFolderSelector, qualityProfileSelector string) (sonarr.AddSeriesDefaults, error)
	PreviewCreateSeries(ctx context.Context, request sonarr.CreateSeriesRequest, defaults sonarr.AddSeriesDefaults) (string, error)
	CreateSeries(ctx context.Context, request sonarr.CreateSeriesRequest, defaults sonarr.AddSeriesDefaults) (*starrsonarr.Series, error)
	PreviewUpdateSeriesMonitoring(series *starrsonarr.Series, request sonarr.CreateSeriesRequest) (bool, error)
	UpdateSeriesMonitoring(ctx context.Context, series *starrsonarr.Series, request sonarr.CreateSeriesRequest) (*starrsonarr.Series, bool, error)
	SearchSeason(ctx context.Context, seriesID int64, seasonNumber int) error
}

type radarrService interface {
	FindMovie(ctx context.Context, title string, tmdbID int64) (*radarr.MovieMatch, error)
	ResolveAddMovieDefaults(ctx context.Context, rootFolderSelector, qualityProfileSelector string) (radarr.AddMovieDefaults, error)
	PreviewCreateMovie(ctx context.Context, request radarr.CreateMovieRequest, defaults radarr.AddMovieDefaults) (string, error)
	CreateMovie(ctx context.Context, request radarr.CreateMovieRequest, defaults radarr.AddMovieDefaults) (*starrradarr.Movie, error)
	PreviewUpdateMovieMonitoring(movie *starrradarr.Movie, monitored bool) (bool, error)
	UpdateMovieMonitoring(ctx context.Context, movie *starrradarr.Movie, monitored bool) (*starrradarr.Movie, bool, error)
	SearchMovie(ctx context.Context, movieID int64) error
}

func (c *RunCmd) Run(d *deps) error {
	tv := d.canSyncTV()
	movies := d.canSyncMovies()
	if !tv && !movies {
		return fmt.Errorf("nothing to sync: set PLEX_TV_COLLECTION + SONARR_URL + SONARR_API_KEY for TV, or PLEX_MOVIE_COLLECTION + RADARR_URL + RADARR_API_KEY for movies")
	}

	interval, err := config.ParseHumanDuration(d.cfg.Interval)
	if err != nil {
		return err
	}

	if c.DryRun {
		d.println("[dry-run] previewing changes only")
	}

	printSyncTargets(d.output(), tv, movies)

	if interval == 0 {
		return d.syncAll(tv, movies, c.DryRun)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	d.printf("interval: %s (next run: %s)\n\n", interval, time.Now().Add(interval).Format(time.DateTime))

	if err := d.syncAll(tv, movies, c.DryRun); err != nil {
		d.errorf("sync error: %v\n", err)
	}

	return c.runContinuously(ctx, d, tv, movies, interval, ticker.C)
}

func (c *RunCmd) runContinuously(ctx context.Context, d *deps, tv, movies bool, interval time.Duration, ticks <-chan time.Time) error {
	for {
		select {
		case <-ctx.Done():
			d.println("\nshutting down")
			return nil
		case tick := <-ticks:
			d.printf("\n--- sync started at %s ---\n\n", time.Now().Format(time.DateTime))
			if err := d.syncAll(tv, movies, c.DryRun); err != nil {
				d.errorf("sync error: %v\n", err)
			}
			d.printf("\nnext run: %s\n", tick.Add(interval).Format(time.DateTime))
		}
	}
}

func printSyncTargets(w io.Writer, tv, movies bool) {
	var targets []string
	if tv {
		targets = append(targets, "tv")
	}
	if movies {
		targets = append(targets, "movies")
	}
	_, _ = fmt.Fprintf(w, "sync targets: %s\n", strings.Join(targets, ", "))
}

func (d *deps) canSyncTV() bool {
	return d.cfg.TVCollectionName != "" && d.cfg.SonarrURL != "" && d.cfg.SonarrAPIKey != ""
}

func (d *deps) canSyncMovies() bool {
	return d.cfg.MovieCollectionName != "" && d.cfg.RadarrURL != "" && d.cfg.RadarrAPIKey != ""
}

func (d *deps) syncAll(tv, movies, dryRun bool) error {
	var tvErr, movieErr error

	if tv {
		d.println("=== TV Sync ===")
		cmd := &SyncTVCmd{DryRun: dryRun}
		if err := cmd.Run(d); err != nil {
			tvErr = fmt.Errorf("tv sync: %w", err)
		}
		d.println()
	}

	if movies {
		d.println("=== Movie Sync ===")
		cmd := &SyncMoviesCmd{DryRun: dryRun}
		if err := cmd.Run(d); err != nil {
			movieErr = fmt.Errorf("movie sync: %w", err)
		}
		d.println()
	}

	return errors.Join(tvErr, movieErr)
}

type ListMoviesCmd struct{}

func (c *ListMoviesCmd) Run(d *deps) error {
	if d.cfg.MovieCollectionName == "" {
		return fmt.Errorf("PLEX_MOVIE_COLLECTION is not set")
	}

	ctx := context.Background()
	items, err := d.resolveCollection(ctx, d.cfg.MovieCollectionName)
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(d.output(), 0, 4, 2, ' ', 0)
	if err := writef(w, "#\tTITLE\tTMDB\tTVDB\tRATING KEY\n"); err != nil {
		return err
	}
	for i, item := range items {
		if err := writef(w, "%d\t%s\t%d\t%d\t%s\n", i+1, item.Title, item.TMDBID, item.TVDBID, item.RatingKey); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	d.printf(totalMoviesFormat, len(items))
	return nil
}

type ListTVCmd struct{}

func (c *ListTVCmd) Run(d *deps) error {
	if d.cfg.TVCollectionName == "" {
		return fmt.Errorf("PLEX_TV_COLLECTION is not set")
	}

	ctx := context.Background()
	items, err := d.resolveCollection(ctx, d.cfg.TVCollectionName)
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(d.output(), 0, 4, 2, ' ', 0)
	if err := writef(w, "#\tSHOW\tSEASON\tTYPE\tTMDB\tTVDB\tRATING KEY\n"); err != nil {
		return err
	}
	for i, item := range items {
		showTitle := item.ParentTitle
		if showTitle == "" {
			showTitle = item.Title
		}

		seasonLabel := item.Title
		if item.Type == "season" && item.Index > 0 {
			seasonLabel = fmt.Sprintf("Season %d", item.Index)
		}

		if err := writef(w, "%d\t%s\t%s\t%s\t%d\t%d\t%s\n", i+1, showTitle, seasonLabel, item.Type, item.TMDBID, item.TVDBID, item.RatingKey); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	d.printf("\nTotal: %d items\n", len(items))
	return nil
}

type CheckTVCmd struct{}

type CheckMoviesCmd struct{}

type SyncMoviesCmd struct {
	Number *int `arg:"" optional:"" name:"number" help:"Row number from 'movies list' to sync."`
	DryRun bool `help:"Preview sync changes without modifying Radarr."`
}

type SyncTVCmd struct {
	Number *int `arg:"" optional:"" name:"number" help:"Row number from 'tv list' to sync."`
	DryRun bool `help:"Preview sync changes without modifying Sonarr."`
}

func (c *CheckMoviesCmd) Run(d *deps) error {
	if err := d.validateMovieCheckConfig(); err != nil {
		return err
	}

	ctx := context.Background()
	items, err := d.resolveCollection(ctx, d.cfg.MovieCollectionName)
	if err != nil {
		return err
	}

	lookupCache := make(map[string]cachedMovieLookup)
	statusCounts := make(map[string]int)

	w := tabwriter.NewWriter(d.output(), 0, 4, 2, ' ', 0)
	if err := writef(w, "#\tTITLE\tSTATUS\tMATCH\tDETAIL\n"); err != nil {
		return err
	}
	for i, item := range items {
		lookup, err := d.getCachedMovieLookup(ctx, lookupCache, item.Title, item.TMDBID)
		if err != nil {
			return err
		}

		status, matchBy, detail := evaluateMovieCheck(item, lookup)
		statusCounts[status]++
		if err := writef(w, "%d\t%s\t%s\t%s\t%s\n", i+1, item.Title, status, matchBy, detail); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	d.printf(totalMoviesFormat, len(items))
	printMovieCheckSummary(d.output(), statusCounts)
	return nil
}

func (c *CheckTVCmd) Run(d *deps) error {
	if err := d.validateTVCheckConfig(); err != nil {
		return err
	}

	ctx := context.Background()
	items, err := d.resolveCollection(ctx, d.cfg.TVCollectionName)
	if err != nil {
		return err
	}

	lookupCache := make(map[string]cachedLookup)
	statusCounts := make(map[string]int)

	w := tabwriter.NewWriter(d.output(), 0, 4, 2, ' ', 0)
	if err := writef(w, "#\tSHOW\tSEASON\tSTATUS\tMATCH\tDETAIL\n"); err != nil {
		return err
	}
	for i, item := range items {
		showTitle, seasonLabel, showTVDBID := sonarrLookupTarget(item)
		lookup, err := d.getCachedLookup(ctx, lookupCache, showTitle, showTVDBID)
		if err != nil {
			return err
		}

		status, matchBy, detail := evaluateTVCheck(item, lookup, showTVDBID)

		statusCounts[status]++
		if err := writef(w, "%d\t%s\t%s\t%s\t%s\t%s\n", i+1, showTitle, seasonLabel, status, matchBy, detail); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	d.printf("\nTotal: %d items\n", len(items))
	printTVCheckSummary(d.output(), statusCounts)
	return nil
}

func (c *SyncTVCmd) Run(d *deps) error {
	if err := d.validateTVCheckConfig(); err != nil {
		return err
	}

	ctx := context.Background()
	items, err := d.resolveCollection(ctx, d.cfg.TVCollectionName)
	if err != nil {
		return err
	}
	items, err = selectTVSyncItems(items, c.Number)
	if err != nil {
		return err
	}

	targets := buildTVSyncTargets(items)
	lookupCache := make(map[string]cachedLookup)
	statusCounts := make(map[string]int)
	var defaults sonarr.AddSeriesDefaults
	defaultsResolved := false
	var errs []error

	w := tabwriter.NewWriter(d.output(), 0, 4, 2, ' ', 0)
	if err := writef(w, "#\tSHOW\tTVDB\tMONITOR\tSTATUS\tDETAIL\n"); err != nil {
		return err
	}
	for i, target := range targets {
		status, detail, syncErr := d.processTVSyncTarget(ctx, lookupCache, target, &defaults, &defaultsResolved, c.DryRun)
		statusCounts[status]++
		errs = appendError(errs, syncErr)
		if err := writeTVSyncRow(w, i+1, target, status, detail); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	d.printf("\nTotal: %d shows\n", len(targets))
	printTVSyncSummary(d.output(), statusCounts)
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func (c *SyncMoviesCmd) Run(d *deps) error {
	if err := d.validateMovieCheckConfig(); err != nil {
		return err
	}

	ctx := context.Background()
	items, err := d.resolveCollection(ctx, d.cfg.MovieCollectionName)
	if err != nil {
		return err
	}
	items, err = selectMovieSyncItems(items, c.Number)
	if err != nil {
		return err
	}

	targets := buildMovieSyncTargets(items)
	lookupCache := make(map[string]cachedMovieLookup)
	statusCounts := make(map[string]int)
	var defaults radarr.AddMovieDefaults
	defaultsResolved := false
	var errs []error

	w := tabwriter.NewWriter(d.output(), 0, 4, 2, ' ', 0)
	if err := writef(w, "#\tTITLE\tTMDB\tSTATUS\tDETAIL\n"); err != nil {
		return err
	}
	for i, target := range targets {
		status, detail, syncErr := d.processMovieSyncTarget(ctx, lookupCache, target, &defaults, &defaultsResolved, c.DryRun)
		statusCounts[status]++
		errs = appendError(errs, syncErr)
		if err := writeMovieSyncRow(w, i+1, target, status, detail); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	d.printf(totalMoviesFormat, len(targets))
	printMovieSyncSummary(d.output(), statusCounts)
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

type cachedLookup struct {
	match *sonarr.SeriesMatch
	err   error
}

type cachedMovieLookup struct {
	match *radarr.MovieMatch
	err   error
}

type movieSyncTarget struct {
	Title  string
	TMDBID int64
}

type tvSyncTarget struct {
	Title      string
	TVDBID     int64
	MonitorAll bool
	Seasons    map[int]struct{}
}

// deps holds shared dependencies injected via kong.Bind.
type deps struct {
	cfg    *config.Config
	plex   plexService
	radarr radarrService
	sonarr sonarrService
	out    io.Writer
	errOut io.Writer
}

func (d *deps) output() io.Writer {
	if d != nil && d.out != nil {
		return d.out
	}
	return os.Stdout
}

func (d *deps) errorOutput() io.Writer {
	if d != nil && d.errOut != nil {
		return d.errOut
	}
	return os.Stderr
}

func (d *deps) println(args ...any) {
	_, _ = fmt.Fprintln(d.output(), args...)
}

func (d *deps) printf(format string, args ...any) {
	_, _ = fmt.Fprintf(d.output(), format, args...)
}

func (d *deps) errorf(format string, args ...any) {
	_, _ = fmt.Fprintf(d.errorOutput(), format, args...)
}

func (d *deps) processTVSyncTarget(ctx context.Context, lookupCache map[string]cachedLookup, target tvSyncTarget, defaults *sonarr.AddSeriesDefaults, defaultsResolved *bool, dryRun bool) (string, string, error) {
	lookup, err := d.getCachedLookup(ctx, lookupCache, target.Title, target.TVDBID)
	if err != nil {
		err = fmt.Errorf("looking up %q in Sonarr: %w", target.Title, err)
		return statusFailed, err.Error(), err
	}

	return d.syncTVTarget(ctx, target, lookup, defaults, defaultsResolved, dryRun)
}

func (d *deps) processMovieSyncTarget(ctx context.Context, lookupCache map[string]cachedMovieLookup, target movieSyncTarget, defaults *radarr.AddMovieDefaults, defaultsResolved *bool, dryRun bool) (string, string, error) {
	lookup, err := d.getCachedMovieLookup(ctx, lookupCache, target.Title, target.TMDBID)
	if err != nil {
		err = fmt.Errorf("looking up %q in Radarr: %w", target.Title, err)
		return statusFailed, err.Error(), err
	}

	return d.syncMovieTarget(ctx, target, lookup, defaults, defaultsResolved, dryRun)
}

func (d *deps) resolveCollection(ctx context.Context, name string) ([]plex.Item, error) {
	ratingKey, err := d.plex.FindCollectionByName(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("finding collection %q: %w", name, err)
	}
	items, err := d.plex.GetCollectionItems(ctx, ratingKey)
	if err != nil {
		return nil, fmt.Errorf("getting items for %q: %w", name, err)
	}
	return items, nil
}

func (d *deps) validateTVCheckConfig() error {
	if d.cfg.TVCollectionName == "" {
		return fmt.Errorf("PLEX_TV_COLLECTION is not set")
	}
	if d.cfg.SonarrURL == "" {
		return fmt.Errorf("SONARR_URL is not set")
	}
	if d.cfg.SonarrAPIKey == "" {
		return fmt.Errorf("SONARR_API_KEY is not set")
	}
	return nil
}

func (d *deps) validateMovieCheckConfig() error {
	if d.cfg.MovieCollectionName == "" {
		return fmt.Errorf("PLEX_MOVIE_COLLECTION is not set")
	}
	if d.cfg.RadarrURL == "" {
		return fmt.Errorf("RADARR_URL is not set")
	}
	if d.cfg.RadarrAPIKey == "" {
		return fmt.Errorf("RADARR_API_KEY is not set")
	}
	return nil
}

func (d *deps) getCachedLookup(ctx context.Context, lookupCache map[string]cachedLookup, showTitle string, showTVDBID int64) (cachedLookup, error) {
	lookupKey := sonarrLookupKey(showTitle, showTVDBID)
	lookup, ok := lookupCache[lookupKey]
	if !ok {
		lookup.match, lookup.err = d.sonarr.FindSeries(ctx, showTitle, showTVDBID)
		lookupCache[lookupKey] = lookup
	}
	if lookup.err != nil && !errors.Is(lookup.err, sonarr.ErrSeriesNotFound) {
		return cachedLookup{}, lookup.err
	}
	return lookup, nil
}

func (d *deps) getCachedMovieLookup(ctx context.Context, lookupCache map[string]cachedMovieLookup, title string, tmdbID int64) (cachedMovieLookup, error) {
	lookupKey := radarrLookupKey(title, tmdbID)
	lookup, ok := lookupCache[lookupKey]
	if !ok {
		lookup.match, lookup.err = d.radarr.FindMovie(ctx, title, tmdbID)
		lookupCache[lookupKey] = lookup
	}
	if lookup.err != nil && !errors.Is(lookup.err, radarr.ErrMovieNotFound) {
		return cachedMovieLookup{}, lookup.err
	}
	return lookup, nil
}

func (d *deps) syncTVTarget(ctx context.Context, target tvSyncTarget, lookup cachedLookup, defaults *sonarr.AddSeriesDefaults, defaultsResolved *bool, dryRun bool) (string, string, error) {
	if errors.Is(lookup.err, sonarr.ErrSeriesNotFound) {
		return d.addMissingTVSeries(ctx, target, defaults, defaultsResolved, dryRun)
	}
	if lookup.match == nil {
		return statusFailed, "unexpected empty Sonarr lookup result", errors.New("unexpected empty Sonarr lookup result")
	}
	return d.updateExistingTVSeries(ctx, target, lookup.match, dryRun)
}

func (d *deps) syncMovieTarget(ctx context.Context, target movieSyncTarget, lookup cachedMovieLookup, defaults *radarr.AddMovieDefaults, defaultsResolved *bool, dryRun bool) (string, string, error) {
	if errors.Is(lookup.err, radarr.ErrMovieNotFound) {
		return d.addMissingMovie(ctx, target, defaults, defaultsResolved, dryRun)
	}
	if lookup.match == nil {
		return statusFailed, "unexpected empty Radarr lookup result", errors.New("unexpected empty Radarr lookup result")
	}
	return d.updateExistingMovie(ctx, target, lookup.match, dryRun)
}

func (d *deps) addMissingTVSeries(ctx context.Context, target tvSyncTarget, defaults *sonarr.AddSeriesDefaults, defaultsResolved *bool, dryRun bool) (string, string, error) {
	if target.TVDBID == 0 {
		return statusSkipped, "Plex item has no show TVDB ID; cannot add to Sonarr", nil
	}

	resolvedDefaults, err := d.resolveSonarrAddDefaults(ctx, defaults, defaultsResolved)
	if err != nil {
		return statusFailed, err.Error(), err
	}
	if dryRun {
		candidateTitle, err := d.sonarr.PreviewCreateSeries(ctx, sonarr.CreateSeriesRequest{
			Title:                    target.Title,
			TVDBID:                   target.TVDBID,
			MonitorAll:               target.MonitorAll,
			MonitoredSeasons:         target.seasonNumbers(),
			SearchForMissingEpisodes: d.cfg.SearchAdded,
		}, resolvedDefaults)
		if err != nil {
			wrappedErr := fmt.Errorf("previewing add for %q in Sonarr: %w", target.Title, err)
			return statusFailed, wrappedErr.Error(), wrappedErr
		}
		detail := fmt.Sprintf("would add %s with profile %s in %s", candidateTitle, resolvedDefaults.QualityProfileName, resolvedDefaults.RootFolderPath)
		if d.cfg.SearchAdded {
			detail = appendDetail(detail, "would ask Sonarr to search missing episodes after add")
		}
		return statusWouldAdd, detail, nil
	}

	series, err := d.sonarr.CreateSeries(ctx, sonarr.CreateSeriesRequest{
		Title:                    target.Title,
		TVDBID:                   target.TVDBID,
		MonitorAll:               target.MonitorAll,
		MonitoredSeasons:         target.seasonNumbers(),
		SearchForMissingEpisodes: d.cfg.SearchAdded,
	}, resolvedDefaults)
	if err != nil {
		wrappedErr := fmt.Errorf("adding %q to Sonarr: %w", target.Title, err)
		return statusFailed, wrappedErr.Error(), wrappedErr
	}

	detail := fmt.Sprintf("added %s with profile %s in %s", series.Title, resolvedDefaults.QualityProfileName, resolvedDefaults.RootFolderPath)
	if d.cfg.SearchAdded {
		detail = appendDetail(detail, "Sonarr will search missing episodes after add")
	}
	return statusAdded, detail, nil
}

func (d *deps) updateExistingTVSeries(ctx context.Context, target tvSyncTarget, match *sonarr.SeriesMatch, dryRun bool) (string, string, error) {
	searchSeasons := newlyEnabledSeasonNumbers(match.Series, target)
	existingSearchSeasons := target.requestedSearchSeasonNumbers(match.Series)
	if dryRun {
		changed, err := d.sonarr.PreviewUpdateSeriesMonitoring(match.Series, sonarr.CreateSeriesRequest{
			Title:            target.Title,
			TVDBID:           target.TVDBID,
			MonitorAll:       target.MonitorAll,
			MonitoredSeasons: target.seasonNumbers(),
		})
		if err != nil {
			wrappedErr := fmt.Errorf("previewing update for %q in Sonarr: %w", target.Title, err)
			return statusFailed, wrappedErr.Error(), wrappedErr
		}
		if !changed {
			detail := fmt.Sprintf("already in Sonarr as %s; requested monitoring already enabled", match.Series.Title)
			if d.cfg.SearchExisting {
				detail = appendTVSearchPreview(detail, target.searchPreviewLabel(match.Series), existingSearchSeasons)
			}
			return statusExisting, detail, nil
		}
		detail := fmt.Sprintf("would update %s to monitor %s", match.Series.Title, target.monitorDescription())
		if d.cfg.SearchAdded {
			detail = appendTVSearchPreview(detail, target.searchPreviewLabel(match.Series), searchSeasons)
		}
		return statusWouldUpdate, detail, nil
	}

	updated, changed, err := d.sonarr.UpdateSeriesMonitoring(ctx, match.Series, sonarr.CreateSeriesRequest{
		Title:            target.Title,
		TVDBID:           target.TVDBID,
		MonitorAll:       target.MonitorAll,
		MonitoredSeasons: target.seasonNumbers(),
	})
	if err != nil {
		wrappedErr := fmt.Errorf("updating %q in Sonarr: %w", target.Title, err)
		return statusFailed, wrappedErr.Error(), wrappedErr
	}
	if !changed {
		detail := fmt.Sprintf("already in Sonarr as %s; requested monitoring already enabled", match.Series.Title)
		searchNote, searchErr := d.searchTVSeasons(ctx, match.Series, existingSearchSeasons, d.cfg.SearchExisting)
		detail = appendDetail(detail, searchNote)
		return statusExisting, detail, searchErr
	}
	detail := fmt.Sprintf("updated %s to monitor %s", updated.Title, target.monitorDescription())
	searchNote, searchErr := d.searchTVSeasons(ctx, updated, searchSeasons, d.cfg.SearchAdded)
	detail = appendDetail(detail, searchNote)
	return statusUpdated, detail, searchErr
}

func (d *deps) addMissingMovie(ctx context.Context, target movieSyncTarget, defaults *radarr.AddMovieDefaults, defaultsResolved *bool, dryRun bool) (string, string, error) {
	if target.TMDBID == 0 {
		return statusSkipped, "Plex item has no TMDB ID; cannot add to Radarr", nil
	}

	resolvedDefaults, err := d.resolveRadarrAddDefaults(ctx, defaults, defaultsResolved)
	if err != nil {
		return statusFailed, err.Error(), err
	}
	if dryRun {
		candidateTitle, err := d.radarr.PreviewCreateMovie(ctx, radarr.CreateMovieRequest{
			Title:          target.Title,
			TMDBID:         target.TMDBID,
			SearchForMovie: d.cfg.SearchAdded,
		}, resolvedDefaults)
		if err != nil {
			wrappedErr := fmt.Errorf("previewing add for %q in Radarr: %w", target.Title, err)
			return statusFailed, wrappedErr.Error(), wrappedErr
		}
		detail := fmt.Sprintf("would add %s with profile %s in %s", candidateTitle, resolvedDefaults.QualityProfileName, resolvedDefaults.RootFolderPath)
		if d.cfg.SearchAdded {
			detail = appendDetail(detail, "would ask Radarr to search for the movie after add")
		}
		return statusWouldAdd, detail, nil
	}

	movie, err := d.radarr.CreateMovie(ctx, radarr.CreateMovieRequest{
		Title:          target.Title,
		TMDBID:         target.TMDBID,
		SearchForMovie: d.cfg.SearchAdded,
	}, resolvedDefaults)
	if err != nil {
		wrappedErr := fmt.Errorf("adding %q to Radarr: %w", target.Title, err)
		return statusFailed, wrappedErr.Error(), wrappedErr
	}

	detail := fmt.Sprintf("added %s with profile %s in %s", movie.Title, resolvedDefaults.QualityProfileName, resolvedDefaults.RootFolderPath)
	if d.cfg.SearchAdded {
		detail = appendDetail(detail, "Radarr will search for the movie after add")
	}
	return statusAdded, detail, nil
}

func (d *deps) updateExistingMovie(ctx context.Context, target movieSyncTarget, match *radarr.MovieMatch, dryRun bool) (string, string, error) {
	if dryRun {
		changed, err := d.radarr.PreviewUpdateMovieMonitoring(match.Movie, true)
		if err != nil {
			wrappedErr := fmt.Errorf("previewing update for %q in Radarr: %w", target.Title, err)
			return statusFailed, wrappedErr.Error(), wrappedErr
		}
		if !changed {
			detail := fmt.Sprintf("already in Radarr as %s; monitoring already enabled", match.Movie.Title)
			if d.cfg.SearchExisting {
				detail = appendDetail(detail, fmt.Sprintf("would queue Radarr search for %s", match.Movie.Title))
			}
			return statusExisting, detail, nil
		}
		detail := fmt.Sprintf("would update %s to monitor the movie", match.Movie.Title)
		if d.cfg.SearchAdded {
			detail = appendDetail(detail, fmt.Sprintf("would queue Radarr search for %s", match.Movie.Title))
		}
		return statusWouldUpdate, detail, nil
	}

	updated, changed, err := d.radarr.UpdateMovieMonitoring(ctx, match.Movie, true)
	if err != nil {
		wrappedErr := fmt.Errorf("updating %q in Radarr: %w", target.Title, err)
		return statusFailed, wrappedErr.Error(), wrappedErr
	}
	if !changed {
		detail := fmt.Sprintf("already in Radarr as %s; monitoring already enabled", match.Movie.Title)
		searchNote, searchErr := d.searchMovie(ctx, match.Movie, d.cfg.SearchExisting)
		detail = appendDetail(detail, searchNote)
		return statusExisting, detail, searchErr
	}

	detail := fmt.Sprintf("updated %s to monitor the movie", updated.Title)
	searchNote, searchErr := d.searchMovie(ctx, updated, d.cfg.SearchAdded)
	detail = appendDetail(detail, searchNote)
	return statusUpdated, detail, searchErr
}

func (d *deps) resolveSonarrAddDefaults(ctx context.Context, defaults *sonarr.AddSeriesDefaults, defaultsResolved *bool) (sonarr.AddSeriesDefaults, error) {
	if *defaultsResolved {
		return *defaults, nil
	}

	resolvedDefaults, err := d.sonarr.ResolveAddSeriesDefaults(ctx, d.cfg.SonarrRootFolder, d.cfg.SonarrQualityProfile)
	if err != nil {
		return sonarr.AddSeriesDefaults{}, err
	}
	*defaults = resolvedDefaults
	*defaultsResolved = true
	return resolvedDefaults, nil
}

func (d *deps) resolveRadarrAddDefaults(ctx context.Context, defaults *radarr.AddMovieDefaults, defaultsResolved *bool) (radarr.AddMovieDefaults, error) {
	if *defaultsResolved {
		return *defaults, nil
	}

	resolvedDefaults, err := d.radarr.ResolveAddMovieDefaults(ctx, d.cfg.RadarrRootFolder, d.cfg.RadarrQualityProfile)
	if err != nil {
		return radarr.AddMovieDefaults{}, err
	}
	*defaults = resolvedDefaults
	*defaultsResolved = true
	return resolvedDefaults, nil
}

func describeTVItem(item plex.Item) (string, string) {
	showTitle := item.ParentTitle
	if showTitle == "" {
		showTitle = item.Title
	}

	seasonLabel := item.Title
	if item.Type == "show" {
		seasonLabel = "-"
	} else if item.Type == "season" && item.Index > 0 {
		seasonLabel = fmt.Sprintf("Season %d", item.Index)
	}

	return showTitle, seasonLabel
}

func sonarrLookupTarget(item plex.Item) (string, string, int64) {
	showTitle, seasonLabel := describeTVItem(item)
	showTVDBID := item.ShowTVDBID
	if item.Type == "show" && showTVDBID == 0 {
		showTVDBID = item.TVDBID
	}
	return showTitle, seasonLabel, showTVDBID
}

func evaluateTVCheck(item plex.Item, lookup cachedLookup, showTVDBID int64) (string, string, string) {
	if errors.Is(lookup.err, sonarr.ErrSeriesNotFound) {
		return missingSeriesDetail(showTVDBID)
	}
	if lookup.match == nil {
		return statusPresent, "-", ""
	}

	if item.Type != "season" {
		return statusPresent, lookup.match.MatchedBy, lookup.match.Series.Title
	}

	return evaluateSeasonCheck(item, lookup.match)
}

func evaluateMovieCheck(item plex.Item, lookup cachedMovieLookup) (string, string, string) {
	if errors.Is(lookup.err, radarr.ErrMovieNotFound) {
		return missingMovieDetail(item.TMDBID)
	}
	if lookup.match == nil {
		return statusPresent, "-", ""
	}
	if !lookup.match.Movie.Monitored {
		return statusUnmonitored, lookup.match.MatchedBy, "movie exists in Radarr but is not monitored"
	}
	return statusPresent, lookup.match.MatchedBy, lookup.match.Movie.Title
}

func missingMovieDetail(tmdbID int64) (string, string, string) {
	if tmdbID > 0 {
		return statusMissingMovie, "-", fmt.Sprintf("movie tmdb %d not found in Radarr", tmdbID)
	}
	return statusMissingMovie, "-", "no matching Radarr movie for Plex item"
}

func missingSeriesDetail(showTVDBID int64) (string, string, string) {
	if showTVDBID > 0 {
		return statusMissingSeries, "-", fmt.Sprintf("show tvdb %d not found in Sonarr", showTVDBID)
	}
	return statusMissingSeries, "-", "no matching Sonarr series for Plex show"
}

func evaluateSeasonCheck(item plex.Item, match *sonarr.SeriesMatch) (string, string, string) {
	season, found := sonarr.FindSeason(match.Series, item.Index)
	if !found {
		return statusMissingSeason, match.MatchedBy, fmt.Sprintf("season %d is not defined on %s", item.Index, match.Series.Title)
	}
	if !season.Monitored {
		return statusUnmonitored, match.MatchedBy, fmt.Sprintf("season %d exists but is not monitored", item.Index)
	}
	return statusPresent, match.MatchedBy, fmt.Sprintf("season %d is monitored", item.Index)
}

func printTVCheckSummary(w io.Writer, statusCounts map[string]int) {
	for _, status := range []string{statusPresent, statusMissingSeries, statusMissingSeason, statusUnmonitored} {
		if count := statusCounts[status]; count > 0 {
			_, _ = fmt.Fprintf(w, statusCountFormat, status, count)
		}
	}
}

func printTVSyncSummary(w io.Writer, statusCounts map[string]int) {
	for _, status := range []string{statusAdded, statusUpdated, statusWouldAdd, statusWouldUpdate, statusExisting, statusSkipped, statusFailed} {
		if count := statusCounts[status]; count > 0 {
			_, _ = fmt.Fprintf(w, statusCountFormat, status, count)
		}
	}
}

func printMovieSyncSummary(w io.Writer, statusCounts map[string]int) {
	for _, status := range []string{statusAdded, statusUpdated, statusWouldAdd, statusWouldUpdate, statusExisting, statusSkipped, statusFailed} {
		if count := statusCounts[status]; count > 0 {
			_, _ = fmt.Fprintf(w, statusCountFormat, status, count)
		}
	}
}

func printMovieCheckSummary(w io.Writer, statusCounts map[string]int) {
	for _, status := range []string{statusPresent, statusMissingMovie, statusUnmonitored} {
		if count := statusCounts[status]; count > 0 {
			_, _ = fmt.Fprintf(w, statusCountFormat, status, count)
		}
	}
}

func sonarrLookupKey(showTitle string, showTVDBID int64) string {
	if showTVDBID > 0 {
		return fmt.Sprintf("tvdb:%d", showTVDBID)
	}
	return "title:" + strings.ToLower(strings.TrimSpace(showTitle))
}

func radarrLookupKey(title string, tmdbID int64) string {
	if tmdbID > 0 {
		return fmt.Sprintf("tmdb:%d", tmdbID)
	}
	return "title:" + strings.ToLower(strings.TrimSpace(title))
}

func buildTVSyncTargets(items []plex.Item) []tvSyncTarget {
	targetMap := make(map[string]*tvSyncTarget)
	for _, item := range items {
		showTitle, _, showTVDBID := sonarrLookupTarget(item)
		lookupKey := sonarrLookupKey(showTitle, showTVDBID)
		target, ok := targetMap[lookupKey]
		if !ok {
			target = &tvSyncTarget{
				Title:   showTitle,
				TVDBID:  showTVDBID,
				Seasons: make(map[int]struct{}),
			}
			targetMap[lookupKey] = target
		}

		if item.Type == "show" {
			target.MonitorAll = true
			continue
		}
		if item.Type == "season" {
			target.Seasons[item.Index] = struct{}{}
		}
	}

	targets := make([]tvSyncTarget, 0, len(targetMap))
	for _, target := range targetMap {
		targets = append(targets, *target)
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Title == targets[j].Title {
			return targets[i].TVDBID < targets[j].TVDBID
		}
		return targets[i].Title < targets[j].Title
	})
	return targets
}

func buildMovieSyncTargets(items []plex.Item) []movieSyncTarget {
	type movieTargetGroup struct {
		title      string
		hasUnknown bool
		tmdbIDs    map[int64]struct{}
	}

	groups := make(map[string]*movieTargetGroup)
	for _, item := range items {
		titleKey := radarrLookupKey(item.Title, 0)
		group, ok := groups[titleKey]
		if !ok {
			group = &movieTargetGroup{
				title:   item.Title,
				tmdbIDs: make(map[int64]struct{}),
			}
			groups[titleKey] = group
		}

		if item.TMDBID > 0 {
			group.tmdbIDs[item.TMDBID] = struct{}{}
			continue
		}

		group.hasUnknown = true
	}

	targets := make([]movieSyncTarget, 0, len(items))
	for _, group := range groups {
		switch len(group.tmdbIDs) {
		case 0:
			targets = append(targets, movieSyncTarget{Title: group.title})
		case 1:
			for tmdbID := range group.tmdbIDs {
				targets = append(targets, movieSyncTarget{Title: group.title, TMDBID: tmdbID})
			}
		default:
			if group.hasUnknown {
				targets = append(targets, movieSyncTarget{Title: group.title})
			}
			for tmdbID := range group.tmdbIDs {
				targets = append(targets, movieSyncTarget{Title: group.title, TMDBID: tmdbID})
			}
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Title == targets[j].Title {
			return targets[i].TMDBID < targets[j].TMDBID
		}
		return targets[i].Title < targets[j].Title
	})
	return targets
}

func selectTVSyncItems(items []plex.Item, rowNumber *int) ([]plex.Item, error) {
	if rowNumber == nil {
		return items, nil
	}
	if *rowNumber < 1 || *rowNumber > len(items) {
		return nil, fmt.Errorf("tv row %d is out of range; valid rows are 1-%d", *rowNumber, len(items))
	}
	return []plex.Item{items[*rowNumber-1]}, nil
}

func selectMovieSyncItems(items []plex.Item, rowNumber *int) ([]plex.Item, error) {
	if rowNumber == nil {
		return items, nil
	}
	if *rowNumber < 1 || *rowNumber > len(items) {
		return nil, fmt.Errorf("movie row %d is out of range; valid rows are 1-%d", *rowNumber, len(items))
	}
	return []plex.Item{items[*rowNumber-1]}, nil
}

func (t tvSyncTarget) seasonNumbers() []int {
	seasonNumbers := make([]int, 0, len(t.Seasons))
	for seasonNumber := range t.Seasons {
		seasonNumbers = append(seasonNumbers, seasonNumber)
	}
	sort.Ints(seasonNumbers)
	return seasonNumbers
}

func (t tvSyncTarget) monitorDescription() string {
	if t.MonitorAll {
		return "all seasons"
	}
	seasonNumbers := t.seasonNumbers()
	if len(seasonNumbers) == 0 {
		return "no seasons"
	}
	labels := make([]string, 0, len(seasonNumbers))
	for _, seasonNumber := range seasonNumbers {
		labels = append(labels, fmt.Sprintf("S%d", seasonNumber))
	}
	return strings.Join(labels, ", ")
}

func (t tvSyncTarget) requestedSearchSeasonNumbers(series *starrsonarr.Series) []int {
	if t.MonitorAll {
		if series == nil {
			return nil
		}
		return monitoredSeasonNumbers(series.Seasons)
	}
	return t.seasonNumbers()
}

func monitoredSeasonNumbers(seasons []*starrsonarr.Season) []int {
	seasonNumbers := make([]int, 0, len(seasons))
	for _, season := range seasons {
		if season != nil && season.Monitored {
			seasonNumbers = append(seasonNumbers, season.SeasonNumber)
		}
	}
	sort.Ints(seasonNumbers)
	return seasonNumbers
}

func newlyEnabledSeasonNumbers(series *starrsonarr.Series, target tvSyncTarget) []int {
	if series == nil {
		return nil
	}

	requested := requestedSeasonNumbers(series.Seasons, target)
	var seasonNumbers []int
	for _, season := range series.Seasons {
		if season == nil || season.Monitored {
			continue
		}
		if _, ok := requested[season.SeasonNumber]; ok {
			seasonNumbers = append(seasonNumbers, season.SeasonNumber)
		}
	}
	sort.Ints(seasonNumbers)
	return seasonNumbers
}

func requestedSeasonNumbers(seasons []*starrsonarr.Season, target tvSyncTarget) map[int]struct{} {
	requested := make(map[int]struct{})
	if target.MonitorAll {
		for _, season := range seasons {
			if season != nil {
				requested[season.SeasonNumber] = struct{}{}
			}
		}
		return requested
	}

	for _, seasonNumber := range target.seasonNumbers() {
		requested[seasonNumber] = struct{}{}
	}
	return requested
}

func appendTVSearchPreview(detail string, searchLabel string, seasonNumbers []int) string {
	if searchLabel == "" {
		return detail
	}
	if len(seasonNumbers) == 0 {
		return appendDetail(detail, fmt.Sprintf("would queue Sonarr search for %s", searchLabel))
	}
	return appendDetail(detail, fmt.Sprintf("would queue Sonarr search for %s", formatSeasonLabels(seasonNumbers)))
}

func (d *deps) searchMovie(ctx context.Context, movie *starrradarr.Movie, enabled bool) (string, error) {
	if !enabled {
		return "", nil
	}
	if movie == nil {
		return "", errors.New("cannot queue Radarr movie search without a movie")
	}
	if err := d.radarr.SearchMovie(ctx, movie.ID); err != nil {
		return fmt.Sprintf("search queue failed for %s", movie.Title), err
	}
	return fmt.Sprintf("queued Radarr search for %s", movie.Title), nil
}

func (d *deps) searchTVSeasons(ctx context.Context, series *starrsonarr.Series, seasonNumbers []int, enabled bool) (string, error) {
	if !enabled {
		return "", nil
	}
	if series == nil {
		return "", errors.New("cannot queue Sonarr season search without a series")
	}
	if len(seasonNumbers) == 0 {
		return "search skipped: no specific seasons selected", nil
	}

	var errs []error
	for _, seasonNumber := range seasonNumbers {
		if err := d.sonarr.SearchSeason(ctx, series.ID, seasonNumber); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Sprintf("search queue failed for %s", formatSeasonLabels(seasonNumbers)), errors.Join(errs...)
	}
	return fmt.Sprintf("queued Sonarr search for %s", formatSeasonLabels(seasonNumbers)), nil
}

func (t tvSyncTarget) searchPreviewLabel(series *starrsonarr.Series) string {
	if t.MonitorAll {
		if series != nil && len(monitoredSeasonNumbers(series.Seasons)) > 0 {
			return formatSeasonLabels(monitoredSeasonNumbers(series.Seasons))
		}
		return "all monitored seasons"
	}
	return ""
}

func appendDetail(detail, addition string) string {
	if addition == "" {
		return detail
	}
	if detail == "" {
		return addition
	}
	return detail + "; " + addition
}

func formatSeasonLabels(seasonNumbers []int) string {
	labels := make([]string, 0, len(seasonNumbers))
	for _, seasonNumber := range seasonNumbers {
		labels = append(labels, fmt.Sprintf("S%d", seasonNumber))
	}
	return strings.Join(labels, ", ")
}

func appendError(errs []error, err error) []error {
	if err == nil {
		return errs
	}

	return append(errs, err)
}

func writeTVSyncRow(w io.Writer, index int, target tvSyncTarget, status, detail string) error {
	return writef(w, "%d\t%s\t%d\t%s\t%s\t%s\n", index, target.Title, target.TVDBID, target.monitorDescription(), status, detail)
}

func writeMovieSyncRow(w io.Writer, index int, target movieSyncTarget, status, detail string) error {
	return writef(w, "%d\t%s\t%d\t%s\t%s\n", index, target.Title, target.TMDBID, status, detail)
}

func writef(w io.Writer, format string, args ...any) error {
	_, err := fmt.Fprintf(w, format, args...)
	return err
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}

	appDeps := &deps{
		cfg:    cfg,
		plex:   plex.New(cfg.PlexURL, cfg.PlexToken),
		radarr: radarr.New(cfg.RadarrURL, cfg.RadarrAPIKey),
		sonarr: sonarr.New(cfg.SonarrURL, cfg.SonarrAPIKey),
	}

	var cli CLI
	ctx := kong.Parse(&cli,
		kong.Name("collection-sync"),
		kong.Description("Bridge between Plex collections and *arr apps."),
		kong.UsageOnError(),
		kong.Bind(appDeps),
	)
	if err := ctx.Run(appDeps); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
