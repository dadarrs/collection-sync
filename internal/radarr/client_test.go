package radarr

import (
	"context"
	"errors"
	"strings"
	"testing"

	starrradarr "golift.io/starr/radarr"
)

const (
	moviesPathA      = "/movies-a"
	moviesPathB      = "/movies-b"
	moviesRootPath   = "/movies"
	movieTitleLookup = "Movie Title"
)

func TestNewInitializesClient(t *testing.T) {
	if client := New("http://radarr", "key"); client == nil || client.api == nil {
		t.Fatal("New() returned an uninitialized client")
	}
}

func TestMatchRootFolder(t *testing.T) {
	rootFolders := []*starrradarr.RootFolder{{ID: 1, Path: moviesPathA}, {ID: 2, Path: moviesPathB}}
	if folder, ok := matchRootFolder(rootFolders, "2"); !ok || folder.Path != "/movies-b" {
		t.Fatalf("matchRootFolder(id) = (%+v, %t), want /movies-b", folder, ok)
	}
	if folder, ok := matchRootFolder(rootFolders, moviesPathA); !ok || folder.ID != 1 {
		t.Fatalf("matchRootFolder(path) = (%+v, %t), want id 1", folder, ok)
	}
	if _, ok := matchRootFolder(rootFolders, "/missing"); ok {
		t.Fatal("matchRootFolder(missing) = true, want false")
	}
}

func TestMatchQualityProfile(t *testing.T) {
	profiles := []*starrradarr.QualityProfile{{ID: 10, Name: "HD"}, {ID: 20, Name: "UHD"}}
	if profile, ok := matchQualityProfile(profiles, "20"); !ok || profile.Name != "UHD" {
		t.Fatalf("matchQualityProfile(id) = (%+v, %t), want UHD", profile, ok)
	}
	if profile, ok := matchQualityProfile(profiles, "hd"); !ok || profile.ID != 10 {
		t.Fatalf("matchQualityProfile(name) = (%+v, %t), want id 10", profile, ok)
	}
	if _, ok := matchQualityProfile(profiles, "sd"); ok {
		t.Fatal("matchQualityProfile(missing) = true, want false")
	}
}

func TestRadarrJoinAndTitleHelpers(t *testing.T) {
	rootFolders := []*starrradarr.RootFolder{{ID: 1, Path: moviesPathA}, {ID: 2, Path: moviesPathB}}
	profiles := []*starrradarr.QualityProfile{{ID: 10, Name: "HD"}, {ID: 20, Name: "UHD"}}
	if got := joinRootFolderPaths(rootFolders); got != "/movies-a, /movies-b" {
		t.Fatalf("joinRootFolderPaths() = %q", got)
	}
	if got := joinQualityProfileNames(profiles); got != "HD, UHD" {
		t.Fatalf("joinQualityProfileNames() = %q", got)
	}
	if got := normalizeTitle("  Spider-Man: Homecoming! "); got != "spidermanhomecoming" {
		t.Fatalf("normalizeTitle() = %q", got)
	}

	movie := &starrradarr.Movie{Title: "Spider-Man: Homecoming"}
	if !titlesMatch(movie, "Spider Man Homecoming", normalizeTitle("Spider Man Homecoming")) {
		t.Fatal("titlesMatch() = false, want true")
	}
}

func TestFindMovieNotConfigured(t *testing.T) {
	ctx := context.Background()
	if _, err := (*Client)(nil).FindMovie(ctx, "Movie", 1); err == nil {
		t.Fatal("FindMovie() error = nil, want error")
	}
}

func TestFindMovieTMDBHit(t *testing.T) {
	ctx := context.Background()
	client := &Client{api: fakeRadarrAPI{getMovieContext: func(context.Context, *starrradarr.GetMovie) ([]*starrradarr.Movie, error) {
		return []*starrradarr.Movie{{Title: "Movie", TmdbID: 123}}, nil
	}}}
	match, err := client.FindMovie(ctx, "Movie", 123)
	if err != nil {
		t.Fatalf("FindMovie() error = %v", err)
	}
	if match.MatchedBy != "tmdb" || match.Movie.TmdbID != 123 {
		t.Fatalf("FindMovie() = %+v, want tmdb match", match)
	}
}

