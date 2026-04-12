package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	starrradarr "golift.io/starr/radarr"
	starrsonarr "golift.io/starr/sonarr"

	"github.com/dadarrs/collection-sync/internal/config"
	plexpkg "github.com/dadarrs/collection-sync/internal/plex"
	radarrpkg "github.com/dadarrs/collection-sync/internal/radarr"
	sonarrpkg "github.com/dadarrs/collection-sync/internal/sonarr"
	"github.com/dadarrs/collection-sync/internal/ui"
)

const (
	seasonOneLabel     = "Season 1"
	showATitle         = "Show A"
	showBTitle         = "Show B"
	queuedRadarrSearch = "queued Radarr search"
	testMoviesRootPath = "/movies"
)

func TestPrintSyncTargets(t *testing.T) {
	var buf bytes.Buffer
	printSyncTargets(&buf, true, true)
	if got := buf.String(); got != "sync targets: tv, movies\n" {
		t.Fatalf("printSyncTargets() = %q", got)
	}
}

func TestCanSyncHelpers(t *testing.T) {
	d, _, _ := newTestDeps(baseConfig())
	if !d.canSyncTV() || !d.canSyncMovies() {
		t.Fatal("canSyncTV()/canSyncMovies() = false, want true")
	}
	d.cfg.SonarrAPIKey = ""
	d.cfg.RadarrAPIKey = ""
	if d.canSyncTV() || d.canSyncMovies() {
		t.Fatal("canSyncTV()/canSyncMovies() = true, want false")
	}
}

func TestDescribeTVItem(t *testing.T) {
	showTitle, seasonLabel := describeTVItem(plexpkg.Item{Title: "Show", Type: "show", TVDBID: 11})
	if showTitle != "Show" || seasonLabel != "-" {
		t.Fatalf("describeTVItem(show) = (%q, %q)", showTitle, seasonLabel)
	}

	showTitle, seasonLabel = describeTVItem(plexpkg.Item{Title: "Season One", ParentTitle: "Show", Type: "season", Index: 1})
	if showTitle != "Show" || seasonLabel != seasonOneLabel {
		t.Fatalf("describeTVItem(season) = (%q, %q)", showTitle, seasonLabel)
	}
}

func TestSonarrLookupTarget(t *testing.T) {
	showTitle, seasonLabel, tvdbID := sonarrLookupTarget(plexpkg.Item{Title: "Show", Type: "show", TVDBID: 22})
	if showTitle != "Show" || seasonLabel != "-" || tvdbID != 22 {
		t.Fatalf("sonarrLookupTarget(show) = (%q, %q, %d)", showTitle, seasonLabel, tvdbID)
	}

	showTitle, seasonLabel, tvdbID = sonarrLookupTarget(plexpkg.Item{Title: "Season", ParentTitle: "Show", Type: "season", Index: 2, ShowTVDBID: 33})
	if showTitle != "Show" || seasonLabel != "Season 2" || tvdbID != 33 {
		t.Fatalf("sonarrLookupTarget(season) = (%q, %q, %d)", showTitle, seasonLabel, tvdbID)
	}
}

func TestEvaluateCheckHelpers(t *testing.T) {
	status, matchBy, detail := evaluateTVCheck(plexpkg.Item{Type: "show"}, cachedLookup{err: sonarrpkg.ErrSeriesNotFound}, 123)
	if status != statusMissingSeries || matchBy != "-" || !strings.Contains(detail, "123") {
		t.Fatalf("evaluateTVCheck(missing) = (%q, %q, %q)", status, matchBy, detail)
	}

	status, matchBy, detail = evaluateTVCheck(plexpkg.Item{Type: "season", Index: 2}, cachedLookup{match: &sonarrpkg.SeriesMatch{MatchedBy: "tvdb", Series: &starrsonarr.Series{Title: "Show", Seasons: []*starrsonarr.Season{{SeasonNumber: 2, Monitored: false}}}}}, 0)
	if status != statusUnmonitored || matchBy != "tvdb" || !strings.Contains(detail, "not monitored") {
		t.Fatalf("evaluateTVCheck(season) = (%q, %q, %q)", status, matchBy, detail)
	}

	status, matchBy, detail = evaluateMovieCheck(plexpkg.Item{TMDBID: 456}, cachedMovieLookup{err: radarrpkg.ErrMovieNotFound})
	if status != statusMissingMovie || matchBy != "-" || !strings.Contains(detail, "456") {
		t.Fatalf("evaluateMovieCheck(missing) = (%q, %q, %q)", status, matchBy, detail)
	}

	status, matchBy, detail = evaluateMovieCheck(plexpkg.Item{}, cachedMovieLookup{match: &radarrpkg.MovieMatch{MatchedBy: "tmdb", Movie: &starrradarr.Movie{Title: "Movie", Monitored: false}}})
	if status != statusUnmonitored || matchBy != "tmdb" || !strings.Contains(detail, "not monitored") {
		t.Fatalf("evaluateMovieCheck(unmonitored) = (%q, %q, %q)", status, matchBy, detail)
	}
}

func TestSummaryPrintersOnlyEmitTrackedStatuses(t *testing.T) {
	var buf bytes.Buffer
	r := ui.New(&buf)
	printTVCheckSummary(&buf, r, map[string]int{statusPresent: 2, statusFailed: 1})
	if got := buf.String(); got != "present: 2\n" {
		t.Fatalf("printTVCheckSummary() = %q", got)
	}

	buf.Reset()
	printMovieSyncSummary(&buf, r, map[string]int{statusAdded: 1, statusFailed: 2})
	if got := buf.String(); !strings.Contains(got, "added: 1") || !strings.Contains(got, "failed: 2") {
		t.Fatalf("printMovieSyncSummary() = %q", got)
	}
}

func TestBuildTVSyncTargets(t *testing.T) {
	items := []plexpkg.Item{
		{Title: showBTitle, Type: "season", ParentTitle: showBTitle, Index: 2, ShowTVDBID: 200},
		{Title: showATitle, Type: "show", TVDBID: 100},
		{Title: showBTitle, Type: "season", ParentTitle: showBTitle, Index: 1, ShowTVDBID: 200},
		{Title: showATitle, Type: "season", ParentTitle: showATitle, Index: 3, ShowTVDBID: 100},
	}
	targets := buildTVSyncTargets(items)
	if len(targets) != 2 || targets[0].Title != showATitle || !targets[0].MonitorAll || targets[1].Title != showBTitle {
		t.Fatalf("buildTVSyncTargets() = %+v", targets)
	}
	if got := targets[1].monitorDescription(); got != "S1, S2" {
		t.Fatalf("monitorDescription() = %q, want S1, S2", got)
	}
	if got := targets[1].seasonNumbers(); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("seasonNumbers() = %v", got)
	}
}

