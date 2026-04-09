package sonarr

import (
	"context"
	"errors"
	"strings"
	"testing"

	starrsonarr "golift.io/starr/sonarr"
)

func TestBuildAddSeriesInput(t *testing.T) {
	candidate := testSeriesCandidate()
	defaults := AddSeriesDefaults{RootFolderPath: "/tv", QualityProfileID: 7}

	t.Run("monitor all", func(t *testing.T) {
		input, err := buildAddSeriesInput(candidate, CreateSeriesRequest{
			TVDBID:                   candidate.TvdbID,
			MonitorAll:               true,
			SearchForMissingEpisodes: true,
		}, defaults)
		if err != nil {
			t.Fatalf("buildAddSeriesInput() error = %v", err)
		}
		if input.AddOptions.Monitor != starrsonarr.MonitorAll {
			t.Fatalf("Monitor = %q, want %q", input.AddOptions.Monitor, starrsonarr.MonitorAll)
		}
		if input.QualityProfileID != defaults.QualityProfileID || input.RootFolderPath != defaults.RootFolderPath {
			t.Fatal("buildAddSeriesInput() did not map defaults")
		}
		if !input.AddOptions.SearchForMissingEpisodes {
			t.Fatal("SearchForMissingEpisodes = false, want true")
		}
	})

	t.Run("selected seasons", func(t *testing.T) {
		input, err := buildAddSeriesInput(candidate, CreateSeriesRequest{MonitoredSeasons: []int{1, 3}}, defaults)
		if err != nil {
			t.Fatalf("buildAddSeriesInput() error = %v", err)
		}
		if input.AddOptions.Monitor != starrsonarr.MonitorSkip {
			t.Fatalf("Monitor = %q, want %q", input.AddOptions.Monitor, starrsonarr.MonitorSkip)
		}
		if len(input.Seasons) != 3 || !input.Seasons[0].Monitored || input.Seasons[1].Monitored || !input.Seasons[2].Monitored {
			t.Fatalf("Seasons = %+v, want seasons 1 and 3 monitored", input.Seasons)
		}
	})

	t.Run("missing requested seasons", func(t *testing.T) {
		_, err := buildAddSeriesInput(candidate, CreateSeriesRequest{MonitoredSeasons: []int{9}}, defaults)
		if err == nil || !strings.Contains(err.Error(), "requested seasons 9") {
			t.Fatalf("buildAddSeriesInput() error = %v, want requested seasons error", err)
		}
	})
}

func TestBuildUpdateSeriesInput(t *testing.T) {
	series := testExistingSeries()

	t.Run("no-op update", func(t *testing.T) {
		input, changed, err := buildUpdateSeriesInput(series, CreateSeriesRequest{})
		if err != nil {
			t.Fatalf("buildUpdateSeriesInput() error = %v", err)
		}
		if changed {
			t.Fatal("changed = true, want false")
		}
		if input.ID != series.ID {
			t.Fatalf("ID = %d, want %d", input.ID, series.ID)
		}
	})

	t.Run("monitor all changes", func(t *testing.T) {
		_, changed, err := buildUpdateSeriesInput(series, CreateSeriesRequest{MonitorAll: true})
		if err != nil {
			t.Fatalf("buildUpdateSeriesInput() error = %v", err)
		}
		if !changed {
			t.Fatal("changed = false, want true")
		}
	})

	t.Run("selected season changes", func(t *testing.T) {
		input, changed, err := buildUpdateSeriesInput(series, CreateSeriesRequest{MonitoredSeasons: []int{2}})
		if err != nil {
			t.Fatalf("buildUpdateSeriesInput() error = %v", err)
		}
		if !changed || !input.Seasons[1].Monitored {
			t.Fatalf("buildUpdateSeriesInput() = (%+v, %t), want changed season 2", input.Seasons, changed)
		}
	})

	t.Run("missing requested season", func(t *testing.T) {
		_, _, err := buildUpdateSeriesInput(series, CreateSeriesRequest{MonitoredSeasons: []int{9}})
		if err == nil || !strings.Contains(err.Error(), "requested seasons 9") {
			t.Fatalf("buildUpdateSeriesInput() error = %v, want requested seasons error", err)
		}
	})
}

