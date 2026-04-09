package plex

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
)

const (
	acceptHeader    = "Accept"
	jsonMimeType    = "application/json"
	plexTokenHeader = "X-Plex-Token"
)

// Item represents a minimal Plex media item from a collection.
type Item struct {
	RatingKey       string
	Title           string
	Type            string // "show" or "movie"
	ParentTitle     string
	ParentRatingKey string
	Index           int
	TVDBID          int64
	TMDBID          int64
	ShowTVDBID      int64
	ShowTMDBID      int64
}

// Client wraps Plex Media Server API calls for the operations needed by this service.
type Client struct {
	serverURL string
	token     string
	doer      httpDoer
}

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// New creates a Plex client targeting the given server URL with the provided token.
func New(serverURL, token string) *Client {
	return &Client{serverURL: strings.TrimRight(serverURL, "/"), token: token, doer: http.DefaultClient}
}

func (c *Client) doerOrDefault() httpDoer {
	if c != nil && c.doer != nil {
		return c.doer
	}
	return http.DefaultClient
}

// section is a minimal representation of a Plex library section used for
// listing section IDs. This avoids the plexgo SDK's GetSections which has
// deserialization issues with the allowSync union field.
type section struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Type  string `json:"type"`
}

type sectionsResponse struct {
	MediaContainer struct {
		Directory []section `json:"Directory"`
	} `json:"MediaContainer"`
}

type externalIDs struct {
	tvdb int64
	tmdb int64
}

type collectionMetadata struct {
	RatingKey       string          `json:"ratingKey"`
	Title           string          `json:"title"`
	Type            string          `json:"type"`
	ParentTitle     string          `json:"parentTitle"`
	ParentRatingKey string          `json:"parentRatingKey"`
	Index           int             `json:"index"`
	GUID            json.RawMessage `json:"Guid"`
}

// listSections fetches library sections via a direct HTTP call.
func (c *Client) listSections(ctx context.Context) ([]section, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.serverURL+"/library/sections", nil)
	if err != nil {
		return nil, err
	}
	setHeaders(req, c.token)

	resp, err := c.doerOrDefault().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var body sectionsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body.MediaContainer.Directory, nil
}

type collectionsResponse struct {
	MediaContainer struct {
		Metadata []struct {
			RatingKey string `json:"ratingKey"`
			Title     string `json:"title"`
		} `json:"Metadata"`
	} `json:"MediaContainer"`
}

// FindCollectionByName searches all library sections for a collection with the
// given title and returns its rating key. Returns an error if no match is found.
func (c *Client) FindCollectionByName(ctx context.Context, name string) (string, error) {
	sections, err := c.listSections(ctx)
	if err != nil {
		return "", fmt.Errorf("listing library sections: %w", err)
	}

	for _, sec := range sections {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.serverURL+"/library/sections/"+sec.Key+"/collections", nil)
		if err != nil {
			return "", fmt.Errorf("building request: %w", err)
		}
		setHeaders(req, c.token)

		resp, err := c.doerOrDefault().Do(req)
		if err != nil {
			slog.Warn("listing collections for section failed", "section", sec.Key, "error", err)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			slog.Warn("listing collections for section returned unexpected status", "section", sec.Key, "status", resp.StatusCode)
			continue
		}

		var body collectionsResponse
		err = json.NewDecoder(resp.Body).Decode(&body)
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Warn("closing collections response failed", "section", sec.Key, "error", closeErr)
		}
		if err != nil {
			slog.Warn("decoding collections response failed", "section", sec.Key, "error", err)
			continue
		}

		for _, coll := range body.MediaContainer.Metadata {
			if coll.Title == name {
				slog.Info("found collection", "name", name, "ratingKey", coll.RatingKey, "section", sec.Key)
				return coll.RatingKey, nil
			}
		}
	}

	return "", fmt.Errorf("collection %q not found in any library section", name)
}

// collectionItemsResponse is a minimal representation of the Plex
// /library/collections/{id}/items response. Using a custom struct avoids
// union-type deserialization bugs in the plexgo SDK.
type collectionItemsResponse struct {
	MediaContainer struct {
		Metadata []collectionMetadata `json:"Metadata"`
	} `json:"MediaContainer"`
}

type guidEntry struct {
	ID string `json:"id"`
}

// parseGuids handles the Guid field which can be either a JSON array of
// {id: "..."} objects or a plain string in older Plex responses.
func parseGuids(raw json.RawMessage) []guidEntry {
	if len(raw) == 0 {
		return nil
	}
	var arr []guidEntry
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}
	// Older Plex versions may return a single string; ignore it since it
	// uses the legacy agent-based format without tvdb/tmdb prefixes.
	return nil
}