func TestBuildMovieSyncTargets(t *testing.T) {
	t.Run("upgrades unknown title to sole tmdb id", func(t *testing.T) {
		movieTargets := buildMovieSyncTargets([]plexpkg.Item{{Title: "Movie B", TMDBID: 0}, {Title: "Movie A", TMDBID: 1}, {Title: "Movie B", TMDBID: 2}})
		if len(movieTargets) != 2 || movieTargets[0].Title != "Movie A" || movieTargets[0].TMDBID != 1 || movieTargets[1].Title != "Movie B" || movieTargets[1].TMDBID != 2 {
			t.Fatalf("buildMovieSyncTargets() = %+v", movieTargets)
		}
	})

	t.Run("preserves distinct remakes with same title", func(t *testing.T) {
		movieTargets := buildMovieSyncTargets([]plexpkg.Item{{Title: "Dune", TMDBID: 438631}, {Title: "Dune", TMDBID: 841}, {Title: "Dune", TMDBID: 438631}})
		if len(movieTargets) != 2 {
			t.Fatalf("len(buildMovieSyncTargets()) = %d, want 2 (%+v)", len(movieTargets), movieTargets)
		}
		if movieTargets[0].Title != "Dune" || movieTargets[0].TMDBID != 841 || movieTargets[1].Title != "Dune" || movieTargets[1].TMDBID != 438631 {
			t.Fatalf("buildMovieSyncTargets() = %+v", movieTargets)
		}
	})

	t.Run("keeps unknown title separate when multiple tmdb ids exist", func(t *testing.T) {
		movieTargets := buildMovieSyncTargets([]plexpkg.Item{{Title: "King Kong", TMDBID: 0}, {Title: "King Kong", TMDBID: 9072}, {Title: "King Kong", TMDBID: 254}})
		if len(movieTargets) != 3 {
			t.Fatalf("len(buildMovieSyncTargets()) = %d, want 3 (%+v)", len(movieTargets), movieTargets)
		}
		if movieTargets[0].TMDBID != 0 || movieTargets[1].TMDBID != 254 || movieTargets[2].TMDBID != 9072 {
			t.Fatalf("buildMovieSyncTargets() = %+v", movieTargets)
		}
	})
}

func TestSelectSyncItems(t *testing.T) {
	items := []plexpkg.Item{
		{Title: showBTitle, Type: "season", ParentTitle: showBTitle, Index: 2, ShowTVDBID: 200},
		{Title: showATitle, Type: "show", TVDBID: 100},
		{Title: showBTitle, Type: "season", ParentTitle: showBTitle, Index: 1, ShowTVDBID: 200},
		{Title: showATitle, Type: "season", ParentTitle: showATitle, Index: 3, ShowTVDBID: 100},
	}
	row := 2
	selectedTV, err := selectTVSyncItems(items, &row)
	if err != nil || len(selectedTV) != 1 || selectedTV[0].Title != "Show A" {
		t.Fatalf("selectTVSyncItems() = (%+v, %v)", selectedTV, err)
	}
	if _, err := selectMovieSyncItems([]plexpkg.Item{{Title: "Movie"}}, &row); err == nil {
		t.Fatal("selectMovieSyncItems() error = nil, want out of range error")
	}
}

func TestSeasonFormattingHelpers(t *testing.T) {
	series := &starrsonarr.Series{Seasons: []*starrsonarr.Season{{SeasonNumber: 1, Monitored: true}, {SeasonNumber: 2, Monitored: false}}}
	target := tvSyncTarget{MonitorAll: true, Seasons: map[int]struct{}{1: {}, 2: {}}}
	if got := target.requestedSearchSeasonNumbers(series); len(got) != 1 || got[0] != 1 {
		t.Fatalf("requestedSearchSeasonNumbers() = %v", got)
	}
	if got := monitoredSeasonNumbers(series.Seasons); len(got) != 1 || got[0] != 1 {
		t.Fatalf("monitoredSeasonNumbers() = %v", got)
	}
	if got := newlyEnabledSeasonNumbers(series, tvSyncTarget{Seasons: map[int]struct{}{2: {}}}); len(got) != 1 || got[0] != 2 {
		t.Fatalf("newlyEnabledSeasonNumbers() = %v", got)
	}
	if got := target.searchPreviewLabel(series); got != "S1" {
		t.Fatalf("searchPreviewLabel() = %q, want S1", got)
	}

	if got := appendTVSearchPreview("detail", "S1", []int{1, 2}); got != "detail; would queue Sonarr search for S1, S2" {
		t.Fatalf("appendTVSearchPreview() = %q", got)
	}
	if got := appendDetail("detail", "more"); got != "detail; more" {
		t.Fatalf("appendDetail() = %q", got)
	}
	if got := formatSeasonLabels([]int{1, 2}); got != "S1, S2" {
		t.Fatalf("formatSeasonLabels() = %q", got)
	}
	if got := appendError(nil, errors.New("boom")); len(got) != 1 {
		t.Fatalf("appendError() len = %d, want 1", len(got))
	}
}

func TestFormatInt(t *testing.T) {
	if got := ui.FormatInt(42); got != "42" {
		t.Fatalf("FormatInt(42) = %q", got)
	}
}

func TestLookupKeys(t *testing.T) {
	if sonarrLookupKey(" Show ", 0) != "title:show" || sonarrLookupKey("Show", 10) != "tvdb:10" {
		t.Fatal("sonarrLookupKey() returned unexpected value")
	}
	if radarrLookupKey(" Movie ", 0) != "title:movie" || radarrLookupKey("Movie", 20) != "tmdb:20" {
		t.Fatal("radarrLookupKey() returned unexpected value")
	}
}

func TestDepsValidationAndCollectionResolution(t *testing.T) {
	d, _, _ := newTestDeps(baseConfig())
	if err := d.validateTVCheckConfig(); err != nil {
		t.Fatalf("validateTVCheckConfig() error = %v", err)
	}
	if err := d.validateMovieCheckConfig(); err != nil {
		t.Fatalf("validateMovieCheckConfig() error = %v", err)
	}

	d.cfg.TVCollectionName = ""
	if err := d.validateTVCheckConfig(); err == nil {
		t.Fatal("validateTVCheckConfig() error = nil, want error")
	}
	d.cfg = baseConfig()
	d.cfg.MovieCollectionName = ""
	if err := d.validateMovieCheckConfig(); err == nil {
		t.Fatal("validateMovieCheckConfig() error = nil, want error")
	}

	d.cfg = baseConfig()
	d.plex = fakePlexService{
		findCollectionByName: func(context.Context, string) (string, error) { return "rk", nil },
		getCollectionItems:   func(context.Context, string) ([]plexpkg.Item, error) { return []plexpkg.Item{{Title: "Movie"}}, nil },
	}
	items, err := d.resolveCollection(context.Background(), "Movies")
	if err != nil || len(items) != 1 {
		t.Fatalf("resolveCollection() = (%+v, %v)", items, err)
	}

	d.plex = fakePlexService{findCollectionByName: func(context.Context, string) (string, error) { return "", errors.New("boom") }}
	if _, err := d.resolveCollection(context.Background(), "Movies"); err == nil || !strings.Contains(err.Error(), "finding collection") {
		t.Fatalf("resolveCollection() error = %v, want wrapped find error", err)
	}
}