func TestFindMovieTitleHitWhenTMDBMisses(t *testing.T) {
	ctx := context.Background()
	client := &Client{api: fakeRadarrAPI{getMovieContext: func(_ context.Context, query *starrradarr.GetMovie) ([]*starrradarr.Movie, error) {
		if query != nil && query.TMDBID != 0 {
			return nil, nil
		}
		return []*starrradarr.Movie{{Title: movieTitleLookup}}, nil
	}}}
	match, err := client.FindMovie(ctx, movieTitleLookup, 123)
	if err != nil {
		t.Fatalf("FindMovie() error = %v", err)
	}
	if match.MatchedBy != "title" {
		t.Fatalf("MatchedBy = %q, want title", match.MatchedBy)
	}
}

func TestFindMovieBlankTitleReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	client := &Client{api: fakeRadarrAPI{getMovieContext: func(context.Context, *starrradarr.GetMovie) ([]*starrradarr.Movie, error) { return nil, nil }}}
	_, err := client.FindMovie(ctx, "   ", 123)
	if !errors.Is(err, ErrMovieNotFound) {
		t.Fatalf("FindMovie() error = %v, want ErrMovieNotFound", err)
	}
}

func TestFindMovieWrapsSDKErrors(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("boom")
	client := &Client{api: fakeRadarrAPI{getMovieContext: func(context.Context, *starrradarr.GetMovie) ([]*starrradarr.Movie, error) { return nil, boom }}}
	_, err := client.FindMovie(ctx, "Movie", 123)
	if err == nil || !errors.Is(err, boom) || !strings.Contains(err.Error(), "looking up radarr movie by tmdb id 123") {
		t.Fatalf("FindMovie() error = %v, want wrapped boom", err)
	}
}