func TestBuildMonitoredSeasons(t *testing.T) {
	t.Run("empty candidate seasons", func(t *testing.T) {
		if _, err := buildMonitoredSeasons(nil, []int{1}); err == nil {
			t.Fatal("buildMonitoredSeasons() error = nil, want error")
		}
	})

	t.Run("nil seasons ignored and missing ordered", func(t *testing.T) {
		_, err := buildMonitoredSeasons([]*starrsonarr.Season{{SeasonNumber: 2}, nil}, []int{3, 1})
		if err == nil || !strings.Contains(err.Error(), "1, 3") {
			t.Fatalf("buildMonitoredSeasons() error = %v, want ordered missing seasons", err)
		}
	})
}

func TestBuildUpdatedSeasons(t *testing.T) {
	t.Run("empty existing seasons", func(t *testing.T) {
		if _, _, err := buildUpdatedSeasons(nil, CreateSeriesRequest{MonitoredSeasons: []int{1}}); err == nil {
			t.Fatal("buildUpdatedSeasons() error = nil, want error")
		}
	})

	t.Run("unchanged result", func(t *testing.T) {
		seasons, changed, err := buildUpdatedSeasons([]*starrsonarr.Season{{SeasonNumber: 1, Monitored: true}}, CreateSeriesRequest{})
		if err != nil {
			t.Fatalf("buildUpdatedSeasons() error = %v", err)
		}
		if changed || !seasons[0].Monitored {
			t.Fatalf("buildUpdatedSeasons() = (%+v, %t), want unchanged monitored season", seasons, changed)
		}
	})

	t.Run("monitor all", func(t *testing.T) {
		seasons, changed, err := buildUpdatedSeasons([]*starrsonarr.Season{{SeasonNumber: 1}, {SeasonNumber: 2}}, CreateSeriesRequest{MonitorAll: true})
		if err != nil {
			t.Fatalf("buildUpdatedSeasons() error = %v", err)
		}
		if !changed || !seasons[0].Monitored || !seasons[1].Monitored {
			t.Fatalf("buildUpdatedSeasons() = (%+v, %t), want all monitored", seasons, changed)
		}
	})

	t.Run("missing requested season ordered", func(t *testing.T) {
		_, _, err := buildUpdatedSeasons([]*starrsonarr.Season{{SeasonNumber: 2}}, CreateSeriesRequest{MonitoredSeasons: []int{3, 1}})
		if err == nil || !strings.Contains(err.Error(), "1, 3") {
			t.Fatalf("buildUpdatedSeasons() error = %v, want ordered missing seasons", err)
		}
	})
}

func TestFindSeason(t *testing.T) {
	if _, ok := FindSeason(nil, 1); ok {
		t.Fatal("FindSeason(nil) = found, want not found")
	}

	series := &starrsonarr.Series{Seasons: []*starrsonarr.Season{{SeasonNumber: 1}, {SeasonNumber: 2}}}
	if season, ok := FindSeason(series, 2); !ok || season.SeasonNumber != 2 {
		t.Fatalf("FindSeason(series, 2) = (%+v, %t), want season 2", season, ok)
	}
	if _, ok := FindSeason(series, 9); ok {
		t.Fatal("FindSeason(series, 9) = found, want not found")
	}
}