func TestResolveCollectionPrintsProgress(t *testing.T) {
	d, out, _ := newTestDeps(baseConfig())
	d.plex = fakePlexService{
		findCollectionByName: func(context.Context, string) (string, error) { return "rk", nil },
		getCollectionItems:   func(context.Context, string) ([]plexpkg.Item, error) { return []plexpkg.Item{{Title: "Movie"}}, nil },
	}
	items, err := d.resolveCollection(context.Background(), "Movies")
	if err != nil || len(items) != 1 {
		t.Fatalf("resolveCollection() = (%+v, %v)", items, err)
	}
	got := out.String()
	if !strings.Contains(got, `Finding collection "Movies"`) {
		t.Fatalf("resolveCollection() output missing finding message: %q", got)
	}
	if !strings.Contains(got, `Fetching items for "Movies"`) {
		t.Fatalf("resolveCollection() output missing fetching message: %q", got)
	}
	if !strings.Contains(got, `Loaded 1 items from "Movies"`) {
		t.Fatalf("resolveCollection() output missing loaded message: %q", got)
	}
}

func TestNewCollectionProgressFunc(t *testing.T) {
	var buf bytes.Buffer
	r := ui.New(&buf)
	collectionProgress := newCollectionProgressFunc(r, &buf)
	collectionProgress(1, 3)
	if got := buf.String(); !strings.Contains(got, "Processing collection items") || !strings.Contains(got, "1/3") {
		t.Fatalf("progress(1,3) = %q", got)
	}
	buf.Reset()
	collectionProgress(3, 3)
	if got := buf.String(); !strings.Contains(got, "3/3") || !strings.HasSuffix(got, "\n") {
		t.Fatalf("progress(3,3) = %q", got)
	}
}

func TestSyncCommandsEmitProgressToStderr(t *testing.T) {
	t.Run("tv sync", func(t *testing.T) {
		d, out, errOut := newTestDeps(baseConfig())
		d.plex = fakePlexService{
			findCollectionByName: func(context.Context, string) (string, error) { return "rk", nil },
			getCollectionItems: func(context.Context, string) ([]plexpkg.Item, error) {
				return []plexpkg.Item{{Title: "Show", Type: "show", TVDBID: 100}}, nil
			},
		}
		d.sonarr = fakeSonarrService{
			findSeries: func(context.Context, string, int64) (*sonarrpkg.SeriesMatch, error) {
				return nil, sonarrpkg.ErrSeriesNotFound
			},
			resolveAddSeriesDefaults: func(context.Context, string, string) (sonarrpkg.AddSeriesDefaults, error) {
				return sonarrpkg.AddSeriesDefaults{RootFolderPath: "/tv", QualityProfileName: "HD"}, nil
			},
			previewCreateSeries: func(_ context.Context, request sonarrpkg.CreateSeriesRequest, _ sonarrpkg.AddSeriesDefaults) (string, error) {
				return request.Title, nil
			},
		}

		if err := (&SyncTVCmd{DryRun: true}).Run(d); err != nil {
			t.Fatalf("SyncTVCmd.Run() error = %v", err)
		}
		gotErr := errOut.String()
		if !strings.Contains(gotErr, "Processing Sonarr sync targets") || !strings.Contains(gotErr, "1/1") {
			t.Fatalf("SyncTVCmd.Run() stderr = %q", gotErr)
		}
		if strings.Contains(out.String(), "Processing Sonarr sync targets") {
			t.Fatalf("SyncTVCmd.Run() stdout unexpectedly contains progress: %q", out.String())
		}
	})

	t.Run("movie sync", func(t *testing.T) {
		d, out, errOut := newTestDeps(baseConfig())
		d.plex = fakePlexService{
			findCollectionByName: func(context.Context, string) (string, error) { return "rk", nil },
			getCollectionItems: func(context.Context, string) ([]plexpkg.Item, error) {
				return []plexpkg.Item{{Title: "Movie", TMDBID: 200}}, nil
			},
		}
		d.radarr = fakeRadarrService{
			findMovie: func(context.Context, string, int64) (*radarrpkg.MovieMatch, error) {
				return nil, radarrpkg.ErrMovieNotFound
			},
			resolveAddMovieDefaults: func(context.Context, string, string) (radarrpkg.AddMovieDefaults, error) {
				return radarrpkg.AddMovieDefaults{RootFolderPath: testMoviesRootPath, QualityProfileName: "HD"}, nil
			},
			previewCreateMovie: func(_ context.Context, request radarrpkg.CreateMovieRequest, _ radarrpkg.AddMovieDefaults) (string, error) {
				return request.Title, nil
			},
		}

		if err := (&SyncMoviesCmd{DryRun: true}).Run(d); err != nil {
			t.Fatalf("SyncMoviesCmd.Run() error = %v", err)
		}
		gotErr := errOut.String()
		if !strings.Contains(gotErr, "Processing Radarr sync targets") || !strings.Contains(gotErr, "1/1") {
			t.Fatalf("SyncMoviesCmd.Run() stderr = %q", gotErr)
		}
		if strings.Contains(out.String(), "Processing Radarr sync targets") {
			t.Fatalf("SyncMoviesCmd.Run() stdout unexpectedly contains progress: %q", out.String())
		}
	})
}

func TestLookupCaches(t *testing.T) {
	t.Run("tv lookup caches found and not found", func(t *testing.T) {
		count := 0
		d, _, _ := newTestDeps(baseConfig())
		d.sonarr = fakeSonarrService{findSeries: func(context.Context, string, int64) (*sonarrpkg.SeriesMatch, error) {
			count++
			return nil, sonarrpkg.ErrSeriesNotFound
		}}
		cache := map[string]cachedLookup{}
		first, err := d.getCachedLookup(context.Background(), cache, "Show", 10)
		if err != nil || !errors.Is(first.err, sonarrpkg.ErrSeriesNotFound) {
			t.Fatalf("getCachedLookup() = (%+v, %v)", first, err)
		}
		if _, err := d.getCachedLookup(context.Background(), cache, "Show", 10); err != nil {
			t.Fatalf("getCachedLookup() second error = %v", err)
		}
		if count != 1 {
			t.Fatalf("FindSeries call count = %d, want 1", count)
		}
	})

	t.Run("movie lookup returns non-not-found errors", func(t *testing.T) {
		boom := errors.New("boom")
		d, _, _ := newTestDeps(baseConfig())
		d.radarr = fakeRadarrService{findMovie: func(context.Context, string, int64) (*radarrpkg.MovieMatch, error) { return nil, boom }}
		_, err := d.getCachedMovieLookup(context.Background(), map[string]cachedMovieLookup{}, "Movie", 20)
		if !errors.Is(err, boom) {
			t.Fatalf("getCachedMovieLookup() error = %v, want boom", err)
		}
	})
}