func TestResolveAddMovieDefaults(t *testing.T) {
	ctx := context.Background()

	client := &Client{api: fakeRadarrAPI{
		getRootFoldersContext: func(context.Context) ([]*starrradarr.RootFolder, error) {
			return []*starrradarr.RootFolder{{ID: 1, Path: moviesRootPath, Accessible: true}}, nil
		},
		getQualityProfilesContext: func(context.Context) ([]*starrradarr.QualityProfile, error) {
			return []*starrradarr.QualityProfile{{ID: 2, Name: "HD"}}, nil
		},
	}}
	defaults, err := client.ResolveAddMovieDefaults(ctx, "", "")
	if err != nil {
		t.Fatalf("ResolveAddMovieDefaults() error = %v", err)
	}
	if defaults.RootFolderPath != moviesRootPath || defaults.QualityProfileID != 2 || defaults.QualityProfileName != "HD" {
		t.Fatalf("ResolveAddMovieDefaults() = %+v", defaults)
	}

	client = &Client{api: fakeRadarrAPI{getRootFoldersContext: func(context.Context) ([]*starrradarr.RootFolder, error) {
		return []*starrradarr.RootFolder{{ID: 1, Path: moviesPathA, Accessible: true}, {ID: 2, Path: moviesPathB, Accessible: true}}, nil
	}}}
	if _, err := client.resolveRootFolder(ctx, ""); err == nil {
		t.Fatal("resolveRootFolder() error = nil, want error")
	}
	client = &Client{api: fakeRadarrAPI{getRootFoldersContext: func(context.Context) ([]*starrradarr.RootFolder, error) {
		return []*starrradarr.RootFolder{{ID: 1, Path: moviesPathA, Accessible: false}}, nil
	}}}
	if _, err := client.resolveRootFolder(ctx, ""); err == nil {
		t.Fatal("resolveRootFolder() no accessible folders error = nil, want error")
	}
	client = &Client{api: fakeRadarrAPI{getRootFoldersContext: func(context.Context) ([]*starrradarr.RootFolder, error) {
		return []*starrradarr.RootFolder{{ID: 1, Path: moviesPathA, Accessible: true}, {ID: 2, Path: moviesPathB, Accessible: true}}, nil
	}}}
	if folder, err := client.resolveRootFolder(ctx, "2"); err != nil || folder.ID != 2 {
		t.Fatalf("resolveRootFolder(id) = (%+v, %v)", folder, err)
	}
	if folder, err := client.resolveRootFolder(ctx, moviesPathA); err != nil || folder.Path != moviesPathA {
		t.Fatalf("resolveRootFolder(path) = (%+v, %v)", folder, err)
	}

	client = &Client{api: fakeRadarrAPI{getQualityProfilesContext: func(context.Context) ([]*starrradarr.QualityProfile, error) {
		return []*starrradarr.QualityProfile{{ID: 1, Name: "HD"}}, nil
	}}}
	if _, err := client.resolveQualityProfile(ctx, "UHD"); err == nil {
		t.Fatal("resolveQualityProfile() error = nil, want error")
	}
	client = &Client{api: fakeRadarrAPI{getQualityProfilesContext: func(context.Context) ([]*starrradarr.QualityProfile, error) {
		return nil, nil
	}}}
	if _, err := client.resolveQualityProfile(ctx, ""); err == nil {
		t.Fatal("resolveQualityProfile() no profiles error = nil, want error")
	}
	client = &Client{api: fakeRadarrAPI{getQualityProfilesContext: func(context.Context) ([]*starrradarr.QualityProfile, error) {
		return []*starrradarr.QualityProfile{{ID: 1, Name: "HD"}, {ID: 2, Name: "UHD"}}, nil
	}}}
	if profile, err := client.resolveQualityProfile(ctx, "2"); err != nil || profile.ID != 2 {
		t.Fatalf("resolveQualityProfile(id) = (%+v, %v)", profile, err)
	}
	if profile, err := client.resolveQualityProfile(ctx, "hd"); err != nil || profile.Name != "HD" {
		t.Fatalf("resolveQualityProfile(name) = (%+v, %v)", profile, err)
	}
}

func TestCreateMovieNotConfigured(t *testing.T) {
	ctx := context.Background()
	defaults := AddMovieDefaults{RootFolderPath: moviesRootPath, QualityProfileID: 7, QualityProfileName: "HD"}
	if _, err := (*Client)(nil).CreateMovie(ctx, CreateMovieRequest{TMDBID: 1}, defaults); err == nil {
		t.Fatal("CreateMovie() error = nil, want error")
	}
}

func TestCreateMovieMissingTMDBID(t *testing.T) {
	ctx := context.Background()
	defaults := AddMovieDefaults{RootFolderPath: moviesRootPath, QualityProfileID: 7, QualityProfileName: "HD"}
	client := &Client{api: fakeRadarrAPI{}}
	if _, err := client.CreateMovie(ctx, CreateMovieRequest{}, defaults); err == nil {
		t.Fatal("CreateMovie() error = nil, want error")
	}
}

func TestCreateMovieLookupByTMDB(t *testing.T) {
	ctx := context.Background()
	defaults := AddMovieDefaults{RootFolderPath: moviesRootPath, QualityProfileID: 7, QualityProfileName: "HD"}
	var got *starrradarr.AddMovieInput
	client := &Client{api: fakeRadarrAPI{
		lookupTMDBContext: func(context.Context, int64) (*starrradarr.Movie, error) {
			return testMovieCandidate(), nil
		},
		addMovieContext: func(_ context.Context, input *starrradarr.AddMovieInput) (*starrradarr.Movie, error) {
			got = input
			return &starrradarr.Movie{Title: input.Title}, nil
		},
	}}
	movie, err := client.CreateMovie(ctx, CreateMovieRequest{Title: "Movie", TMDBID: 321, SearchForMovie: true}, defaults)
	if err != nil {
		t.Fatalf("CreateMovie() error = %v", err)
	}
	if movie.Title != "Movie" || got == nil || got.RootFolderPath != moviesRootPath || got.QualityProfileID != 7 || !got.AddOptions.SearchForMovie {
		t.Fatalf("CreateMovie() captured input = %+v", got)
	}
}

