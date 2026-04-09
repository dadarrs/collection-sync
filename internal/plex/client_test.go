package plex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestNewTrimsTrailingSlash(t *testing.T) {
	client := New("http://plex/", "token")
	if client.serverURL != "http://plex" {
		t.Fatalf("serverURL = %q, want %q", client.serverURL, "http://plex")
	}
}

func TestParseGuids(t *testing.T) {
	tests := []struct {
		name string
		raw  json.RawMessage
		want int
	}{
		{name: "valid json array", raw: json.RawMessage(`[{"id":"tvdb://123"},{"id":"tmdb://456"}]`), want: 2},
		{name: "empty payload", raw: nil, want: 0},
		{name: "legacy string ignored", raw: json.RawMessage(`"com.plexapp.agents.thetvdb://123"`), want: 0},
		{name: "invalid json ignored", raw: json.RawMessage(`{`), want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := len(parseGuids(tt.raw)); got != tt.want {
				t.Fatalf("len(parseGuids(%s)) = %d, want %d", tt.raw, got, tt.want)
			}
		})
	}
}

func TestExtractIDs(t *testing.T) {
	tests := []struct {
		name     string
		guids    []guidEntry
		wantTVDB int64
		wantTMDB int64
	}{
		{name: "tvdb only", guids: []guidEntry{{ID: "tvdb://123"}}, wantTVDB: 123},
		{name: "tmdb only", guids: []guidEntry{{ID: "tmdb://456"}}, wantTMDB: 456},
		{name: "both", guids: []guidEntry{{ID: "tvdb://123"}, {ID: "tmdb://456"}}, wantTVDB: 123, wantTMDB: 456},
		{name: "malformed ignored", guids: []guidEntry{{ID: "tvdb://abc"}}, wantTVDB: 0},
		{name: "unknown provider ignored", guids: []guidEntry{{ID: "imdb://789"}}, wantTMDB: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTVDB, gotTMDB := extractIDs(tt.guids)
			if gotTVDB != tt.wantTVDB || gotTMDB != tt.wantTMDB {
				t.Fatalf("extractIDs() = (%d, %d), want (%d, %d)", gotTVDB, gotTMDB, tt.wantTVDB, tt.wantTMDB)
			}
		})
	}
}

func TestSetHeaders(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://plex", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}

	setHeaders(req, "token")

	if got := req.Header.Get(acceptHeader); got != jsonMimeType {
		t.Fatalf("Accept header = %q, want %q", got, jsonMimeType)
	}
	if got := req.Header.Get(plexTokenHeader); got != "token" {
		t.Fatalf("X-Plex-Token header = %q, want token", got)
	}
}

func TestResolveExternalIDs(t *testing.T) {
	t.Run("uses guid data directly", func(t *testing.T) {
		client := &Client{}
		ids := client.resolveExternalIDs(context.Background(), "123", json.RawMessage(`[{"id":"tvdb://11"},{"id":"tmdb://22"}]`), "item")
		if ids.tvdb != 11 || ids.tmdb != 22 {
			t.Fatalf("resolveExternalIDs() = %+v, want tvdb=11 tmdb=22", ids)
		}
	})

	t.Run("falls back to metadata lookup and ignores failures", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/library/metadata/123":
				writeJSON(t, w, metadataResponse{MediaContainer: struct {
					Metadata []struct {
						GUID json.RawMessage `json:"Guid"`
					} `json:"Metadata"`
				}{Metadata: []struct {
					GUID json.RawMessage `json:"Guid"`
				}{{GUID: json.RawMessage(`[{"id":"tvdb://33"},{"id":"tmdb://44"}]`)}}}})
			case "/library/metadata/124":
				http.Error(w, "boom", http.StatusBadGateway)
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		client := &Client{serverURL: server.URL, token: "token", doer: server.Client()}
		ids := client.resolveExternalIDs(context.Background(), "123", nil, "item")
		if ids.tvdb != 33 || ids.tmdb != 44 {
			t.Fatalf("resolveExternalIDs() = %+v, want tvdb=33 tmdb=44", ids)
		}

		failed := client.resolveExternalIDs(context.Background(), "124", nil, "item")
		if failed.tvdb != 0 || failed.tmdb != 0 {
			t.Fatalf("resolveExternalIDs() failure = %+v, want zero ids", failed)
		}
	})
}