func TestSearchHelpers(t *testing.T) {
	d, _, _ := newTestDeps(baseConfig())

	note, err := d.searchMovie(context.Background(), &starrradarr.Movie{ID: 1, Title: "Movie"}, false)
	if note != "" || err != nil {
		t.Fatalf("searchMovie(disabled) = (%q, %v)", note, err)
	}
	if _, err := d.searchMovie(context.Background(), nil, true); err == nil {
		t.Fatal("searchMovie(nil) error = nil, want error")
	}
	d.radarr = fakeRadarrService{searchMovie: func(context.Context, int64) error { return nil }}
	note, err = d.searchMovie(context.Background(), &starrradarr.Movie{ID: 1, Title: "Movie"}, true)
	if err != nil || !strings.Contains(note, queuedRadarrSearch) {
		t.Fatalf("searchMovie() = (%q, %v)", note, err)
	}
	boom := errors.New("boom")
	d.radarr = fakeRadarrService{searchMovie: func(context.Context, int64) error { return boom }}
	note, err = d.searchMovie(context.Background(), &starrradarr.Movie{ID: 1, Title: "Movie"}, true)
	if !errors.Is(err, boom) || !strings.Contains(note, "search queue failed") {
		t.Fatalf("searchMovie() = (%q, %v)", note, err)
	}

	note, err = d.searchTVSeasons(context.Background(), &starrsonarr.Series{ID: 1, Title: "Show"}, nil, true)
	if err != nil || !strings.Contains(note, "search skipped") {
		t.Fatalf("searchTVSeasons(no seasons) = (%q, %v)", note, err)
	}
	if _, err := d.searchTVSeasons(context.Background(), nil, []int{1}, true); err == nil {
		t.Fatal("searchTVSeasons(nil) error = nil, want error")
	}
	called := []int{}
	d.sonarr = fakeSonarrService{searchSeason: func(_ context.Context, _ int64, seasonNumber int) error {
		called = append(called, seasonNumber)
		if seasonNumber == 2 {
			return errors.New("boom")
		}
		return nil
	}}
	note, err = d.searchTVSeasons(context.Background(), &starrsonarr.Series{ID: 1, Title: "Show"}, []int{1, 2}, true)
	if err == nil || !strings.Contains(note, "search queue failed") || len(called) != 2 {
		t.Fatalf("searchTVSeasons() = (%q, %v), called=%v", note, err, called)
	}
}

func TestAddMissingTVSeriesDryRunAndLive(t *testing.T) {
	d, _, _ := newTestDeps(baseConfig())
	d.cfg.SearchAdded = true
	defaultsCalls := 0
	previewCalls := 0
	createCalls := 0
	d.sonarr = fakeSonarrService{
		resolveAddSeriesDefaults: func(context.Context, string, string) (sonarrpkg.AddSeriesDefaults, error) {
			defaultsCalls++
			return sonarrpkg.AddSeriesDefaults{RootFolderPath: "/tv", QualityProfileID: 7, QualityProfileName: "HD"}, nil
		},
		previewCreateSeries: func(context.Context, sonarrpkg.CreateSeriesRequest, sonarrpkg.AddSeriesDefaults) (string, error) {
			previewCalls++
			return "Show", nil
		},
		createSeries: func(context.Context, sonarrpkg.CreateSeriesRequest, sonarrpkg.AddSeriesDefaults) (*starrsonarr.Series, error) {
			createCalls++
			return &starrsonarr.Series{Title: "Show"}, nil
		},
	}
	defaults := sonarrpkg.AddSeriesDefaults{}
	resolved := false
	status, detail, err := d.addMissingTVSeries(context.Background(), tvSyncTarget{Title: "Show", TVDBID: 100, MonitorAll: true}, &defaults, &resolved, true)
	if err != nil || status != statusWouldAdd || !strings.Contains(detail, "would ask Sonarr to search") {
		t.Fatalf("addMissingTVSeries(dry) = (%q, %q, %v)", status, detail, err)
	}
	status, detail, err = d.addMissingTVSeries(context.Background(), tvSyncTarget{Title: "Show", TVDBID: 100, MonitorAll: true}, &defaults, &resolved, false)
	if err != nil || status != statusAdded || !strings.Contains(detail, "Sonarr will search") {
		t.Fatalf("addMissingTVSeries(live) = (%q, %q, %v)", status, detail, err)
	}
	if defaultsCalls != 1 || previewCalls != 1 || createCalls != 1 {
		t.Fatalf("calls = defaults:%d preview:%d create:%d", defaultsCalls, previewCalls, createCalls)
	}
}

func TestUpdateExistingTVSeries(t *testing.T) {
	searchCalls := []int{}
	d, _, _ := newTestDeps(baseConfig())
	d.cfg.SearchAdded = true
	d.cfg.SearchExisting = true
	series := &starrsonarr.Series{ID: 1, Title: "Show", Seasons: []*starrsonarr.Season{{SeasonNumber: 1, Monitored: true}, {SeasonNumber: 2, Monitored: false}}, Monitored: true}
	d.sonarr = fakeSonarrService{
		previewUpdateSeriesMonitoring: func(*starrsonarr.Series, sonarrpkg.CreateSeriesRequest) (bool, error) { return false, nil },
		updateSeriesMonitoring: func(context.Context, *starrsonarr.Series, sonarrpkg.CreateSeriesRequest) (*starrsonarr.Series, bool, error) {
			series.Seasons[1].Monitored = true
			return series, true, nil
		},
		searchSeason: func(_ context.Context, _ int64, seasonNumber int) error {
			searchCalls = append(searchCalls, seasonNumber)
			return nil
		},
	}
	status, detail, err := d.updateExistingTVSeries(context.Background(), tvSyncTarget{Title: "Show", MonitorAll: true, Seasons: map[int]struct{}{1: {}, 2: {}}}, &sonarrpkg.SeriesMatch{MatchedBy: "tvdb", Series: series}, true)
	if err != nil || status != statusExisting || !strings.Contains(detail, "would queue Sonarr search") {
		t.Fatalf("updateExistingTVSeries(dry) = (%q, %q, %v)", status, detail, err)
	}
	status, detail, err = d.updateExistingTVSeries(context.Background(), tvSyncTarget{Title: "Show", Seasons: map[int]struct{}{2: {}}}, &sonarrpkg.SeriesMatch{MatchedBy: "tvdb", Series: series}, false)
	if err != nil || status != statusUpdated || !strings.Contains(detail, "queued Sonarr search") || len(searchCalls) != 1 || searchCalls[0] != 2 {
		t.Fatalf("updateExistingTVSeries(live) = (%q, %q, %v), searchCalls=%v", status, detail, err, searchCalls)
	}
}

