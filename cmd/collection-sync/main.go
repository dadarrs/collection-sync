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
	"time"
	// Importing time/tzdata ensures the binary includes the IANA time zone database, allowing time.LoadLocation to work even if the host system doesn't have the tzdata installed.
	// This is important for correctly handling time zones when formatting the next scheduled run time in the interval sync status output.
	_ "time/tzdata"

	"github.com/alecthomas/kong"
	starrradarr "golift.io/starr/radarr"
	starrsonarr "golift.io/starr/sonarr"

	"github.com/dadarrs/collection-sync/internal/batchstate"
	"github.com/dadarrs/collection-sync/internal/config"
	"github.com/dadarrs/collection-sync/internal/plex"
	"github.com/dadarrs/collection-sync/internal/radarr"
	"github.com/dadarrs/collection-sync/internal/sonarr"
	"github.com/dadarrs/collection-sync/internal/ui"
)

// version is set at build time via -ldflags "-X main.version=..."
var version = "dev"

const (
	batchScopeMovies          = "movies"
	batchScopeTV              = "tv"
	batchStateFileName        = ".collection-sync-state.json"
	labelProcessedThisRun     = "Processed this run"
	labelTotal                = "Total"
	intervalTimeFormat        = "2006-01-02 15:04:05 -07:00 MST"
	valueMoviesFormat         = "%d movies"
	errUnexpectedRadarrLookup = "unexpected empty Radarr lookup result"
	errUnexpectedSonarrLookup = "unexpected empty Sonarr lookup result"
	statusAdded               = "added"
	statusExisting            = "existing"
	statusFailed              = "failed"
	statusPresent             = "present"
	statusMissingMovie        = "missing-movie"
	statusSkipped             = "skipped"
	statusMissingSeries       = "missing-series"
	statusMissingSeason       = "missing-season"
	statusUnmonitored         = "unmonitored"
	statusUpdated             = "updated"
	statusWouldAdd            = "would-add"
	statusWouldUpdate         = "would-update"
)

type CLI struct {
	Run     RunCmd           `cmd:"" help:"Sync TV and movie collections. Repeats on INTERVAL if set."`
	TV      TVCmd            `cmd:"" help:"TV show and season operations."`
	Movies  MoviesCmd        `cmd:"" help:"Movie operations."`
	Version kong.VersionFlag `name:"version" short:"v" help:"Print version and exit."`
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

type batchStateStore interface {
	Completed(scope string) ([]string, error)
	SetCompleted(scope string, completed []string) error
	ClearScope(scope string) error
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
		d.println(d.ui.Notice("[dry-run]", "previewing changes only"))
	}

	printSyncTargets(d.output(), d.ui, tv, movies)

	if interval == 0 {
		return d.syncAll(tv, movies, c.DryRun, d.cfg.MaxItemsProcessedPerRun)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	loc := runLocation()
	nextRun := time.Now().Add(interval)
	d.println(d.ui.Fields("interval status", []ui.Field{
		{Label: "interval", Value: interval.String()},
		{Label: "next scheduled time", Value: formatRunTime(nextRun, loc)},
	}))
	d.println()

	start := time.Now()
	if err := d.syncAll(tv, movies, c.DryRun, d.cfg.MaxItemsProcessedPerRun); err != nil {
		d.printerrln(d.ui.Notice("sync error:", err.Error()))
	}
	end := time.Now()
	printWaitStatus(d.output(), d.ui, end.Sub(start), end, nextRun, loc)

	return c.runContinuously(ctx, d, tv, movies, interval, loc, ticker.C)
}

func (c *RunCmd) runContinuously(ctx context.Context, d *deps, tv, movies bool, interval time.Duration, loc *time.Location, ticks <-chan time.Time) error {
	for {
		select {
		case <-ctx.Done():
			d.println()
			d.println(d.ui.Section("shutting down"))
			return nil
		case tick := <-ticks:
			start := time.Now()
			d.println()
			d.println(d.ui.Fields("sync started", []ui.Field{{Label: "started at", Value: formatRunTime(start, loc)}}))
			d.println()
			if err := d.syncAll(tv, movies, c.DryRun, d.cfg.MaxItemsProcessedPerRun); err != nil {
				d.printerrln(d.ui.Notice("sync error:", err.Error()))
			}
			end := time.Now()
			printWaitStatus(d.output(), d.ui, end.Sub(start), end, tick.Add(interval), loc)
		}
	}
}

func runLocation() *time.Location {
	tz := strings.TrimSpace(os.Getenv("TZ"))
	if tz == "" {
		return time.UTC
	}

	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC
	}

	return loc
}

func formatRunTime(t time.Time, loc *time.Location) string {
	return t.In(loc).Format(intervalTimeFormat)
}