func TestResolveParentIDsUsesCache(t *testing.T) {
	var mu sync.Mutex
	requests := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests[r.URL.Path]++
		mu.Unlock()

		writeJSON(t, w, metadataResponse{MediaContainer: struct {
			Metadata []struct {
				GUID json.RawMessage `json:"Guid"`
			} `json:"Metadata"`
		}{Metadata: []struct {
			GUID json.RawMessage `json:"Guid"`
		}{{GUID: json.RawMessage(`[{"id":"tvdb://55"},{"id":"tmdb://66"}]`)}}}})
	}))
	defer server.Close()

	client := &Client{serverURL: server.URL, token: "token", doer: server.Client()}
	cache := map[string]externalIDs{}

	if ids := client.resolveParentIDs(context.Background(), "", cache); ids.tvdb != 0 || ids.tmdb != 0 {
		t.Fatalf("resolveParentIDs(empty) = %+v, want zero ids", ids)
	}

	first := client.resolveParentIDs(context.Background(), "parent-1", cache)
	second := client.resolveParentIDs(context.Background(), "parent-1", cache)
	if first != second {
		t.Fatalf("resolveParentIDs cache mismatch: first=%+v second=%+v", first, second)
	}
	if first.tvdb != 55 || first.tmdb != 66 {
		t.Fatalf("resolveParentIDs() = %+v, want tvdb=55 tmdb=66", first)
	}

	mu.Lock()
	count := requests["/library/metadata/parent-1"]
	mu.Unlock()
	if count != 1 {
		t.Fatalf("metadata lookup count = %d, want 1", count)
	}
}

func TestNewCollectionItemSeasonUsesParentIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/library/metadata/show-key":
			writeJSON(t, w, metadataResponse{MediaContainer: struct {
				Metadata []struct {
					GUID json.RawMessage `json:"Guid"`
				} `json:"Metadata"`
			}{Metadata: []struct {
				GUID json.RawMessage `json:"Guid"`
			}{{GUID: json.RawMessage(`[{"id":"tvdb://333"},{"id":"tmdb://444"}]`)}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &Client{serverURL: server.URL, token: "token", doer: server.Client()}
	item := client.newCollectionItem(context.Background(), collectionMetadata{
		RatingKey:       "season-key",
		Title:           "Season 1",
		Type:            "season",
		ParentTitle:     "Show",
		ParentRatingKey: "show-key",
		Index:           1,
		GUID:            json.RawMessage(`[{"id":"tvdb://111"},{"id":"tmdb://222"}]`),
	}, map[string]externalIDs{})

	if item.TVDBID != 111 || item.TMDBID != 222 {
		t.Fatalf("item ids = (%d, %d), want (111, 222)", item.TVDBID, item.TMDBID)
	}
	if item.ShowTVDBID != 333 || item.ShowTMDBID != 444 {
		t.Fatalf("show ids = (%d, %d), want (333, 444)", item.ShowTVDBID, item.ShowTMDBID)
	}
}

func TestListSections(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantErr    bool
		wantCount  int
	}{
		{name: "success", statusCode: http.StatusOK, body: `{"MediaContainer":{"Directory":[{"key":"1","title":"TV","type":"show"}]}}`, wantCount: 1},
		{name: "non-200", statusCode: http.StatusBadGateway, body: `oops`, wantErr: true},
		{name: "invalid json", statusCode: http.StatusOK, body: `{`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			client := &Client{serverURL: server.URL, token: "token", doer: server.Client()}
			sections, err := client.listSections(context.Background())
			if tt.wantErr {
				if err == nil {
					t.Fatal("listSections() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("listSections() error = %v", err)
			}
			if len(sections) != tt.wantCount {
				t.Fatalf("len(listSections()) = %d, want %d", len(sections), tt.wantCount)
			}
		})
	}
}

func TestFindCollectionByName(t *testing.T) {
	t.Run("finds collection after skipped section failures", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/library/sections":
				_, _ = w.Write([]byte(`{"MediaContainer":{"Directory":[{"key":"1"},{"key":"2"},{"key":"3"}]}}`))
			case "/library/sections/1/collections":
				http.Error(w, "bad", http.StatusBadGateway)
			case "/library/sections/2/collections":
				_, _ = w.Write([]byte(`{`))
			case "/library/sections/3/collections":
				_, _ = w.Write([]byte(`{"MediaContainer":{"Metadata":[{"ratingKey":"99","title":"Wanted"}]}}`))
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		client := &Client{serverURL: server.URL, token: "token", doer: server.Client()}
		key, err := client.FindCollectionByName(context.Background(), "Wanted")
		if err != nil {
			t.Fatalf("FindCollectionByName() error = %v", err)
		}
		if key != "99" {
			t.Fatalf("FindCollectionByName() = %q, want 99", key)
		}
	})

	t.Run("returns not found when absent", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/library/sections":
				_, _ = w.Write([]byte(`{"MediaContainer":{"Directory":[{"key":"1"}]}}`))
			case "/library/sections/1/collections":
				_, _ = w.Write([]byte(`{"MediaContainer":{"Metadata":[{"ratingKey":"1","title":"Other"}]}}`))
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		client := &Client{serverURL: server.URL, token: "token", doer: server.Client()}
		if _, err := client.FindCollectionByName(context.Background(), "Missing"); err == nil {
			t.Fatal("FindCollectionByName() error = nil, want error")
		}
	})
}

func TestGetExternalIDs(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantTVDB   int64
		wantTMDB   int64
		wantErr    bool
	}{
		{name: "metadata found", statusCode: http.StatusOK, body: `{"MediaContainer":{"Metadata":[{"Guid":[{"id":"tvdb://10"},{"id":"tmdb://20"}]}]}}`, wantTVDB: 10, wantTMDB: 20},
		{name: "empty metadata", statusCode: http.StatusOK, body: `{"MediaContainer":{"Metadata":[]}}`},
		{name: "non-200", statusCode: http.StatusBadGateway, body: `oops`, wantErr: true},
		{name: "invalid json", statusCode: http.StatusOK, body: `{`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			client := &Client{serverURL: server.URL, token: "token", doer: server.Client()}
			gotTVDB, gotTMDB, err := client.getExternalIDs(context.Background(), "123")
			if tt.wantErr {
				if err == nil {
					t.Fatal("getExternalIDs() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("getExternalIDs() error = %v", err)
			}
			if gotTVDB != tt.wantTVDB || gotTMDB != tt.wantTMDB {
				t.Fatalf("getExternalIDs() = (%d, %d), want (%d, %d)", gotTVDB, gotTMDB, tt.wantTVDB, tt.wantTMDB)
			}
		})
	}
}

func TestGetCollectionItems(t *testing.T) {
	t.Run("parses mixed items with fallback metadata", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/library/collections/collection-1/items":
				_, _ = w.Write([]byte(`{"MediaContainer":{"Metadata":[
					{"ratingKey":"movie-1","title":"Movie One","type":"movie","Guid":[{"id":"tmdb://101"}]},
					{"ratingKey":"show-1","title":"Show One","type":"show","Guid":[{"id":"tvdb://202"}]},
					{"ratingKey":"season-1","title":"Season 1","type":"season","parentTitle":"Show One","parentRatingKey":"show-parent","index":1}
				]}}`))
			case "/library/metadata/season-1":
				_, _ = w.Write([]byte(`{"MediaContainer":{"Metadata":[{"Guid":[{"id":"tvdb://303"},{"id":"tmdb://404"}]}]}}`))
			case "/library/metadata/show-parent":
				_, _ = w.Write([]byte(`{"MediaContainer":{"Metadata":[{"Guid":[{"id":"tvdb://505"},{"id":"tmdb://606"}]}]}}`))
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		client := &Client{serverURL: server.URL, token: "token", doer: server.Client()}
		items, err := client.GetCollectionItems(context.Background(), "collection-1")
		if err != nil {
			t.Fatalf("GetCollectionItems() error = %v", err)
		}
		if len(items) != 3 {
			t.Fatalf("len(GetCollectionItems()) = %d, want 3", len(items))
		}
		if items[0].TMDBID != 101 {
			t.Fatalf("movie TMDBID = %d, want 101", items[0].TMDBID)
		}
		if items[1].TVDBID != 202 {
			t.Fatalf("show TVDBID = %d, want 202", items[1].TVDBID)
		}
		if items[2].TVDBID != 303 || items[2].TMDBID != 404 {
			t.Fatalf("season item ids = (%d, %d), want (303, 404)", items[2].TVDBID, items[2].TMDBID)
		}
		if items[2].ShowTVDBID != 505 || items[2].ShowTMDBID != 606 {
			t.Fatalf("season parent ids = (%d, %d), want (505, 606)", items[2].ShowTVDBID, items[2].ShowTMDBID)
		}
	})

	t.Run("non-200 response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "bad", http.StatusBadGateway)
		}))
		defer server.Close()

		client := &Client{serverURL: server.URL, token: "token", doer: server.Client()}
		if _, err := client.GetCollectionItems(context.Background(), "collection-1"); err == nil {
			t.Fatal("GetCollectionItems() error = nil, want error")
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{`))
		}))
		defer server.Close()

		client := &Client{serverURL: server.URL, token: "token", doer: server.Client()}
		if _, err := client.GetCollectionItems(context.Background(), "collection-1"); err == nil {
			t.Fatal("GetCollectionItems() error = nil, want error")
		}
	})
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
}