func TestAddMissingMovieAndUpdateExistingMovie(t *testing.T) {
	searchCalls := 0
	d, _, _ := newTestDeps(baseConfig())
	d.cfg.SearchAdded = true
	d.cfg.SearchExisting = true
	defaults := radarrpkg.AddMovieDefaults{}
	resolved := false
	d.radarr = fakeRadarrService{
		resolveAddMovieDefaults: func(context.Context, string, string) (radarrpkg.AddMovieDefaults, error) {
			return radarrpkg.AddMovieDefaults{RootFolderPath: testMoviesRootPath, QualityProfileID: 7, QualityProfileName: "HD"}, nil
		},
		previewCreateMovie: func(context.Context, radarrpkg.CreateMovieRequest, radarrpkg.AddMovieDefaults) (string, error) {
			return "Movie", nil
		},
		createMovie: func(context.Context, radarrpkg.CreateMovieRequest, radarrpkg.AddMovieDefaults) (*starrradarr.Movie, error) {
			return &starrradarr.Movie{Title: "Movie"}, nil
		},
		previewUpdateMovieMonitoring: func(*starrradarr.Movie, bool) (bool, error) { return true, nil },
		updateMovieMonitoring: func(context.Context, *starrradarr.Movie, bool) (*starrradarr.Movie, bool, error) {
			return &starrradarr.Movie{ID: 2, Title: "Movie", Monitored: true}, true, nil
		},
		searchMovie: func(context.Context, int64) error {
			searchCalls++
			return nil
		},
	}
	status, detail, err := d.addMissingMovie(context.Background(), movieSyncTarget{Title: "Movie", TMDBID: 20}, &defaults, &resolved, true)
	if err != nil || status != statusWouldAdd || !strings.Contains(detail, "would ask Radarr") {
		t.Fatalf("addMissingMovie(dry) = (%q, %q, %v)", status, detail, err)
	}
	status, detail, err = d.addMissingMovie(context.Background(), movieSyncTarget{Title: "Movie", TMDBID: 20}, &defaults, &resolved, false)
	if err != nil || status != statusAdded || !strings.Contains(detail, "Radarr will search") {
		t.Fatalf("addMissingMovie(live) = (%q, %q, %v)", status, detail, err)
	}
	status, detail, err = d.updateExistingMovie(context.Background(), movieSyncTarget{Title: "Movie", TMDBID: 20}, &radarrpkg.MovieMatch{MatchedBy: "tmdb", Movie: &starrradarr.Movie{ID: 2, Title: "Movie", Monitored: false}}, false)
	if err != nil || status != statusUpdated || !strings.Contains(detail, queuedRadarrSearch) || searchCalls != 1 {
		t.Fatalf("updateExistingMovie() = (%q, %q, %v), searchCalls=%d", status, detail, err, searchCalls)
	}
}

func TestSyncTargetDispatch(t *testing.T) {
	d, _, _ := newTestDeps(baseConfig())
	d.sonarr = fakeSonarrService{}
	status, _, err := d.syncTVTarget(context.Background(), tvSyncTarget{Title: "Show", TVDBID: 0}, cachedLookup{err: sonarrpkg.ErrSeriesNotFound}, &sonarrpkg.AddSeriesDefaults{}, new(bool), true)
	if err != nil || status != statusSkipped {
		t.Fatalf("syncTVTarget() = (%q, %v)", status, err)
	}
	status, _, err = d.syncTVTarget(context.Background(), tvSyncTarget{Title: "Show"}, cachedLookup{}, &sonarrpkg.AddSeriesDefaults{}, new(bool), true)
	if err == nil || status != statusFailed {
		t.Fatalf("syncTVTarget(nil match) = (%q, %v)", status, err)
	}

	status, _, err = d.syncMovieTarget(context.Background(), movieSyncTarget{Title: "Movie", TMDBID: 0}, cachedMovieLookup{err: radarrpkg.ErrMovieNotFound}, &radarrpkg.AddMovieDefaults{}, new(bool), true)
	if err != nil || status != statusSkipped {
		t.Fatalf("syncMovieTarget() = (%q, %v)", status, err)
	}
}

func TestAddDefaultsAreCached(t *testing.T) {
	d, _, _ := newTestDeps(baseConfig())
	sonarrCalls := 0
	radarrCalls := 0
	d.sonarr = fakeSonarrService{resolveAddSeriesDefaults: func(context.Context, string, string) (sonarrpkg.AddSeriesDefaults, error) {
		sonarrCalls++
		return sonarrpkg.AddSeriesDefaults{RootFolderPath: "/tv", QualityProfileID: 1}, nil
	}}
	d.radarr = fakeRadarrService{resolveAddMovieDefaults: func(context.Context, string, string) (radarrpkg.AddMovieDefaults, error) {
		radarrCalls++
		return radarrpkg.AddMovieDefaults{RootFolderPath: testMoviesRootPath, QualityProfileID: 1}, nil
	}}
	seriesDefaults := sonarrpkg.AddSeriesDefaults{}
	movieDefaults := radarrpkg.AddMovieDefaults{}
	seriesResolved := false
	movieResolved := false
	if _, err := d.resolveSonarrAddDefaults(context.Background(), &seriesDefaults, &seriesResolved); err != nil {
		t.Fatalf("resolveSonarrAddDefaults() error = %v", err)
	}
	if _, err := d.resolveSonarrAddDefaults(context.Background(), &seriesDefaults, &seriesResolved); err != nil {
		t.Fatalf("resolveSonarrAddDefaults() second error = %v", err)
	}
	if _, err := d.resolveRadarrAddDefaults(context.Background(), &movieDefaults, &movieResolved); err != nil {
		t.Fatalf("resolveRadarrAddDefaults() error = %v", err)
	}
	if _, err := d.resolveRadarrAddDefaults(context.Background(), &movieDefaults, &movieResolved); err != nil {
		t.Fatalf("resolveRadarrAddDefaults() second error = %v", err)
	}
	if sonarrCalls != 1 || radarrCalls != 1 {
		t.Fatalf("default resolution calls = sonarr:%d radarr:%d", sonarrCalls, radarrCalls)
	}
}

func TestRunCommandNothingToSync(t *testing.T) {
	d, _, _ := newTestDeps(&config.Config{})
	err := (&RunCmd{}).Run(d)
	if err == nil || !strings.Contains(err.Error(), "nothing to sync") {
		t.Fatalf("Run() error = %v, want nothing to sync", err)
	}
}

func TestRunCommandInvalidInterval(t *testing.T) {
	cfg := baseConfig()
	cfg.Interval = "later"
	d, _, _ := newTestDeps(cfg)
	err := (&RunCmd{}).Run(d)
	if err == nil || !strings.Contains(err.Error(), "invalid interval") {
		t.Fatalf("Run() error = %v, want invalid interval", err)
	}
}

func TestRunCommandDryRunSinglePass(t *testing.T) {
	cfg := baseConfig()
	cfg.MovieCollectionName = ""
	d, out, _ := newTestDeps(cfg)
	d.plex = fakePlexService{
		findCollectionByName: func(context.Context, string) (string, error) { return "rk", nil },
		getCollectionItems:   func(context.Context, string) ([]plexpkg.Item, error) { return nil, nil },
	}
	d.sonarr = fakeSonarrService{}
	if err := (&RunCmd{DryRun: true}).Run(d); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "[dry-run] previewing changes only") || !strings.Contains(got, "sync targets: tv") || !strings.Contains(got, "=== TV Sync ===") {
		t.Fatalf("Run() output = %q", got)
	}
}