func TestTitleAndSelectorHelpers(t *testing.T) {
	if got := normalizeTitle("  The.Office! "); got != "theoffice" {
		t.Fatalf("normalizeTitle() = %q, want theoffice", got)
	}

	series := &starrsonarr.Series{Title: "The Office", CleanTitle: "office us"}
	if !titlesMatch(series, "The Office", normalizeTitle("The Office")) {
		t.Fatal("titlesMatch() exact title = false, want true")
	}
	if !titlesMatch(series, "The-Office", normalizeTitle("The-Office")) {
		t.Fatal("titlesMatch() normalized title = false, want true")
	}
	if !titlesMatch(series, "Office US", normalizeTitle("Office US")) {
		t.Fatal("titlesMatch() clean title = false, want true")
	}

	rootFolders := []*starrsonarr.RootFolder{{ID: 1, Path: "/tv-a"}, {ID: 2, Path: "/tv-b"}}
	if folder, ok := matchRootFolder(rootFolders, "2"); !ok || folder.Path != "/tv-b" {
		t.Fatalf("matchRootFolder(id) = (%+v, %t), want /tv-b", folder, ok)
	}
	if folder, ok := matchRootFolder(rootFolders, "/tv-a"); !ok || folder.ID != 1 {
		t.Fatalf("matchRootFolder(path) = (%+v, %t), want id 1", folder, ok)
	}
	if _, ok := matchRootFolder(rootFolders, "/missing"); ok {
		t.Fatal("matchRootFolder(missing) = true, want false")
	}

	profiles := []*starrsonarr.QualityProfile{{ID: 10, Name: "HD"}, {ID: 20, Name: "UHD"}}
	if profile, ok := matchQualityProfile(profiles, "20"); !ok || profile.Name != "UHD" {
		t.Fatalf("matchQualityProfile(id) = (%+v, %t), want UHD", profile, ok)
	}
	if profile, ok := matchQualityProfile(profiles, "hd"); !ok || profile.ID != 10 {
		t.Fatalf("matchQualityProfile(name) = (%+v, %t), want id 10", profile, ok)
	}
	if _, ok := matchQualityProfile(profiles, "sd"); ok {
		t.Fatal("matchQualityProfile(missing) = true, want false")
	}

	if got := joinRootFolderPaths(rootFolders); got != "/tv-a, /tv-b" {
		t.Fatalf("joinRootFolderPaths() = %q", got)
	}
	if got := joinQualityProfileNames(profiles); got != "HD, UHD" {
		t.Fatalf("joinQualityProfileNames() = %q", got)
	}
	if got := joinSeasonNumbers([]int{1, 2, 3}); got != "1, 2, 3" {
		t.Fatalf("joinSeasonNumbers() = %q", got)
	}
}

func TestClientFindSeries(t *testing.T) {
	ctx := context.Background()

	t.Run("not configured", func(t *testing.T) {
		if _, err := (*Client)(nil).FindSeries(ctx, "Show", 1); err == nil {
			t.Fatal("FindSeries() error = nil, want error")
		}
	})

	t.Run("tvdb hit", func(t *testing.T) {
		client := &Client{api: fakeSonarrAPI{
			getSeriesContext: func(context.Context, int64) ([]*starrsonarr.Series, error) {
				return []*starrsonarr.Series{{Title: "Show", TvdbID: 123}}, nil
			},
		}}
		match, err := client.FindSeries(ctx, "Show", 123)
		if err != nil {
			t.Fatalf("FindSeries() error = %v", err)
		}
		if match.MatchedBy != "tvdb" || match.Series.TvdbID != 123 {
			t.Fatalf("FindSeries() = %+v, want tvdb match", match)
		}
	})

	t.Run("tvdb miss then title hit", func(t *testing.T) {
		client := &Client{api: fakeSonarrAPI{
			getSeriesContext: func(context.Context, int64) ([]*starrsonarr.Series, error) { return nil, nil },
			getAllSeriesContext: func(context.Context) ([]*starrsonarr.Series, error) {
				return []*starrsonarr.Series{{Title: "The Office"}}, nil
			},
		}}
		match, err := client.FindSeries(ctx, "The Office", 123)
		if err != nil {
			t.Fatalf("FindSeries() error = %v", err)
		}
		if match.MatchedBy != "title" {
			t.Fatalf("MatchedBy = %q, want title", match.MatchedBy)
		}
	})

	t.Run("blank title with no tvdb match returns not found", func(t *testing.T) {
		client := &Client{api: fakeSonarrAPI{getSeriesContext: func(context.Context, int64) ([]*starrsonarr.Series, error) { return nil, nil }}}
		_, err := client.FindSeries(ctx, "   ", 123)
		if !errors.Is(err, ErrSeriesNotFound) {
			t.Fatalf("FindSeries() error = %v, want ErrSeriesNotFound", err)
		}
	})

	t.Run("sdk errors are wrapped", func(t *testing.T) {
		boom := errors.New("boom")
		client := &Client{api: fakeSonarrAPI{getSeriesContext: func(context.Context, int64) ([]*starrsonarr.Series, error) { return nil, boom }}}
		_, err := client.FindSeries(ctx, "Show", 123)
		if err == nil || !strings.Contains(err.Error(), "looking up sonarr series by tvdb id 123") || !errors.Is(err, boom) {
			t.Fatalf("FindSeries() error = %v, want wrapped boom", err)
		}
	})
}