func TestCreateMovieLookupByTitleAndPreview(t *testing.T) {
	ctx := context.Background()
	defaults := AddMovieDefaults{RootFolderPath: moviesRootPath, QualityProfileID: 7, QualityProfileName: "HD"}
	client := &Client{api: fakeRadarrAPI{
		lookupContext: func(context.Context, string) ([]*starrradarr.Movie, error) {
			return []*starrradarr.Movie{testMovieCandidate()}, nil
		},
		addMovieContext: func(_ context.Context, input *starrradarr.AddMovieInput) (*starrradarr.Movie, error) {
			return &starrradarr.Movie{Title: input.Title}, nil
		},
	}}
	movie, err := client.CreateMovie(ctx, CreateMovieRequest{Title: "Movie", SearchForMovie: true}, defaults)
	if err != nil || movie.Title != "Movie" {
		t.Fatalf("CreateMovie(title lookup) = (%+v, %v)", movie, err)
	}
	title, err := client.PreviewCreateMovie(ctx, CreateMovieRequest{Title: "Movie", SearchForMovie: true}, defaults)
	if err != nil {
		t.Fatalf("PreviewCreateMovie() error = %v", err)
	}
	if title != "Movie" {
		t.Fatalf("PreviewCreateMovie() = %q, want Movie", title)
	}
}

func TestPreviewCreateMovieMissingDefaultsValidation(t *testing.T) {
	ctx := context.Background()
	client := &Client{api: fakeRadarrAPI{lookupTMDBContext: func(context.Context, int64) (*starrradarr.Movie, error) { return testMovieCandidate(), nil }}}
	if _, err := client.PreviewCreateMovie(ctx, CreateMovieRequest{Title: "Movie", TMDBID: 321}, AddMovieDefaults{}); err == nil {
		t.Fatal("PreviewCreateMovie() error = nil, want error")
	}
}

func TestCreateMovieAddFailureWrapped(t *testing.T) {
	ctx := context.Background()
	defaults := AddMovieDefaults{RootFolderPath: moviesRootPath, QualityProfileID: 7, QualityProfileName: "HD"}
	boom := errors.New("boom")
	client := &Client{api: fakeRadarrAPI{
		lookupTMDBContext: func(context.Context, int64) (*starrradarr.Movie, error) { return testMovieCandidate(), nil },
		addMovieContext:   func(context.Context, *starrradarr.AddMovieInput) (*starrradarr.Movie, error) { return nil, boom },
	}}
	if _, err := client.CreateMovie(ctx, CreateMovieRequest{Title: "Movie", TMDBID: 321}, defaults); err == nil || !errors.Is(err, boom) {
		t.Fatalf("CreateMovie() error = %v, want wrapped boom", err)
	}
}

func TestUpdateMovieMonitoringNilMovie(t *testing.T) {
	ctx := context.Background()
	client := &Client{api: fakeRadarrAPI{}}
	if _, _, err := client.UpdateMovieMonitoring(ctx, nil, true); err == nil {
		t.Fatal("UpdateMovieMonitoring() error = nil, want error")
	}
	if _, err := client.PreviewUpdateMovieMonitoring(nil, true); err == nil {
		t.Fatal("PreviewUpdateMovieMonitoring() error = nil, want error")
	}
}

func TestUpdateMovieMonitoringUnchangedReturnsExistingMovie(t *testing.T) {
	ctx := context.Background()
	movie := &starrradarr.Movie{ID: 7, Title: "Movie", Monitored: true}
	client := &Client{api: fakeRadarrAPI{}}
	updated, changed, err := client.UpdateMovieMonitoring(ctx, movie, true)
	if err != nil || changed || updated != movie {
		t.Fatalf("UpdateMovieMonitoring() = (%+v, %t, %v), want unchanged original", updated, changed, err)
	}
	preview, err := client.PreviewUpdateMovieMonitoring(movie, true)
	if err != nil || preview {
		t.Fatalf("PreviewUpdateMovieMonitoring() = (%t, %v), want false nil", preview, err)
	}
}