func TestListCommandsRenderTables(t *testing.T) {
	cfg := baseConfig()
	d, out, _ := newTestDeps(cfg)
	d.plex = fakePlexService{
		findCollectionByName: func(context.Context, string) (string, error) { return "rk", nil },
		getCollectionItems: func(context.Context, string) ([]plexpkg.Item, error) {
			return []plexpkg.Item{{Title: "Movie", TMDBID: 10, RatingKey: "1"}, {Title: seasonOneLabel, ParentTitle: "Show", Type: "season", Index: 1, TVDBID: 20, RatingKey: "2"}}, nil
		},
	}
	if err := (&ListMoviesCmd{}).Run(d); err != nil {
		t.Fatalf("ListMoviesCmd.Run() error = %v", err)
	}
	if !strings.Contains(out.String(), "TITLE") || !strings.Contains(out.String(), "Movie") {
		t.Fatalf("ListMoviesCmd output = %q", out.String())
	}
	out.Reset()
	if err := (&ListTVCmd{}).Run(d); err != nil {
		t.Fatalf("ListTVCmd.Run() error = %v", err)
	}
	if !strings.Contains(out.String(), "SHOW") || !strings.Contains(out.String(), seasonOneLabel) {
		t.Fatalf("ListTVCmd output = %q", out.String())
	}
}

func TestCheckAndSyncCommands(t *testing.T) {
	cfg := baseConfig()
	d, out, _ := newTestDeps(cfg)
	d.plex = fakePlexService{
		findCollectionByName: func(context.Context, string) (string, error) { return "rk", nil },
		getCollectionItems: func(context.Context, string) ([]plexpkg.Item, error) {
			return []plexpkg.Item{{Title: "Show", Type: "show", TVDBID: 10}, {Title: "Movie", TMDBID: 20}}, nil
		},
	}
	d.sonarr = fakeSonarrService{findSeries: func(context.Context, string, int64) (*sonarrpkg.SeriesMatch, error) {
		return &sonarrpkg.SeriesMatch{MatchedBy: "tvdb", Series: &starrsonarr.Series{Title: "Show", Monitored: true}}, nil
	}}
	d.radarr = fakeRadarrService{
		findMovie: func(context.Context, string, int64) (*radarrpkg.MovieMatch, error) {
			return nil, radarrpkg.ErrMovieNotFound
		},
		resolveAddMovieDefaults: func(context.Context, string, string) (radarrpkg.AddMovieDefaults, error) {
			return radarrpkg.AddMovieDefaults{RootFolderPath: testMoviesRootPath, QualityProfileID: 7, QualityProfileName: "HD"}, nil
		},
		previewCreateMovie: func(context.Context, radarrpkg.CreateMovieRequest, radarrpkg.AddMovieDefaults) (string, error) {
			return "Movie", errors.New("preview failed")
		},
	}
	if err := (&CheckTVCmd{}).Run(d); err != nil {
		t.Fatalf("CheckTVCmd.Run() error = %v", err)
	}
	if !strings.Contains(out.String(), "present") {
		t.Fatalf("CheckTVCmd output = %q", out.String())
	}
	out.Reset()
	row := 3
	if err := (&SyncTVCmd{Number: &row}).Run(d); err == nil {
		t.Fatal("SyncTVCmd.Run() error = nil, want row selection error")
	}
	row = 1
	if err := (&SyncMoviesCmd{DryRun: true}).Run(d); err == nil || !strings.Contains(err.Error(), "preview failed") {
		t.Fatalf("SyncMoviesCmd.Run() error = %v, want preview failure", err)
	}
}

func TestRunContinuouslyAndWriters(t *testing.T) {
	d, out, errOut := newTestDeps(baseConfig())
	d.errorf("sync error: boom\n")
	if got := errOut.String(); !strings.Contains(got, "sync error: boom") {
		t.Fatalf("errorf() wrote %q", got)
	}
	if d.output() != out || d.errorOutput() != errOut {
		t.Fatal("output()/errorOutput() did not return injected writers")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (&RunCmd{}).runContinuously(ctx, d, false, false, time.Minute, time.UTC, make(chan time.Time)); err != nil {
		t.Fatalf("runContinuously() error = %v", err)
	}
	if !strings.Contains(out.String(), "shutting down") {
		t.Fatalf("runContinuously() output = %q", out.String())
	}

	out.Reset()
	ctx, cancel = context.WithCancel(context.Background())
	ticks := make(chan time.Time, 1)
	ticks <- time.Now()
	d.cfg.MovieCollectionName = ""
	d.plex = fakePlexService{
		findCollectionByName: func(context.Context, string) (string, error) { return "rk", nil },
		getCollectionItems: func(context.Context, string) ([]plexpkg.Item, error) {
			cancel()
			return nil, nil
		},
	}
	d.sonarr = fakeSonarrService{}
	if err := (&RunCmd{DryRun: true}).runContinuously(ctx, d, true, false, time.Minute, time.UTC, ticks); err != nil {
		t.Fatalf("runContinuously(tick) error = %v", err)
	}
	if got := out.String(); !strings.Contains(got, "sync started") || !strings.Contains(got, "last run took") || !strings.Contains(got, "current time") || !strings.Contains(got, "next scheduled time") {
		t.Fatalf("runContinuously(tick) output = %q", got)
	}
}

func TestRunLocation(t *testing.T) {
	t.Run("defaults to UTC", func(t *testing.T) {
		t.Setenv("TZ", "")
		if got := runLocation(); got != time.UTC {
			t.Fatalf("runLocation() = %v, want UTC", got)
		}
	})

	t.Run("uses configured timezone", func(t *testing.T) {
		t.Setenv("TZ", "America/New_York")
		if got := runLocation().String(); got != "America/New_York" {
			t.Fatalf("runLocation() = %q, want America/New_York", got)
		}
	})

	t.Run("falls back to UTC for invalid timezone", func(t *testing.T) {
		t.Setenv("TZ", "Mars/Olympus_Mons")
		if got := runLocation(); got != time.UTC {
			t.Fatalf("runLocation() = %v, want UTC", got)
		}
	})
}

func TestPrintWaitStatus(t *testing.T) {
	out := &bytes.Buffer{}
	currentTime := time.Date(2026, time.April, 12, 4, 11, 28, 0, time.UTC)
	nextRun := currentTime.Add(10 * time.Minute)

	printWaitStatus(out, 1500*time.Millisecond, currentTime, nextRun, time.UTC)

	got := out.String()
	for _, want := range []string{
		"waiting for next run",
		"last run took: 1.5s",
		"current time: 2026-04-12 04:11:28 +00:00 UTC",
		"next scheduled time: 2026-04-12 04:21:28 +00:00 UTC",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("printWaitStatus() output = %q, want substring %q", got, want)
		}
	}
}

func TestFormatRunTime(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation() error = %v", err)
	}

	got := formatRunTime(time.Date(2026, time.April, 12, 4, 11, 28, 0, time.UTC), loc)
	want := "2026-04-12 00:11:28 -04:00 EDT"
	if got != want {
		t.Fatalf("formatRunTime() = %q, want %q", got, want)
	}
}