func TestResolveAddSeriesDefaults(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		client := &Client{api: fakeSonarrAPI{
			getRootFoldersContext: func(context.Context) ([]*starrsonarr.RootFolder, error) {
				return []*starrsonarr.RootFolder{{ID: 1, Path: "/tv", Accessible: true}}, nil
			},
			getQualityProfilesContext: func(context.Context) ([]*starrsonarr.QualityProfile, error) {
				return []*starrsonarr.QualityProfile{{ID: 2, Name: "HD"}}, nil
			},
		}}
		defaults, err := client.ResolveAddSeriesDefaults(ctx, "", "")
		if err != nil {
			t.Fatalf("ResolveAddSeriesDefaults() error = %v", err)
		}
		if defaults.RootFolderPath != "/tv" || defaults.QualityProfileID != 2 || defaults.QualityProfileName != "HD" {
			t.Fatalf("ResolveAddSeriesDefaults() = %+v", defaults)
		}
	})

	t.Run("multiple options require selector", func(t *testing.T) {
		client := &Client{api: fakeSonarrAPI{
			getRootFoldersContext: func(context.Context) ([]*starrsonarr.RootFolder, error) {
				return []*starrsonarr.RootFolder{{ID: 1, Path: "/tv-a", Accessible: true}, {ID: 2, Path: "/tv-b", Accessible: true}}, nil
			},
		}}
		if _, err := client.resolveRootFolder(ctx, ""); err == nil {
			t.Fatal("resolveRootFolder() error = nil, want error")
		}
	})

	t.Run("selector not found", func(t *testing.T) {
		client := &Client{api: fakeSonarrAPI{
			getQualityProfilesContext: func(context.Context) ([]*starrsonarr.QualityProfile, error) {
				return []*starrsonarr.QualityProfile{{ID: 1, Name: "HD"}}, nil
			},
		}}
		if _, err := client.resolveQualityProfile(ctx, "UHD"); err == nil {
			t.Fatal("resolveQualityProfile() error = nil, want error")
		}
	})
}

