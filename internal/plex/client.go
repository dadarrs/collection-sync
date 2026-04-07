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

// Item represents a minimal Plex media item from a collection.
type Item struct {
	RatingKey string
	Title     string
	Type      string // "show" or "movie"
	TVDBID    int64
	TMDBID    int64
}

// Client wraps Plex Media Server API calls for the operations needed by this service.
type Client struct {
	serverURL string
	token     string
}

// New creates a Plex client targeting the given server URL with the provided token.
func New(serverURL, token string) *Client {
	return &Client{serverURL: strings.TrimRight(serverURL, "/"), token: token}
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

// listSections fetches library sections via a direct HTTP call.
func (c *Client) listSections(ctx context.Context) ([]section, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.serverURL+"/library/sections", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Token", c.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

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
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-Plex-Token", c.token)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			slog.Warn("listing collections for section failed", "section", sec.Key, "error", err)
			continue
		}

		var body collectionsResponse
		err = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
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
		Metadata []struct {
			RatingKey string          `json:"ratingKey"`
			Title     string          `json:"title"`
			Type      string          `json:"type"`
			Guid      json.RawMessage `json:"Guid"`
		} `json:"Metadata"`
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
			Guid json.RawMessage `json:"Guid"`
		} `json:"Metadata"`
	} `json:"MediaContainer"`
}

// getExternalIDs fetches the full metadata for a single item and extracts TVDB/TMDB IDs.
func (c *Client) getExternalIDs(ctx context.Context, ratingKey string) (tvdb, tmdb int64, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.serverURL+"/library/metadata/"+ratingKey, nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Token", c.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()

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
	tvdb, tmdb = extractIDs(parseGuids(body.MediaContainer.Metadata[0].Guid))
	return tvdb, tmdb, nil
}

// GetCollectionItems returns the media items in the Plex collection identified by ratingKey.
func (c *Client) GetCollectionItems(ctx context.Context, collectionKey string) ([]Item, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.serverURL+"/library/collections/"+collectionKey+"/items", nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Token", c.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching collection items: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d for collection %s", resp.StatusCode, collectionKey)
	}

	var body collectionItemsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	var items []Item
	for _, meta := range body.MediaContainer.Metadata {
		item := Item{
			RatingKey: meta.RatingKey,
			Title:     meta.Title,
			Type:      meta.Type,
		}

		// The collection list endpoint includes Guid only when available.
		item.TVDBID, item.TMDBID = extractIDs(parseGuids(meta.Guid))

		// If no external IDs were found, fetch the full metadata for this item.
		if item.TVDBID == 0 && item.TMDBID == 0 && item.RatingKey != "" {
			tvdb, tmdb, err := c.getExternalIDs(ctx, item.RatingKey)
			if err != nil {
				slog.Warn("failed to fetch metadata for item", "ratingKey", item.RatingKey, "error", err)
			} else {
				item.TVDBID = tvdb
				item.TMDBID = tmdb
			}
		}

		slog.Debug("collection item", "title", item.Title, "type", item.Type, "tvdb", item.TVDBID, "tmdb", item.TMDBID)
		items = append(items, item)
	}

	return items, nil
}