func TestFormatRunDuration(t *testing.T) {
	t.Run("keeps sub-millisecond precision", func(t *testing.T) {
		got := formatRunDuration(326921 * time.Nanosecond)
		if got != "326.921µs" {
			t.Fatalf("formatRunDuration() = %q, want %q", got, "326.921µs")
		}
	})

	t.Run("rounds to milliseconds", func(t *testing.T) {
		got := formatRunDuration(1501 * time.Millisecond)
		if got != "1.501s" {
			t.Fatalf("formatRunDuration() = %q, want %q", got, "1.501s")
		}
	})
}

func TestSyncAllAndProcessHelpers(t *testing.T) {
	t.Run("sync all joins errors and prints headers", func(t *testing.T) {
		d, out, _ := newTestDeps(baseConfig())
		d.plex = fakePlexService{findCollectionByName: func(_ context.Context, name string) (string, error) {
			return "", errors.New(name + " failed")
		}}
		err := d.syncAll(true, true, false)
		if err == nil || !strings.Contains(err.Error(), "tv sync") || !strings.Contains(err.Error(), "movie sync") {
			t.Fatalf("syncAll() error = %v", err)
		}
		if !strings.Contains(out.String(), "=== TV Sync ===") || !strings.Contains(out.String(), "=== Movie Sync ===") {
			t.Fatalf("syncAll() output = %q", out.String())
		}
	})

	t.Run("process helpers wrap lookup failures", func(t *testing.T) {
		boom := errors.New("boom")
		d, _, _ := newTestDeps(baseConfig())
		d.sonarr = fakeSonarrService{findSeries: func(context.Context, string, int64) (*sonarrpkg.SeriesMatch, error) { return nil, boom }}
		status, detail, err := d.processTVSyncTarget(context.Background(), map[string]cachedLookup{}, tvSyncTarget{Title: "Show", TVDBID: 1}, &sonarrpkg.AddSeriesDefaults{}, new(bool), true)
		if !errors.Is(err, boom) || status != statusFailed || !strings.Contains(detail, "looking up \"Show\" in Sonarr") {
			t.Fatalf("processTVSyncTarget() = (%q, %q, %v)", status, detail, err)
		}

		d.radarr = fakeRadarrService{findMovie: func(context.Context, string, int64) (*radarrpkg.MovieMatch, error) { return nil, boom }}
		status, detail, err = d.processMovieSyncTarget(context.Background(), map[string]cachedMovieLookup{}, movieSyncTarget{Title: "Movie", TMDBID: 1}, &radarrpkg.AddMovieDefaults{}, new(bool), true)
		if !errors.Is(err, boom) || status != statusFailed || !strings.Contains(detail, "looking up \"Movie\" in Radarr") {
			t.Fatalf("processMovieSyncTarget() = (%q, %q, %v)", status, detail, err)
		}

		d.radarr = fakeRadarrService{previewUpdateMovieMonitoring: func(*starrradarr.Movie, bool) (bool, error) { return false, nil }}
		status, detail, err = d.syncMovieTarget(context.Background(), movieSyncTarget{Title: "Movie", TMDBID: 1}, cachedMovieLookup{match: &radarrpkg.MovieMatch{MatchedBy: "tmdb", Movie: &starrradarr.Movie{ID: 1, Title: "Movie", Monitored: true}}}, &radarrpkg.AddMovieDefaults{}, new(bool), true)
		if err != nil || status != statusExisting || !strings.Contains(detail, "already in Radarr") {
			t.Fatalf("syncMovieTarget(existing) = (%q, %q, %v)", status, detail, err)
		}
	})
}

func TestCheckMoviesAndMovieUpdateBranches(t *testing.T) {
	t.Run("check movies renders statuses and summary", func(t *testing.T) {
		cfg := baseConfig()
		d, out, _ := newTestDeps(cfg)
		d.plex = fakePlexService{
			findCollectionByName: func(context.Context, string) (string, error) { return "rk", nil },
			getCollectionItems: func(context.Context, string) ([]plexpkg.Item, error) {
				return []plexpkg.Item{{Title: "Missing", TMDBID: 11}, {Title: "Unmonitored", TMDBID: 22}}, nil
			},
		}
		d.radarr = fakeRadarrService{findMovie: func(_ context.Context, title string, tmdbID int64) (*radarrpkg.MovieMatch, error) {
			if tmdbID == 11 {
				return nil, radarrpkg.ErrMovieNotFound
			}
			return &radarrpkg.MovieMatch{MatchedBy: "tmdb", Movie: &starrradarr.Movie{Title: title, Monitored: false}}, nil
		}}
		if err := (&CheckMoviesCmd{}).Run(d); err != nil {
			t.Fatalf("CheckMoviesCmd.Run() error = %v", err)
		}
		got := out.String()
		if !strings.Contains(got, statusMissingMovie) || !strings.Contains(got, statusUnmonitored) || !strings.Contains(got, "unmonitored: 1") {
			t.Fatalf("CheckMoviesCmd output = %q", got)
		}
	})

	t.Run("update existing movie dry run and unchanged live", func(t *testing.T) {
		searchCalls := 0
		d, _, _ := newTestDeps(baseConfig())
		d.cfg.SearchAdded = true
		d.cfg.SearchExisting = true
		d.radarr = fakeRadarrService{
			previewUpdateMovieMonitoring: func(*starrradarr.Movie, bool) (bool, error) { return false, nil },
			updateMovieMonitoring: func(_ context.Context, movie *starrradarr.Movie, _ bool) (*starrradarr.Movie, bool, error) {
				return movie, false, nil
			},
			searchMovie: func(context.Context, int64) error {
				searchCalls++
				return nil
			},
		}
		match := &radarrpkg.MovieMatch{MatchedBy: "tmdb", Movie: &starrradarr.Movie{ID: 7, Title: "Movie", Monitored: true}}
		status, detail, err := d.updateExistingMovie(context.Background(), movieSyncTarget{Title: "Movie", TMDBID: 7}, match, true)
		if err != nil || status != statusExisting || !strings.Contains(detail, "would queue Radarr search") {
			t.Fatalf("updateExistingMovie(dry) = (%q, %q, %v)", status, detail, err)
		}
		status, detail, err = d.updateExistingMovie(context.Background(), movieSyncTarget{Title: "Movie", TMDBID: 7}, match, false)
		if err != nil || status != statusExisting || !strings.Contains(detail, "queued Radarr search") || searchCalls != 1 {
			t.Fatalf("updateExistingMovie(live unchanged) = (%q, %q, %v), searchCalls=%d", status, detail, err, searchCalls)
		}
	})
}

type fakePlexService struct {
	findCollectionByName func(ctx context.Context, name string) (string, error)
	getCollectionItems   func(ctx context.Context, collectionKey string) ([]plexpkg.Item, error)
}

func (f fakePlexService) FindCollectionByName(ctx context.Context, name string) (string, error) {
	if f.findCollectionByName == nil {
		return "", nil
	}
	return f.findCollectionByName(ctx, name)
}

func (f fakePlexService) GetCollectionItems(ctx context.Context, collectionKey string) ([]plexpkg.Item, error) {
	if f.getCollectionItems == nil {
		return nil, nil
	}
	return f.getCollectionItems(ctx, collectionKey)
}