// extractIDs parses tvdb and tmdb identifiers from a set of Plex GUID entries.
func extractIDs(guids []guidEntry) (tvdb, tmdb int64) {
	for _, guid := range guids {
		provider, idStr, ok := strings.Cut(guid.ID, "://")
		if !ok {
			continue
		}
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			continue
		}
		switch provider {
		case "tvdb":
			tvdb = id
		case "tmdb":
			tmdb = id
		}
	}
	return
}

type metadataResponse struct {
	MediaContainer struct {
		Metadata []struct {
			GUID json.RawMessage `json:"Guid"`
		} `json:"Metadata"`
	} `json:"MediaContainer"`
}

// getExternalIDs fetches the full metadata for a single item and extracts TVDB/TMDB IDs.
func (c *Client) getExternalIDs(ctx context.Context, ratingKey string) (tvdb, tmdb int64, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.serverURL+"/library/metadata/"+ratingKey, nil)
	if err != nil {
		return 0, 0, err
	}
	setHeaders(req, c.token)

	resp, err := c.doerOrDefault().Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var body metadataResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, 0, err
	}
	if len(body.MediaContainer.Metadata) == 0 {
		return 0, 0, nil
	}
	tvdb, tmdb = extractIDs(parseGuids(body.MediaContainer.Metadata[0].GUID))
	return tvdb, tmdb, nil
}

// GetCollectionItems returns the media items in the Plex collection identified by ratingKey.
func (c *Client) GetCollectionItems(ctx context.Context, collectionKey string) ([]Item, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.serverURL+"/library/collections/"+collectionKey+"/items", nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	setHeaders(req, c.token)

	resp, err := c.doerOrDefault().Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching collection items: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d for collection %s", resp.StatusCode, collectionKey)
	}

	var body collectionItemsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	parentIDs := make(map[string]externalIDs)
	var items []Item
	for _, meta := range body.MediaContainer.Metadata {
		item := c.newCollectionItem(ctx, meta, parentIDs)
		slog.Debug("collection item", "title", item.Title, "type", item.Type, "tvdb", item.TVDBID, "tmdb", item.TMDBID)
		items = append(items, item)
	}

	return items, nil
}

func setHeaders(req *http.Request, token string) {
	req.Header.Set(acceptHeader, jsonMimeType)
	req.Header.Set(plexTokenHeader, token)
}

func (c *Client) newCollectionItem(ctx context.Context, meta collectionMetadata, parentIDs map[string]externalIDs) Item {
	item := Item{
		RatingKey:       meta.RatingKey,
		Title:           meta.Title,
		Type:            meta.Type,
		ParentTitle:     meta.ParentTitle,
		ParentRatingKey: meta.ParentRatingKey,
		Index:           meta.Index,
	}

	ids := c.resolveExternalIDs(ctx, item.RatingKey, meta.GUID, "item")
	item.TVDBID = ids.tvdb
	item.TMDBID = ids.tmdb
	item.ShowTVDBID = ids.tvdb
	item.ShowTMDBID = ids.tmdb

	if item.Type == "season" {
		parent := c.resolveParentIDs(ctx, item.ParentRatingKey, parentIDs)
		item.ShowTVDBID = parent.tvdb
		item.ShowTMDBID = parent.tmdb
	}

	return item
}

func (c *Client) resolveExternalIDs(ctx context.Context, ratingKey string, guid json.RawMessage, lookupType string) externalIDs {
	tvdb, tmdb := extractIDs(parseGuids(guid))
	if tvdb != 0 || tmdb != 0 || ratingKey == "" {
		return externalIDs{tvdb: tvdb, tmdb: tmdb}
	}

	tvdb, tmdb, err := c.getExternalIDs(ctx, ratingKey)
	if err != nil {
		slog.Warn("failed to fetch metadata", "lookupType", lookupType, "ratingKey", ratingKey, "error", err)
		return externalIDs{}
	}

	return externalIDs{tvdb: tvdb, tmdb: tmdb}
}

func (c *Client) resolveParentIDs(ctx context.Context, parentRatingKey string, parentIDs map[string]externalIDs) externalIDs {
	if parentRatingKey == "" {
		return externalIDs{}
	}

	if ids, ok := parentIDs[parentRatingKey]; ok {
		return ids
	}

	ids := c.resolveExternalIDs(ctx, parentRatingKey, nil, "parent")
	parentIDs[parentRatingKey] = ids
	return ids
}