func formatRunDuration(d time.Duration) string {
	if d < time.Millisecond {
		return d.String()
	}

	return d.Round(time.Millisecond).String()
}

func printWaitStatus(w io.Writer, r *ui.Renderer, lastRun time.Duration, currentTime, nextRun time.Time, loc *time.Location) {
	_, _ = fmt.Fprintln(w, r.Fields("waiting for next run", []ui.Field{
		{Label: "last run took", Value: formatRunDuration(lastRun)},
		{Label: "current time", Value: formatRunTime(currentTime, loc)},
		{Label: "next scheduled time", Value: formatRunTime(nextRun, loc)},
	}))
}

func printSyncTargets(w io.Writer, r *ui.Renderer, tv, movies bool) {
	var targets []string
	if tv {
		targets = append(targets, "tv")
	}
	if movies {
		targets = append(targets, "movies")
	}
	_, _ = fmt.Fprintln(w, r.Fields("", []ui.Field{{Label: "sync targets", Value: strings.Join(targets, ", ")}}))
}

func (d *deps) canSyncTV() bool {
	return d.cfg.TVCollectionName != "" && d.cfg.SonarrURL != "" && d.cfg.SonarrAPIKey != ""
}

func (d *deps) canSyncMovies() bool {
	return d.cfg.MovieCollectionName != "" && d.cfg.RadarrURL != "" && d.cfg.RadarrAPIKey != ""
}