func TestCreateAndPreviewSeries(t *testing.T) {
	ctx := context.Background()
	defaults := AddSeriesDefaults{RootFolderPath: "/tv", QualityProfileID: 7, QualityProfileName: "HD"}

	t.Run("not configured", func(t *testing.T) {
		if _, err := (*Client)(nil).CreateSeries(ctx, CreateSeriesRequest{TVDBID: 1}, defaults); err == nil {
			t.Fatal("CreateSeries() error = nil, want error")
		}
	})

	t.Run("missing tvdb id", func(t *testing.T) {
		client := &Client{api: fakeSonarrAPI{}}
		if _, err := client.CreateSeries(ctx, CreateSeriesRequest{}, defaults); err == nil {
			t.Fatal("CreateSeries() error = nil, want error")
		}
	})

	t.Run("lookup failure wrapped", func(t *testing.T) {
		boom := errors.New("boom")
		client := &Client{api: fakeSonarrAPI{getSeriesLookupContext: func(context.Context, string, int64) ([]*starrsonarr.Series, error) { return nil, boom }}}
		if _, err := client.CreateSeries(ctx, CreateSeriesRequest{Title: "Show", TVDBID: 1}, defaults); err == nil || !errors.Is(err, boom) {
			t.Fatalf("CreateSeries() error = %v, want wrapped boom", err)
		}
	})

	t.Run("add request mapped", func(t *testing.T) {
		var got *starrsonarr.AddSeriesInput
		client := &Client{api: fakeSonarrAPI{
			getSeriesLookupContext: func(context.Context, string, int64) ([]*starrsonarr.Series, error) {
				return []*starrsonarr.Series{testSeriesCandidate()}, nil
			},
			addSeriesContext: func(_ context.Context, input *starrsonarr.AddSeriesInput) (*starrsonarr.Series, error) {
				got = input
				return &starrsonarr.Series{Title: input.Title}, nil
			},
		}}
		series, err := client.CreateSeries(ctx, CreateSeriesRequest{Title: "Show", TVDBID: 123, MonitorAll: true, SearchForMissingEpisodes: true}, defaults)
		if err != nil {
			t.Fatalf("CreateSeries() error = %v", err)
		}
		if series.Title != "Show" || got == nil || got.RootFolderPath != "/tv" || got.QualityProfileID != 7 || !got.AddOptions.SearchForMissingEpisodes {
			t.Fatalf("CreateSeries() captured input = %+v", got)
		}
	})

	t.Run("add failure wrapped", func(t *testing.T) {
		boom := errors.New("boom")
		client := &Client{api: fakeSonarrAPI{
			getSeriesLookupContext: func(context.Context, string, int64) ([]*starrsonarr.Series, error) {
				return []*starrsonarr.Series{testSeriesCandidate()}, nil
			},
			addSeriesContext: func(context.Context, *starrsonarr.AddSeriesInput) (*starrsonarr.Series, error) { return nil, boom },
		}}
		if _, err := client.CreateSeries(ctx, CreateSeriesRequest{Title: "Show", TVDBID: 123, MonitorAll: true}, defaults); err == nil || !errors.Is(err, boom) {
			t.Fatalf("CreateSeries() error = %v, want wrapped boom", err)
		}
	})

	t.Run("preview returns candidate title", func(t *testing.T) {
		client := &Client{api: fakeSonarrAPI{getSeriesLookupContext: func(context.Context, string, int64) ([]*starrsonarr.Series, error) {
			return []*starrsonarr.Series{testSeriesCandidate()}, nil
		}}}
		title, err := client.PreviewCreateSeries(ctx, CreateSeriesRequest{Title: "Show", TVDBID: 123, MonitorAll: true}, defaults)
		if err != nil {
			t.Fatalf("PreviewCreateSeries() error = %v", err)
		}
		if title != "Show" {
			t.Fatalf("PreviewCreateSeries() = %q, want Show", title)
		}
	})
}

