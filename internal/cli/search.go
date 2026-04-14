package cli

import (
	"context"
	"fmt"
	"log"

	"github.com/KHAEntertainment/markedup/cache"
	"github.com/KHAEntertainment/markedup/embed"
	"github.com/KHAEntertainment/markedup/index"
	"github.com/KHAEntertainment/markedup/rerank"
	"github.com/spf13/cobra"
)

var (
	searchSemantic    bool
	searchRerank      bool
	searchRerankFmt   string
)

func newSearchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "search [path] <query>",
		Short: "Search the knowledge base",
		Long:  "Loads the knowledge base and runs a scored search against all pages.",
		Args:  cobra.RangeArgs(1, 2),
		RunE:  runSearch,
	}

	cmd.Flags().BoolVar(&searchSemantic, "semantic", false,
		"Enable semantic search using cached embeddings (requires prior embedding)")
	cmd.Flags().BoolVar(&searchRerank, "rerank", false,
		"Re-rank results using a cross-encoder model (requires endpoint config)")
	cmd.Flags().StringVar(&searchRerankFmt, "rerank-format", "jina",
		`Reranker API format: "jina" (default, also correct for TEI) or "openai"`)

	return cmd
}

func runSearch(cmd *cobra.Command, args []string) error {
	var path, query string
	if len(args) == 1 {
		path = "."
		query = args[0]
	} else {
		path = args[0]
		query = args[1]
	}

	ctx := context.Background()

	result, err := index.Load(ctx, path, index.WithIgnoreErrors(true))
	if err != nil {
		return fmt.Errorf("failed to load %s: %w", path, err)
	}

	var searchOpts []index.SearchOption
	searchOpts = append(searchOpts, index.WithContext(ctx))

	if searchSemantic {
		if appConfig.Embed.Endpoint == "" || appConfig.Embed.Model == "" {
			log.Println("search: --semantic requires embed endpoint and model (set via config or MARKEDUP_EMBED_* env vars)")
		} else {
			emb := embed.NewOpenAICompatible(embed.Config{
				Endpoint:  appConfig.Embed.Endpoint,
				ModelName: appConfig.Embed.Model,
				APIKey:    appConfig.Embed.APIKey,
			})
			vc := cache.NewVectorCache(path)
			searchOpts = append(searchOpts,
				index.WithEmbedder(emb),
				index.WithVectorCache(vc),
			)
		}
	}

	if searchRerank {
		if appConfig.Rerank.Endpoint == "" || appConfig.Rerank.Model == "" {
			log.Println("search: --rerank requires rerank endpoint and model (set via config or MARKEDUP_RERANK_* env vars)")
		} else {
			rr := rerank.NewCrossEncoder(rerank.Config{
				Endpoint: appConfig.Rerank.Endpoint,
				Model:    appConfig.Rerank.Model,
				APIKey:   appConfig.Rerank.APIKey,
				Format:   parseRerankFormat(searchRerankFmt),
			})
			searchOpts = append(searchOpts, index.WithReranker(rr))
		}
	}

	results := index.Search(result.Index, query, searchOpts...)
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, formatResults(results, currentFormat()))

	if !jsonOutput && len(results) > 0 {
		fmt.Fprintln(out, "\nUse `markedup show <id>` to view a page.")
	}

	return nil
}