type fakeSonarrService struct {
	findSeries                    func(ctx context.Context, title string, tvdbID int64) (*sonarrpkg.SeriesMatch, error)
	resolveAddSeriesDefaults      func(ctx context.Context, rootFolderSelector, qualityProfileSelector string) (sonarrpkg.AddSeriesDefaults, error)
	previewCreateSeries           func(ctx context.Context, request sonarrpkg.CreateSeriesRequest, defaults sonarrpkg.AddSeriesDefaults) (string, error)
	createSeries                  func(ctx context.Context, request sonarrpkg.CreateSeriesRequest, defaults sonarrpkg.AddSeriesDefaults) (*starrsonarr.Series, error)
	previewUpdateSeriesMonitoring func(series *starrsonarr.Series, request sonarrpkg.CreateSeriesRequest) (bool, error)
	updateSeriesMonitoring        func(ctx context.Context, series *starrsonarr.Series, request sonarrpkg.CreateSeriesRequest) (*starrsonarr.Series, bool, error)
	searchSeason                  func(ctx context.Context, seriesID int64, seasonNumber int) error
}

func (f fakeSonarrService) FindSeries(ctx context.Context, title string, tvdbID int64) (*sonarrpkg.SeriesMatch, error) {
	if f.findSeries == nil {
		return nil, nil
	}
	return f.findSeries(ctx, title, tvdbID)
}

func (f fakeSonarrService) ResolveAddSeriesDefaults(ctx context.Context, rootFolderSelector, qualityProfileSelector string) (sonarrpkg.AddSeriesDefaults, error) {
	if f.resolveAddSeriesDefaults == nil {
		return sonarrpkg.AddSeriesDefaults{}, nil
	}
	return f.resolveAddSeriesDefaults(ctx, rootFolderSelector, qualityProfileSelector)
}

func (f fakeSonarrService) PreviewCreateSeries(ctx context.Context, request sonarrpkg.CreateSeriesRequest, defaults sonarrpkg.AddSeriesDefaults) (string, error) {
	if f.previewCreateSeries == nil {
		return "", nil
	}
	return f.previewCreateSeries(ctx, request, defaults)
}

func (f fakeSonarrService) CreateSeries(ctx context.Context, request sonarrpkg.CreateSeriesRequest, defaults sonarrpkg.AddSeriesDefaults) (*starrsonarr.Series, error) {
	if f.createSeries == nil {
		return nil, nil
	}
	return f.createSeries(ctx, request, defaults)
}

func (f fakeSonarrService) PreviewUpdateSeriesMonitoring(series *starrsonarr.Series, request sonarrpkg.CreateSeriesRequest) (bool, error) {
	if f.previewUpdateSeriesMonitoring == nil {
		return false, nil
	}
	return f.previewUpdateSeriesMonitoring(series, request)
}

func (f fakeSonarrService) UpdateSeriesMonitoring(ctx context.Context, series *starrsonarr.Series, request sonarrpkg.CreateSeriesRequest) (*starrsonarr.Series, bool, error) {
	if f.updateSeriesMonitoring == nil {
		return series, false, nil
	}
	return f.updateSeriesMonitoring(ctx, series, request)
}

func (f fakeSonarrService) SearchSeason(ctx context.Context, seriesID int64, seasonNumber int) error {
	if f.searchSeason == nil {
		return nil
	}
	return f.searchSeason(ctx, seriesID, seasonNumber)
}

type fakeRadarrService struct {
	findMovie                    func(ctx context.Context, title string, tmdbID int64) (*radarrpkg.MovieMatch, error)
	resolveAddMovieDefaults      func(ctx context.Context, rootFolderSelector, qualityProfileSelector string) (radarrpkg.AddMovieDefaults, error)
	previewCreateMovie           func(ctx context.Context, request radarrpkg.CreateMovieRequest, defaults radarrpkg.AddMovieDefaults) (string, error)
	createMovie                  func(ctx context.Context, request radarrpkg.CreateMovieRequest, defaults radarrpkg.AddMovieDefaults) (*starrradarr.Movie, error)
	previewUpdateMovieMonitoring func(movie *starrradarr.Movie, monitored bool) (bool, error)
	updateMovieMonitoring        func(ctx context.Context, movie *starrradarr.Movie, monitored bool) (*starrradarr.Movie, bool, error)
	searchMovie                  func(ctx context.Context, movieID int64) error
}

func (f fakeRadarrService) FindMovie(ctx context.Context, title string, tmdbID int64) (*radarrpkg.MovieMatch, error) {
	if f.findMovie == nil {
		return nil, nil
	}
	return f.findMovie(ctx, title, tmdbID)
}

func (f fakeRadarrService) ResolveAddMovieDefaults(ctx context.Context, rootFolderSelector, qualityProfileSelector string) (radarrpkg.AddMovieDefaults, error) {
	if f.resolveAddMovieDefaults == nil {
		return radarrpkg.AddMovieDefaults{}, nil
	}
	return f.resolveAddMovieDefaults(ctx, rootFolderSelector, qualityProfileSelector)
}

func (f fakeRadarrService) PreviewCreateMovie(ctx context.Context, request radarrpkg.CreateMovieRequest, defaults radarrpkg.AddMovieDefaults) (string, error) {
	if f.previewCreateMovie == nil {
		return "", nil
	}
	return f.previewCreateMovie(ctx, request, defaults)
}

func (f fakeRadarrService) CreateMovie(ctx context.Context, request radarrpkg.CreateMovieRequest, defaults radarrpkg.AddMovieDefaults) (*starrradarr.Movie, error) {
	if f.createMovie == nil {
		return nil, nil
	}
	return f.createMovie(ctx, request, defaults)
}

func (f fakeRadarrService) PreviewUpdateMovieMonitoring(movie *starrradarr.Movie, monitored bool) (bool, error) {
	if f.previewUpdateMovieMonitoring == nil {
		return false, nil
	}
	return f.previewUpdateMovieMonitoring(movie, monitored)
}

func (f fakeRadarrService) UpdateMovieMonitoring(ctx context.Context, movie *starrradarr.Movie, monitored bool) (*starrradarr.Movie, bool, error) {
	if f.updateMovieMonitoring == nil {
		return movie, false, nil
	}
	return f.updateMovieMonitoring(ctx, movie, monitored)
}

func (f fakeRadarrService) SearchMovie(ctx context.Context, movieID int64) error {
	if f.searchMovie == nil {
		return nil
	}
	return f.searchMovie(ctx, movieID)
}

func baseConfig() *config.Config {
	return &config.Config{
		PlexURL:             "http://plex",
		PlexToken:           "token",
		TVCollectionName:    "TV",
		MovieCollectionName: "Movies",
		SonarrURL:           "http://sonarr",
		SonarrAPIKey:        "sonarr-key",
		RadarrURL:           "http://radarr",
		RadarrAPIKey:        "radarr-key",
	}
}

func newTestDeps(cfg *config.Config) (*deps, *bytes.Buffer, *bytes.Buffer) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	return &deps{cfg: cfg, out: out, errOut: errOut, ui: ui.New(out)}, out, errOut
}

var _ io.Writer = (*bytes.Buffer)(nil)