func TestUpdateAndPreviewSeriesMonitoring(t *testing.T) {
	ctx := context.Background()

	t.Run("nil series", func(t *testing.T) {
		client := &Client{api: fakeSonarrAPI{}}
		if _, _, err := client.UpdateSeriesMonitoring(ctx, nil, CreateSeriesRequest{}); err == nil {
			t.Fatal("UpdateSeriesMonitoring() error = nil, want error")
		}
		if _, err := client.PreviewUpdateSeriesMonitoring(nil, CreateSeriesRequest{}); err == nil {
			t.Fatal("PreviewUpdateSeriesMonitoring() error = nil, want error")
		}
	})

	t.Run("unchanged returns existing series", func(t *testing.T) {
		series := testExistingSeries()
		client := &Client{api: fakeSonarrAPI{}}
		updated, changed, err := client.UpdateSeriesMonitoring(ctx, series, CreateSeriesRequest{})
		if err != nil {
			t.Fatalf("UpdateSeriesMonitoring() error = %v", err)
		}
		if changed || updated != series {
			t.Fatalf("UpdateSeriesMonitoring() = (%+v, %t), want unchanged original", updated, changed)
		}
		preview, err := client.PreviewUpdateSeriesMonitoring(series, CreateSeriesRequest{})
		if err != nil || preview {
			t.Fatalf("PreviewUpdateSeriesMonitoring() = (%t, %v), want false nil", preview, err)
		}
	})

	t.Run("changed calls update", func(t *testing.T) {
		series := testExistingSeries()
		called := false
		client := &Client{api: fakeSonarrAPI{updateSeriesContext: func(_ context.Context, input *starrsonarr.AddSeriesInput, moveFiles bool) (*starrsonarr.Series, error) {
			called = true
			if moveFiles {
				t.Fatal("moveFiles = true, want false")
			}
			return &starrsonarr.Series{Title: input.Title, Monitored: input.Monitored, Seasons: input.Seasons}, nil
		}}}
		updated, changed, err := client.UpdateSeriesMonitoring(ctx, series, CreateSeriesRequest{MonitoredSeasons: []int{2}})
		if err != nil {
			t.Fatalf("UpdateSeriesMonitoring() error = %v", err)
		}
		if !called || !changed || updated == nil {
			t.Fatalf("UpdateSeriesMonitoring() = (%+v, %t, %v), want changed update call", updated, changed, err)
		}
		preview, err := client.PreviewUpdateSeriesMonitoring(series, CreateSeriesRequest{MonitoredSeasons: []int{2}})
		if err != nil || !preview {
			t.Fatalf("PreviewUpdateSeriesMonitoring() = (%t, %v), want true nil", preview, err)
		}
	})

	t.Run("update failure wrapped", func(t *testing.T) {
		boom := errors.New("boom")
		client := &Client{api: fakeSonarrAPI{updateSeriesContext: func(context.Context, *starrsonarr.AddSeriesInput, bool) (*starrsonarr.Series, error) {
			return nil, boom
		}}}
		if _, _, err := client.UpdateSeriesMonitoring(ctx, testExistingSeries(), CreateSeriesRequest{MonitoredSeasons: []int{2}}); err == nil || !errors.Is(err, boom) {
			t.Fatalf("UpdateSeriesMonitoring() error = %v, want wrapped boom", err)
		}
	})
}

func TestSearchSeason(t *testing.T) {
	ctx := context.Background()

	t.Run("missing series id", func(t *testing.T) {
		client := &Client{api: fakeSonarrAPI{}}
		if err := client.SearchSeason(ctx, 0, 1); err == nil {
			t.Fatal("SearchSeason() error = nil, want error")
		}
	})

	t.Run("invalid season number", func(t *testing.T) {
		client := &Client{api: fakeSonarrAPI{}}
		if err := client.SearchSeason(ctx, 1, -1); err == nil {
			t.Fatal("SearchSeason() error = nil, want error")
		}
	})

	t.Run("sends command", func(t *testing.T) {
		called := false
		client := &Client{api: fakeSonarrAPI{sendCommandContext: func(_ context.Context, cmd *starrsonarr.CommandRequest) (*starrsonarr.CommandResponse, error) {
			called = true
			if cmd.Name != commandSeasonSearch || cmd.SeriesID != 7 || cmd.SeasonNumber != 2 {
				t.Fatalf("CommandRequest = %+v", cmd)
			}
			return &starrsonarr.CommandResponse{}, nil
		}}}
		if err := client.SearchSeason(ctx, 7, 2); err != nil {
			t.Fatalf("SearchSeason() error = %v", err)
		}
		if !called {
			t.Fatal("SendCommandContext was not called")
		}
	})

	t.Run("command error wrapped", func(t *testing.T) {
		boom := errors.New("boom")
		client := &Client{api: fakeSonarrAPI{sendCommandContext: func(context.Context, *starrsonarr.CommandRequest) (*starrsonarr.CommandResponse, error) {
			return nil, boom
		}}}
		if err := client.SearchSeason(ctx, 7, 2); err == nil || !errors.Is(err, boom) {
			t.Fatalf("SearchSeason() error = %v, want wrapped boom", err)
		}
	})
}