func TestUpdateMovieMonitoringFetchesStoredMovieAndUpdates(t *testing.T) {
	ctx := context.Background()
	called := false
	client := &Client{api: fakeRadarrAPI{
		getMovieByIDContext: func(context.Context, int64) (*starrradarr.Movie, error) {
			return &starrradarr.Movie{ID: 7, Title: "Movie", Monitored: false}, nil
		},
		updateMovieContext: func(_ context.Context, movieID int64, movie *starrradarr.Movie, moveFiles bool) (*starrradarr.Movie, error) {
			called = true
			if movieID != 7 || !movie.Monitored || moveFiles {
				t.Fatalf("UpdateMovieContext() args = (%d, %+v, %t)", movieID, movie, moveFiles)
			}
			return movie, nil
		},
	}}
	updated, changed, err := client.UpdateMovieMonitoring(ctx, &starrradarr.Movie{ID: 7, Title: "Movie", Monitored: false}, true)
	if err != nil || !changed || !called || !updated.Monitored {
		t.Fatalf("UpdateMovieMonitoring() = (%+v, %t, %v), want updated monitored movie", updated, changed, err)
	}
	preview, err := client.PreviewUpdateMovieMonitoring(&starrradarr.Movie{ID: 7, Title: "Movie", Monitored: false}, true)
	if err != nil || !preview {
		t.Fatalf("PreviewUpdateMovieMonitoring() = (%t, %v), want true nil", preview, err)
	}
}

func TestUpdateMovieMonitoringUpdateFailureWrapped(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("boom")
	client := &Client{api: fakeRadarrAPI{
		getMovieByIDContext: func(context.Context, int64) (*starrradarr.Movie, error) {
			return &starrradarr.Movie{ID: 7, Title: "Movie", Monitored: false}, nil
		},
		updateMovieContext: func(context.Context, int64, *starrradarr.Movie, bool) (*starrradarr.Movie, error) { return nil, boom },
	}}
	if _, _, err := client.UpdateMovieMonitoring(ctx, &starrradarr.Movie{ID: 7, Title: "Movie", Monitored: false}, true); err == nil || !errors.Is(err, boom) {
		t.Fatalf("UpdateMovieMonitoring() error = %v, want wrapped boom", err)
	}
}

func TestSearchMovie(t *testing.T) {
	ctx := context.Background()

	t.Run("missing movie id", func(t *testing.T) {
		client := &Client{api: fakeRadarrAPI{}}
		if err := client.SearchMovie(ctx, 0); err == nil {
			t.Fatal("SearchMovie() error = nil, want error")
		}
	})

	t.Run("command request sent", func(t *testing.T) {
		called := false
		client := &Client{api: fakeRadarrAPI{sendCommandContext: func(_ context.Context, cmd *starrradarr.CommandRequest) (*starrradarr.CommandResponse, error) {
			called = true
			if cmd.Name != commandMovieSearch || len(cmd.MovieIDs) != 1 || cmd.MovieIDs[0] != 7 {
				t.Fatalf("CommandRequest = %+v", cmd)
			}
			return &starrradarr.CommandResponse{}, nil
		}}}
		if err := client.SearchMovie(ctx, 7); err != nil {
			t.Fatalf("SearchMovie() error = %v", err)
		}
		if !called {
			t.Fatal("SendCommandContext was not called")
		}
	})

	t.Run("command error wrapped", func(t *testing.T) {
		boom := errors.New("boom")
		client := &Client{api: fakeRadarrAPI{sendCommandContext: func(context.Context, *starrradarr.CommandRequest) (*starrradarr.CommandResponse, error) {
			return nil, boom
		}}}
		if err := client.SearchMovie(ctx, 7); err == nil || !errors.Is(err, boom) {
			t.Fatalf("SearchMovie() error = %v, want wrapped boom", err)
		}
	})
}

