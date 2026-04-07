package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"text/tabwriter"

	"github.com/alecthomas/kong"
	"github.com/enddzone/maintainerr-bridge/internal/config"
	"github.com/enddzone/maintainerr-bridge/internal/plex"
)

type CLI struct {
	List ListCmd `cmd:"" help:"List items in a Plex collection."`
}

type ListCmd struct {
	Movies ListMoviesCmd `cmd:"" help:"List movies in the movie collection."`
	TV     ListTVCmd     `cmd:"" help:"List TV shows/seasons in the TV collection."`
}

type ListMoviesCmd struct{}

func (c *ListMoviesCmd) Run(p *plexDeps) error {
	if p.cfg.MovieCollectionName == "" {
		return fmt.Errorf("PLEX_MOVIE_COLLECTION is not set")
	}

	ctx := context.Background()
	items, err := p.resolveCollection(ctx, p.cfg.MovieCollectionName)
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "#\tTITLE\tTMDB\tTVDB\tRATING KEY\n")
	for i, item := range items {
		fmt.Fprintf(w, "%d\t%s\t%d\t%d\t%s\n", i+1, item.Title, item.TMDBID, item.TVDBID, item.RatingKey)
	}
	w.Flush()
	fmt.Printf("\nTotal: %d movies\n", len(items))
	return nil
}

type ListTVCmd struct{}

func (c *ListTVCmd) Run(p *plexDeps) error {
	if p.cfg.TVCollectionName == "" {
		return fmt.Errorf("PLEX_TV_COLLECTION is not set")
	}

	ctx := context.Background()
	items, err := p.resolveCollection(ctx, p.cfg.TVCollectionName)
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "#\tTITLE\tTYPE\tTMDB\tTVDB\tRATING KEY\n")
	for i, item := range items {
		fmt.Fprintf(w, "%d\t%s\t%s\t%d\t%d\t%s\n", i+1, item.Title, item.Type, item.TMDBID, item.TVDBID, item.RatingKey)
	}
	w.Flush()
	fmt.Printf("\nTotal: %d items\n", len(items))
	return nil
}

// plexDeps holds shared dependencies injected via kong.Bind.
type plexDeps struct {
	cfg  *config.Config
	plex *plex.Client
}

func (p *plexDeps) resolveCollection(ctx context.Context, name string) ([]plex.Item, error) {
	ratingKey, err := p.plex.FindCollectionByName(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("finding collection %q: %w", name, err)
	}
	items, err := p.plex.GetCollectionItems(ctx, ratingKey)
	if err != nil {
		return nil, fmt.Errorf("getting items for %q: %w", name, err)
	}
	return items, nil
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}

	deps := &plexDeps{
		cfg:  cfg,
		plex: plex.New(cfg.PlexURL, cfg.PlexToken),
	}

	var cli CLI
	ctx := kong.Parse(&cli,
		kong.Name("maintainerr-bridge"),
		kong.Description("Bridge between Plex collections and *arr apps."),
		kong.UsageOnError(),
		kong.Bind(deps),
	)
	if err := ctx.Run(deps); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