type fakeSonarrAPI struct {
	addSeriesContext          func(ctx context.Context, series *starrsonarr.AddSeriesInput) (*starrsonarr.Series, error)
	updateSeriesContext       func(ctx context.Context, series *starrsonarr.AddSeriesInput, moveFiles bool) (*starrsonarr.Series, error)
	sendCommandContext        func(ctx context.Context, cmd *starrsonarr.CommandRequest) (*starrsonarr.CommandResponse, error)
	getSeriesContext          func(ctx context.Context, tvdbID int64) ([]*starrsonarr.Series, error)
	getAllSeriesContext       func(ctx context.Context) ([]*starrsonarr.Series, error)
	getSeriesLookupContext    func(ctx context.Context, term string, tvdbID int64) ([]*starrsonarr.Series, error)
	getRootFoldersContext     func(ctx context.Context) ([]*starrsonarr.RootFolder, error)
	getQualityProfilesContext func(ctx context.Context) ([]*starrsonarr.QualityProfile, error)
}

func (f fakeSonarrAPI) AddSeriesContext(ctx context.Context, series *starrsonarr.AddSeriesInput) (*starrsonarr.Series, error) {
	if f.addSeriesContext == nil {
		return nil, nil
	}
	return f.addSeriesContext(ctx, series)
}

func (f fakeSonarrAPI) UpdateSeriesContext(ctx context.Context, series *starrsonarr.AddSeriesInput, moveFiles bool) (*starrsonarr.Series, error) {
	if f.updateSeriesContext == nil {
		return nil, nil
	}
	return f.updateSeriesContext(ctx, series, moveFiles)
}

func (f fakeSonarrAPI) SendCommandContext(ctx context.Context, cmd *starrsonarr.CommandRequest) (*starrsonarr.CommandResponse, error) {
	if f.sendCommandContext == nil {
		return nil, nil
	}
	return f.sendCommandContext(ctx, cmd)
}

func (f fakeSonarrAPI) GetSeriesContext(ctx context.Context, tvdbID int64) ([]*starrsonarr.Series, error) {
	if f.getSeriesContext == nil {
		return nil, nil
	}
	return f.getSeriesContext(ctx, tvdbID)
}

func (f fakeSonarrAPI) GetAllSeriesContext(ctx context.Context) ([]*starrsonarr.Series, error) {
	if f.getAllSeriesContext == nil {
		return nil, nil
	}
	return f.getAllSeriesContext(ctx)
}

func (f fakeSonarrAPI) GetSeriesLookupContext(ctx context.Context, term string, tvdbID int64) ([]*starrsonarr.Series, error) {
	if f.getSeriesLookupContext == nil {
		return nil, nil
	}
	return f.getSeriesLookupContext(ctx, term, tvdbID)
}

func (f fakeSonarrAPI) GetRootFoldersContext(ctx context.Context) ([]*starrsonarr.RootFolder, error) {
	if f.getRootFoldersContext == nil {
		return nil, nil
	}
	return f.getRootFoldersContext(ctx)
}

func (f fakeSonarrAPI) GetQualityProfilesContext(ctx context.Context) ([]*starrsonarr.QualityProfile, error) {
	if f.getQualityProfilesContext == nil {
		return nil, nil
	}
	return f.getQualityProfilesContext(ctx)
}

func testSeriesCandidate() *starrsonarr.Series {
	return &starrsonarr.Series{
		Title:             "Show",
		TitleSlug:         "show",
		TvdbID:            123,
		SeasonFolder:      true,
		UseSceneNumbering: true,
		LanguageProfileID: 6,
		SeriesType:        "standard",
		Seasons:           []*starrsonarr.Season{{SeasonNumber: 1}, {SeasonNumber: 2}, {SeasonNumber: 3}},
	}
}

func testExistingSeries() *starrsonarr.Series {
	return &starrsonarr.Series{
		ID:               99,
		Title:            "Show",
		TitleSlug:        "show",
		Path:             "/tv/show",
		SeriesType:       "standard",
		RootFolderPath:   "/tv",
		QualityProfileID: 7,
		Monitored:        true,
		Seasons:          []*starrsonarr.Season{{SeasonNumber: 1, Monitored: true}, {SeasonNumber: 2, Monitored: false}},
	}
}