type fakeRadarrAPI struct {
	addMovieContext           func(ctx context.Context, movie *starrradarr.AddMovieInput) (*starrradarr.Movie, error)
	updateMovieContext        func(ctx context.Context, movieID int64, movie *starrradarr.Movie, moveFiles bool) (*starrradarr.Movie, error)
	sendCommandContext        func(ctx context.Context, cmd *starrradarr.CommandRequest) (*starrradarr.CommandResponse, error)
	getMovieContext           func(ctx context.Context, getMovie *starrradarr.GetMovie) ([]*starrradarr.Movie, error)
	getMovieByIDContext       func(ctx context.Context, movieID int64) (*starrradarr.Movie, error)
	lookupTMDBContext         func(ctx context.Context, tmdbID int64) (*starrradarr.Movie, error)
	lookupContext             func(ctx context.Context, term string) ([]*starrradarr.Movie, error)
	getRootFoldersContext     func(ctx context.Context) ([]*starrradarr.RootFolder, error)
	getQualityProfilesContext func(ctx context.Context) ([]*starrradarr.QualityProfile, error)
}

func (f fakeRadarrAPI) AddMovieContext(ctx context.Context, movie *starrradarr.AddMovieInput) (*starrradarr.Movie, error) {
	if f.addMovieContext == nil {
		return nil, nil
	}
	return f.addMovieContext(ctx, movie)
}

func (f fakeRadarrAPI) UpdateMovieContext(ctx context.Context, movieID int64, movie *starrradarr.Movie, moveFiles bool) (*starrradarr.Movie, error) {
	if f.updateMovieContext == nil {
		return nil, nil
	}
	return f.updateMovieContext(ctx, movieID, movie, moveFiles)
}

func (f fakeRadarrAPI) SendCommandContext(ctx context.Context, cmd *starrradarr.CommandRequest) (*starrradarr.CommandResponse, error) {
	if f.sendCommandContext == nil {
		return nil, nil
	}
	return f.sendCommandContext(ctx, cmd)
}

func (f fakeRadarrAPI) GetMovieContext(ctx context.Context, getMovie *starrradarr.GetMovie) ([]*starrradarr.Movie, error) {
	if f.getMovieContext == nil {
		return nil, nil
	}
	return f.getMovieContext(ctx, getMovie)
}

func (f fakeRadarrAPI) GetMovieByIDContext(ctx context.Context, movieID int64) (*starrradarr.Movie, error) {
	if f.getMovieByIDContext == nil {
		return nil, nil
	}
	return f.getMovieByIDContext(ctx, movieID)
}

func (f fakeRadarrAPI) LookupTMDBContext(ctx context.Context, tmdbID int64) (*starrradarr.Movie, error) {
	if f.lookupTMDBContext == nil {
		return nil, nil
	}
	return f.lookupTMDBContext(ctx, tmdbID)
}

func (f fakeRadarrAPI) LookupContext(ctx context.Context, term string) ([]*starrradarr.Movie, error) {
	if f.lookupContext == nil {
		return nil, nil
	}
	return f.lookupContext(ctx, term)
}

func (f fakeRadarrAPI) GetRootFoldersContext(ctx context.Context) ([]*starrradarr.RootFolder, error) {
	if f.getRootFoldersContext == nil {
		return nil, nil
	}
	return f.getRootFoldersContext(ctx)
}

func (f fakeRadarrAPI) GetQualityProfilesContext(ctx context.Context) ([]*starrradarr.QualityProfile, error) {
	if f.getQualityProfilesContext == nil {
		return nil, nil
	}
	return f.getQualityProfilesContext(ctx)
}

func testMovieCandidate() *starrradarr.Movie {
	return &starrradarr.Movie{
		Title:               "Movie",
		TitleSlug:           "movie",
		TmdbID:              321,
		MinimumAvailability: "released",
		Year:                2025,
	}
}