func (d *deps) syncAll(tv, movies, dryRun bool, remainingBudget int) error {
	var tvErr, movieErr error

	if tv {
		d.println(d.ui.Section("TV Sync"))
		processed, err := d.runTVSync(context.Background(), nil, dryRun, remainingBudget, true)
		remainingBudget -= processed
		if remainingBudget < 0 {
			remainingBudget = 0
		}
		if err != nil {
			tvErr = fmt.Errorf("tv sync: %w", err)
		}
		d.println()
	}

	if movies {
		d.println(d.ui.Section("Movie Sync"))
		if _, err := d.runMovieSync(context.Background(), nil, dryRun, remainingBudget, true); err != nil {
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

	t := d.ui.NewTable([]string{"#", "TITLE", "TMDB", "TVDB", "RATING KEY"}, -1)
	for i, item := range items {
		t.AddRow(ui.FormatInt(int64(i+1)), item.Title, ui.FormatInt(item.TMDBID), ui.FormatInt(item.TVDBID), item.RatingKey)
	}
	d.println(t.Render())
	d.println()
	d.println(d.ui.Fields("", []ui.Field{{Label: labelTotal, Value: fmt.Sprintf(valueMoviesFormat, len(items))}}))
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

	t := d.ui.NewTable([]string{"#", "SHOW", "SEASON", "TYPE", "TMDB", "TVDB", "RATING KEY"}, -1)
	for i, item := range items {
		showTitle := item.ParentTitle
		if showTitle == "" {
			showTitle = item.Title
		}

		seasonLabel := item.Title
		if item.Type == "season" && item.Index > 0 {
			seasonLabel = fmt.Sprintf("Season %d", item.Index)
		}

		t.AddRow(ui.FormatInt(int64(i+1)), showTitle, seasonLabel, item.Type, ui.FormatInt(item.TMDBID), ui.FormatInt(item.TVDBID), item.RatingKey)
	}
	d.println(t.Render())
	d.println()
	d.println(d.ui.Fields("", []ui.Field{{Label: labelTotal, Value: fmt.Sprintf("%d items", len(items))}}))
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

	t := d.ui.NewTable([]string{"#", "TITLE", "STATUS", "MATCH", "DETAIL"}, 2)
	for i, item := range items {
		lookup, err := d.getCachedMovieLookup(ctx, lookupCache, item.Title, item.TMDBID)
		if err != nil {
			return err
		}

		status, matchBy, detail := evaluateMovieCheck(item, lookup)
		statusCounts[status]++
		t.AddRow(ui.FormatInt(int64(i+1)), item.Title, status, matchBy, detail)
	}
	d.println(t.Render())
	d.println()
	d.println(d.ui.Fields("", []ui.Field{{Label: labelTotal, Value: fmt.Sprintf(valueMoviesFormat, len(items))}}))
	printMovieCheckSummary(d.output(), d.ui, statusCounts)
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

	t := d.ui.NewTable([]string{"#", "SHOW", "SEASON", "STATUS", "MATCH", "DETAIL"}, 3)
	for i, item := range items {
		showTitle, seasonLabel, showTVDBID := sonarrLookupTarget(item)
		lookup, err := d.getCachedLookup(ctx, lookupCache, showTitle, showTVDBID)
		if err != nil {
			return err
		}

		status, matchBy, detail := evaluateTVCheck(item, lookup, showTVDBID)

		statusCounts[status]++
		t.AddRow(ui.FormatInt(int64(i+1)), showTitle, seasonLabel, status, matchBy, detail)
	}
	d.println(t.Render())
	d.println()
	d.println(d.ui.Fields("", []ui.Field{{Label: labelTotal, Value: fmt.Sprintf("%d items", len(items))}}))
	printTVCheckSummary(d.output(), d.ui, statusCounts)
	return nil
}

func (c *SyncTVCmd) Run(d *deps) error {
	if err := d.validateTVCheckConfig(); err != nil {
		return err
	}
	_, err := d.runTVSync(context.Background(), c.Number, c.DryRun, d.cfg.MaxItemsProcessedPerRun, true)
	return err
}

func (c *SyncMoviesCmd) Run(d *deps) error {
	if err := d.validateMovieCheckConfig(); err != nil {
		return err
	}
	_, err := d.runMovieSync(context.Background(), c.Number, c.DryRun, d.cfg.MaxItemsProcessedPerRun, true)
	return err
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

type movieTargetGroup struct {
	title      string
	hasUnknown bool
	tmdbIDs    map[int64]struct{}
}

type tvSyncTarget struct {
	Title      string
	TVDBID     int64
	MonitorAll bool
	Seasons    map[int]struct{}
}

type syncBatchStats struct {
	totalTargets            int
	eligibleTargets         int
	processedTargets        int
	remainingTargets        int
	alreadySatisfiedTargets int
	invalidTargets          int
	evaluationFailedTargets int
}

type tvSyncPlanEntry struct {
	key    string
	target tvSyncTarget
	lookup cachedLookup
}

type movieSyncPlanEntry struct {
	key    string
	target movieSyncTarget
	lookup cachedMovieLookup
}

type tvSyncPlan struct {
	selected  []tvSyncPlanEntry
	completed []string
	stats     syncBatchStats
	evalErrs  []error
}

type movieSyncPlan struct {
	selected  []movieSyncPlanEntry
	completed []string
	stats     syncBatchStats
	evalErrs  []error
}

type batchClassification int

const (
	batchClassificationActionable batchClassification = iota
	batchClassificationAlreadySatisfied
	batchClassificationInvalid
	batchClassificationEvaluationFailed
)

// deps holds shared dependencies injected via kong.Bind.
type deps struct {
	cfg        *config.Config
	plex       plexService
	radarr     radarrService
	sonarr     sonarrService
	batchState batchStateStore
	ui         *ui.Renderer
	out        io.Writer
	errOut     io.Writer
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

func (d *deps) printerrln(args ...any) {
	_, _ = fmt.Fprintln(d.errorOutput(), args...)
}

func (d *deps) runTVSync(ctx context.Context, rowNumber *int, dryRun bool, limit int, useCursor bool) (int, error) {
	items, err := d.resolveCollection(ctx, d.cfg.TVCollectionName)
	if err != nil {
		return 0, err
	}
	items, err = selectTVSyncItems(items, rowNumber)
	if err != nil {
		return 0, err
	}

	targets := buildTVSyncTargets(items)
	if rowNumber != nil {
		return d.executeTVSyncTargets(ctx, targets, dryRun)
	}

	plan, err := d.planTVSyncTargets(ctx, targets, limit, useCursor)
	if err != nil {
		return 0, err
	}
	printSyncBatchSummary(d.output(), d.ui, "Sonarr", plan.stats)
	processed, runErr := d.executePlannedTVSyncTargets(ctx, plan.selected, dryRun)
	stateErr := d.persistBatchState(batchScopeTV, plan.stats, plan.completed, processed, dryRun, useCursor)
	return processed, errors.Join(errors.Join(plan.evalErrs...), runErr, stateErr)
}

func (d *deps) runMovieSync(ctx context.Context, rowNumber *int, dryRun bool, limit int, useCursor bool) (int, error) {
	items, err := d.resolveCollection(ctx, d.cfg.MovieCollectionName)
	if err != nil {
		return 0, err
	}
	items, err = selectMovieSyncItems(items, rowNumber)
	if err != nil {
		return 0, err
	}

	targets := buildMovieSyncTargets(items)
	if rowNumber != nil {
		return d.executeMovieSyncTargets(ctx, targets, dryRun)
	}

	plan, err := d.planMovieSyncTargets(ctx, targets, limit, useCursor)
	if err != nil {
		return 0, err
	}
	printSyncBatchSummary(d.output(), d.ui, "Radarr", plan.stats)
	processed, runErr := d.executePlannedMovieSyncTargets(ctx, plan.selected, dryRun)
	stateErr := d.persistBatchState(batchScopeMovies, plan.stats, plan.completed, processed, dryRun, useCursor)
	return processed, errors.Join(errors.Join(plan.evalErrs...), runErr, stateErr)
}

func (d *deps) planTVSyncTargets(ctx context.Context, targets []tvSyncTarget, limit int, useCursor bool) (tvSyncPlan, error) {
	plan := tvSyncPlan{stats: syncBatchStats{totalTargets: len(targets)}}
	lookupCache := make(map[string]cachedLookup)
	actionable := make([]tvSyncPlanEntry, 0, len(targets))
	for _, target := range targets {
		entry, classification, err := d.planTVSyncTarget(ctx, lookupCache, target)
		switch classification {
		case batchClassificationActionable:
			actionable = append(actionable, entry)
		case batchClassificationAlreadySatisfied:
			plan.stats.alreadySatisfiedTargets++
		case batchClassificationInvalid:
			plan.stats.invalidTargets++
		case batchClassificationEvaluationFailed:
			plan.stats.evaluationFailedTargets++
			plan.evalErrs = append(plan.evalErrs, err)
		}
	}
	plan.stats.eligibleTargets = len(actionable)

	completed, err := d.completedBatchKeys(batchScopeTV, useCursor && len(actionable) > 0)
	if err != nil {
		return tvSyncPlan{}, fmt.Errorf("loading tv batch state: %w", err)
	}
	selection := selectBatchCycle(actionable, limit, completed, func(entry tvSyncPlanEntry) string { return entry.key })
	plan.selected = selection.selected
	plan.completed = selection.completed
	plan.stats.processedTargets = len(plan.selected)
	plan.stats.remainingTargets = selection.remaining
	return plan, nil
}

func (d *deps) planMovieSyncTargets(ctx context.Context, targets []movieSyncTarget, limit int, useCursor bool) (movieSyncPlan, error) {
	plan := movieSyncPlan{stats: syncBatchStats{totalTargets: len(targets)}}
	lookupCache := make(map[string]cachedMovieLookup)
	actionable := make([]movieSyncPlanEntry, 0, len(targets))
	for _, target := range targets {
		entry, classification, err := d.planMovieSyncTarget(ctx, lookupCache, target)
		switch classification {
		case batchClassificationActionable:
			actionable = append(actionable, entry)
		case batchClassificationAlreadySatisfied:
			plan.stats.alreadySatisfiedTargets++
		case batchClassificationInvalid:
			plan.stats.invalidTargets++
		case batchClassificationEvaluationFailed:
			plan.stats.evaluationFailedTargets++
			plan.evalErrs = append(plan.evalErrs, err)
		}
	}
	plan.stats.eligibleTargets = len(actionable)

	completed, err := d.completedBatchKeys(batchScopeMovies, useCursor && len(actionable) > 0)
	if err != nil {
		return movieSyncPlan{}, fmt.Errorf("loading movie batch state: %w", err)
	}
	selection := selectBatchCycle(actionable, limit, completed, func(entry movieSyncPlanEntry) string { return entry.key })
	plan.selected = selection.selected
	plan.completed = selection.completed
	plan.stats.processedTargets = len(plan.selected)
	plan.stats.remainingTargets = selection.remaining
	return plan, nil
}

func (d *deps) planTVSyncTarget(ctx context.Context, lookupCache map[string]cachedLookup, target tvSyncTarget) (tvSyncPlanEntry, batchClassification, error) {
	lookup, err := d.getCachedLookup(ctx, lookupCache, target.Title, target.TVDBID)
	if err != nil {
		wrappedErr := fmt.Errorf("looking up %q in Sonarr: %w", target.Title, err)
		return tvSyncPlanEntry{}, batchClassificationEvaluationFailed, wrappedErr
	}
	entry := tvSyncPlanEntry{key: sonarrLookupKey(target.Title, target.TVDBID), target: target, lookup: lookup}
	if errors.Is(lookup.err, sonarr.ErrSeriesNotFound) {
		if target.TVDBID == 0 {
			return entry, batchClassificationInvalid, nil
		}
		return entry, batchClassificationActionable, nil
	}
	if lookup.match == nil || lookup.match.Series == nil {
		wrappedErr := errors.New(errUnexpectedSonarrLookup)
		return tvSyncPlanEntry{}, batchClassificationEvaluationFailed, wrappedErr
	}

	actionable, err := d.tvTargetRequiresProcessing(target, lookup.match)
	if err != nil {
		wrappedErr := fmt.Errorf("evaluating %q in Sonarr: %w", target.Title, err)
		return tvSyncPlanEntry{}, batchClassificationEvaluationFailed, wrappedErr
	}
	if actionable {
		return entry, batchClassificationActionable, nil
	}
	return entry, batchClassificationAlreadySatisfied, nil
}

func (d *deps) planMovieSyncTarget(ctx context.Context, lookupCache map[string]cachedMovieLookup, target movieSyncTarget) (movieSyncPlanEntry, batchClassification, error) {
	lookup, err := d.getCachedMovieLookup(ctx, lookupCache, target.Title, target.TMDBID)
	if err != nil {
		wrappedErr := fmt.Errorf("looking up %q in Radarr: %w", target.Title, err)
		return movieSyncPlanEntry{}, batchClassificationEvaluationFailed, wrappedErr
	}
	entry := movieSyncPlanEntry{key: radarrLookupKey(target.Title, target.TMDBID), target: target, lookup: lookup}
	if errors.Is(lookup.err, radarr.ErrMovieNotFound) {
		if target.TMDBID == 0 {
			return entry, batchClassificationInvalid, nil
		}
		return entry, batchClassificationActionable, nil
	}
	if lookup.match == nil || lookup.match.Movie == nil {
		wrappedErr := errors.New(errUnexpectedRadarrLookup)
		return movieSyncPlanEntry{}, batchClassificationEvaluationFailed, wrappedErr
	}

	actionable, err := d.movieTargetRequiresProcessing(lookup.match)
	if err != nil {
		wrappedErr := fmt.Errorf("evaluating %q in Radarr: %w", target.Title, err)
		return movieSyncPlanEntry{}, batchClassificationEvaluationFailed, wrappedErr
	}
	if actionable {
		return entry, batchClassificationActionable, nil
	}
	return entry, batchClassificationAlreadySatisfied, nil
}

func (d *deps) tvTargetRequiresProcessing(target tvSyncTarget, match *sonarr.SeriesMatch) (bool, error) {
	if match == nil || match.Series == nil {
		return false, errors.New("series is required to evaluate Sonarr monitoring")
	}
	changed, err := d.sonarr.PreviewUpdateSeriesMonitoring(match.Series, sonarr.CreateSeriesRequest{
		Title:            target.Title,
		TVDBID:           target.TVDBID,
		MonitorAll:       target.MonitorAll,
		MonitoredSeasons: target.seasonNumbers(),
	})
	if err != nil {
		return false, err
	}
	if changed {
		return true, nil
	}
	if !d.cfg.SearchExisting {
		return false, nil
	}
	return len(target.requestedSearchSeasonNumbers(match.Series)) > 0, nil
}

func (d *deps) movieTargetRequiresProcessing(match *radarr.MovieMatch) (bool, error) {
	if match == nil || match.Movie == nil {
		return false, errors.New("movie is required to evaluate Radarr monitoring")
	}
	changed, err := d.radarr.PreviewUpdateMovieMonitoring(match.Movie, true)
	if err != nil {
		return false, err
	}
	if changed {
		return true, nil
	}
	return d.cfg.SearchExisting, nil
}

func (d *deps) executeTVSyncTargets(ctx context.Context, targets []tvSyncTarget, dryRun bool) (int, error) {
	lookupCache := make(map[string]cachedLookup)
	statusCounts := make(map[string]int)
	var defaults sonarr.AddSeriesDefaults
	defaultsResolved := false
	var errs []error

	t := d.ui.NewTable([]string{"#", "SHOW", "TVDB", "MONITOR", "STATUS", "DETAIL"}, 4)
	progress := d.ui.NewProgress(d.errorOutput(), "Processing Sonarr sync targets", len(targets))
	if len(targets) > 0 {
		progress.Update(0)
	}
	for i, target := range targets {
		status, detail, syncErr := d.processTVSyncTarget(ctx, lookupCache, target, &defaults, &defaultsResolved, dryRun)
		statusCounts[status]++
		errs = appendError(errs, syncErr)
		t.AddRow(ui.FormatInt(int64(i+1)), target.Title, ui.FormatInt(target.TVDBID), target.monitorDescription(), status, detail)
		progress.Update(i + 1)
	}
	if len(targets) > 0 {
		d.println(t.Render())
	}
	d.println()
	d.println(d.ui.Fields("", []ui.Field{{Label: labelProcessedThisRun, Value: fmt.Sprintf("%d shows", len(targets))}}))
	printTVSyncSummary(d.output(), d.ui, statusCounts)
	if len(errs) > 0 {
		return len(targets), errors.Join(errs...)
	}
	return len(targets), nil
}

func (d *deps) executePlannedTVSyncTargets(ctx context.Context, entries []tvSyncPlanEntry, dryRun bool) (int, error) {
	statusCounts := make(map[string]int)
	var defaults sonarr.AddSeriesDefaults
	defaultsResolved := false
	var errs []error

	t := d.ui.NewTable([]string{"#", "SHOW", "TVDB", "MONITOR", "STATUS", "DETAIL"}, 4)
	progress := d.ui.NewProgress(d.errorOutput(), "Processing Sonarr sync targets", len(entries))
	if len(entries) > 0 {
		progress.Update(0)
	}
	for i, entry := range entries {
		status, detail, syncErr := d.syncTVTarget(ctx, entry.target, entry.lookup, &defaults, &defaultsResolved, dryRun)
		statusCounts[status]++
		errs = appendError(errs, syncErr)
		t.AddRow(ui.FormatInt(int64(i+1)), entry.target.Title, ui.FormatInt(entry.target.TVDBID), entry.target.monitorDescription(), status, detail)
		progress.Update(i + 1)
	}
	if len(entries) > 0 {
		d.println(t.Render())
	}
	d.println()
	d.println(d.ui.Fields("", []ui.Field{{Label: labelProcessedThisRun, Value: fmt.Sprintf("%d shows", len(entries))}}))
	printTVSyncSummary(d.output(), d.ui, statusCounts)
	if len(errs) > 0 {
		return len(entries), errors.Join(errs...)
	}
	return len(entries), nil
}

func (d *deps) executeMovieSyncTargets(ctx context.Context, targets []movieSyncTarget, dryRun bool) (int, error) {
	lookupCache := make(map[string]cachedMovieLookup)
	statusCounts := make(map[string]int)
	var defaults radarr.AddMovieDefaults
	defaultsResolved := false
	var errs []error

	t := d.ui.NewTable([]string{"#", "TITLE", "TMDB", "STATUS", "DETAIL"}, 3)
	progress := d.ui.NewProgress(d.errorOutput(), "Processing Radarr sync targets", len(targets))
	if len(targets) > 0 {
		progress.Update(0)
	}
	for i, target := range targets {
		status, detail, syncErr := d.processMovieSyncTarget(ctx, lookupCache, target, &defaults, &defaultsResolved, dryRun)
		statusCounts[status]++
		errs = appendError(errs, syncErr)
		t.AddRow(ui.FormatInt(int64(i+1)), target.Title, ui.FormatInt(target.TMDBID), status, detail)
		progress.Update(i + 1)
	}
	if len(targets) > 0 {
		d.println(t.Render())
	}
	d.println()
	d.println(d.ui.Fields("", []ui.Field{{Label: labelProcessedThisRun, Value: fmt.Sprintf(valueMoviesFormat, len(targets))}}))
	printMovieSyncSummary(d.output(), d.ui, statusCounts)
	if len(errs) > 0 {
		return len(targets), errors.Join(errs...)
	}
	return len(targets), nil
}

func (d *deps) executePlannedMovieSyncTargets(ctx context.Context, entries []movieSyncPlanEntry, dryRun bool) (int, error) {
	statusCounts := make(map[string]int)
	var defaults radarr.AddMovieDefaults
	defaultsResolved := false
	var errs []error

	t := d.ui.NewTable([]string{"#", "TITLE", "TMDB", "STATUS", "DETAIL"}, 3)
	progress := d.ui.NewProgress(d.errorOutput(), "Processing Radarr sync targets", len(entries))
	if len(entries) > 0 {
		progress.Update(0)
	}
	for i, entry := range entries {
		status, detail, syncErr := d.syncMovieTarget(ctx, entry.target, entry.lookup, &defaults, &defaultsResolved, dryRun)
		statusCounts[status]++
		errs = appendError(errs, syncErr)
		t.AddRow(ui.FormatInt(int64(i+1)), entry.target.Title, ui.FormatInt(entry.target.TMDBID), status, detail)
		progress.Update(i + 1)
	}
	if len(entries) > 0 {
		d.println(t.Render())
	}
	d.println()
	d.println(d.ui.Fields("", []ui.Field{{Label: labelProcessedThisRun, Value: fmt.Sprintf(valueMoviesFormat, len(entries))}}))
	printMovieSyncSummary(d.output(), d.ui, statusCounts)
	if len(errs) > 0 {
		return len(entries), errors.Join(errs...)
	}
	return len(entries), nil
}

func (d *deps) completedBatchKeys(scope string, enabled bool) ([]string, error) {
	if !enabled || d == nil || d.batchState == nil {
		return nil, nil
	}
	return d.batchState.Completed(scope)
}

func (d *deps) persistBatchState(scope string, stats syncBatchStats, completed []string, processed int, dryRun, enabled bool) error {
	if !enabled || dryRun || d == nil || d.batchState == nil {
		return nil
	}
	if stats.eligibleTargets == 0 {
		return d.batchState.ClearScope(scope)
	}
	if processed == 0 {
		return nil
	}
	return d.batchState.SetCompleted(scope, completed)
}

func printSyncBatchSummary(w io.Writer, r *ui.Renderer, label string, stats syncBatchStats) {
	fields := []ui.Field{
		{Label: "deduped targets", Value: ui.FormatInt(int64(stats.totalTargets))},
		{Label: "eligible", Value: ui.FormatInt(int64(stats.eligibleTargets))},
		{Label: "processing this run", Value: ui.FormatInt(int64(stats.processedTargets))},
		{Label: "remaining after this run", Value: ui.FormatInt(int64(stats.remainingTargets))},
	}
	if stats.alreadySatisfiedTargets > 0 {
		fields = append(fields, ui.Field{Label: "already satisfied", Value: ui.FormatInt(int64(stats.alreadySatisfiedTargets))})
	}
	if stats.invalidTargets > 0 {
		fields = append(fields, ui.Field{Label: "invalid targets", Value: ui.FormatInt(int64(stats.invalidTargets))})
	}
	if stats.evaluationFailedTargets > 0 {
		fields = append(fields, ui.Field{Label: "evaluation failures", Value: ui.FormatInt(int64(stats.evaluationFailedTargets))})
	}
	_, _ = fmt.Fprintln(w, r.Fields(label+" target summary", fields))
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
	d.println(d.ui.Notice("Finding collection", fmt.Sprintf("%q", name)))
	ratingKey, err := d.plex.FindCollectionByName(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("finding collection %q: %w", name, err)
	}
	d.println(d.ui.Notice("Fetching items for", fmt.Sprintf("%q", name)))
	items, err := d.plex.GetCollectionItems(ctx, ratingKey)
	if err != nil {
		return nil, fmt.Errorf("getting items for %q: %w", name, err)
	}
	d.println(d.ui.Notice("Loaded", fmt.Sprintf("%d items from %q", len(items), name)))
	return items, nil
}

// newCollectionProgressFunc returns a progress callback that writes per-item
// processing status to w using the UI progress bar. A trailing newline is
// appended when current equals total.
func newCollectionProgressFunc(r *ui.Renderer, w io.Writer) func(current, total int) {
	var p *ui.Progress
	lastCurrent := 0
	lastTotal := 0
	return func(current, total int) {
		if p == nil || total != lastTotal || current < lastCurrent {
			p = r.NewProgress(w, "Processing collection items", total)
		}
		p.Update(current)
		lastCurrent = current
		lastTotal = total
	}
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
		return statusFailed, errUnexpectedSonarrLookup, errors.New(errUnexpectedSonarrLookup)
	}
	return d.updateExistingTVSeries(ctx, target, lookup.match, dryRun)
}

func (d *deps) syncMovieTarget(ctx context.Context, target movieSyncTarget, lookup cachedMovieLookup, defaults *radarr.AddMovieDefaults, defaultsResolved *bool, dryRun bool) (string, string, error) {
	if errors.Is(lookup.err, radarr.ErrMovieNotFound) {
		return d.addMissingMovie(ctx, target, defaults, defaultsResolved, dryRun)
	}
	if lookup.match == nil {
		return statusFailed, errUnexpectedRadarrLookup, errors.New(errUnexpectedRadarrLookup)
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

func printTVCheckSummary(w io.Writer, r *ui.Renderer, statusCounts map[string]int) {
	ui.StatusSummary(w, r.Theme(), r.IsTTY(), statusCounts, []string{statusPresent, statusMissingSeries, statusMissingSeason, statusUnmonitored})
}

func printTVSyncSummary(w io.Writer, r *ui.Renderer, statusCounts map[string]int) {
	ui.StatusSummary(w, r.Theme(), r.IsTTY(), statusCounts, []string{statusAdded, statusUpdated, statusWouldAdd, statusWouldUpdate, statusExisting, statusSkipped, statusFailed})
}

func printMovieSyncSummary(w io.Writer, r *ui.Renderer, statusCounts map[string]int) {
	ui.StatusSummary(w, r.Theme(), r.IsTTY(), statusCounts, []string{statusAdded, statusUpdated, statusWouldAdd, statusWouldUpdate, statusExisting, statusSkipped, statusFailed})
}

func printMovieCheckSummary(w io.Writer, r *ui.Renderer, statusCounts map[string]int) {
	ui.StatusSummary(w, r.Theme(), r.IsTTY(), statusCounts, []string{statusPresent, statusMissingMovie, statusUnmonitored})
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
		accumulateTVSyncTarget(targetMap, item)
	}

	targets := make([]tvSyncTarget, 0, len(targetMap))
	for _, target := range targetMap {
		targets = append(targets, *target)
	}
	sort.Slice(targets, func(i, j int) bool { return tvSyncTargetLess(targets[i], targets[j]) })
	return targets
}

func accumulateTVSyncTarget(targetMap map[string]*tvSyncTarget, item plex.Item) {
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
	markTVSyncTarget(target, item)
}

func markTVSyncTarget(target *tvSyncTarget, item plex.Item) {
	if target == nil {
		return
	}
	if item.Type == "show" {
		target.MonitorAll = true
		return
	}
	if item.Type == "season" {
		target.Seasons[item.Index] = struct{}{}
	}
}

func tvSyncTargetLess(left, right tvSyncTarget) bool {
	if left.Title == right.Title {
		return left.TVDBID < right.TVDBID
	}
	return left.Title < right.Title
}

func buildMovieSyncTargets(items []plex.Item) []movieSyncTarget {
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
		targets = appendMovieGroupTargets(targets, group)
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Title == targets[j].Title {
			return targets[i].TMDBID < targets[j].TMDBID
		}
		return targets[i].Title < targets[j].Title
	})
	return targets
}

func appendMovieGroupTargets(targets []movieSyncTarget, group *movieTargetGroup) []movieSyncTarget {
	if group == nil {
		return targets
	}
	if len(group.tmdbIDs) == 0 {
		return append(targets, movieSyncTarget{Title: group.title})
	}
	if len(group.tmdbIDs) == 1 {
		for tmdbID := range group.tmdbIDs {
			return append(targets, movieSyncTarget{Title: group.title, TMDBID: tmdbID})
		}
	}
	if group.hasUnknown {
		targets = append(targets, movieSyncTarget{Title: group.title})
	}
	for tmdbID := range group.tmdbIDs {
		targets = append(targets, movieSyncTarget{Title: group.title, TMDBID: tmdbID})
	}
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

type batchSelection[T any] struct {
	selected  []T
	completed []string
	remaining int
}

func selectBatchCycle[T any](entries []T, limit int, completed []string, key func(T) string) batchSelection[T] {
	if len(entries) == 0 {
		return batchSelection[T]{}
	}

	completedSet := make(map[string]struct{}, len(completed))
	for _, entryKey := range completed {
		completedSet[entryKey] = struct{}{}
	}

	pending := make([]T, 0, len(entries))
	prunedCompleted := make(map[string]struct{}, len(completedSet))
	for _, entry := range entries {
		entryKey := key(entry)
		if _, ok := completedSet[entryKey]; ok {
			prunedCompleted[entryKey] = struct{}{}
			continue
		}
		pending = append(pending, entry)
	}
	if len(pending) == 0 {
		pending = append([]T(nil), entries...)
		prunedCompleted = map[string]struct{}{}
	}

	if limit < 0 {
		limit = 0
	}
	count := limit
	if count > len(pending) {
		count = len(pending)
	}
	selected := append([]T(nil), pending[:count]...)

	updatedCompleted := make([]string, 0, len(prunedCompleted)+len(selected))
	for _, entry := range entries {
		entryKey := key(entry)
		if _, ok := prunedCompleted[entryKey]; ok {
			updatedCompleted = append(updatedCompleted, entryKey)
		}
	}
	for _, entry := range selected {
		updatedCompleted = append(updatedCompleted, key(entry))
	}

	return batchSelection[T]{
		selected:  selected,
		completed: updatedCompleted,
		remaining: len(pending) - len(selected),
	}
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

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))

	cfg, err := config.Load()
	if err != nil {
		errRenderer := ui.New(os.Stderr)
		fmt.Fprintln(os.Stderr, errRenderer.Notice("config:", err.Error()))
		os.Exit(1)
	}

	renderer := ui.Stdout()

	plexClient := plex.New(cfg.PlexURL, cfg.PlexToken)
	plexClient.OnProgress = newCollectionProgressFunc(renderer, os.Stderr)
	appDeps := &deps{
		cfg:        cfg,
		plex:       plexClient,
		radarr:     radarr.New(cfg.RadarrURL, cfg.RadarrAPIKey),
		sonarr:     sonarr.New(cfg.SonarrURL, cfg.SonarrAPIKey),
		batchState: batchstate.New(batchStateFileName),
		ui:         renderer,
	}

	var cli CLI
	ctx := kong.Parse(&cli,
		kong.Name("collection-sync"),
		kong.Description("Bridge between Plex collections and *arr apps."),
		kong.UsageOnError(),
		kong.Bind(appDeps),
		kong.Vars{"version": version},
	)
	if err := ctx.Run(appDeps); err != nil {
		errRenderer := ui.New(os.Stderr)
		fmt.Fprintln(os.Stderr, errRenderer.Notice("error:", err.Error()))
		os.Exit(1)
	}
}
