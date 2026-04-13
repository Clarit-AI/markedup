package cli

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/KHAEntertainment/markedup/cache"
	"github.com/KHAEntertainment/markedup/embed"
	"github.com/KHAEntertainment/markedup/index"
	"github.com/KHAEntertainment/markedup/rerank"
	"github.com/spf13/cobra"
)

var (
	searchSemantic bool
	searchRerank   bool
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
		embedEndpoint := os.Getenv("MARKEDUP_EMBED_ENDPOINT")
		embedModel := os.Getenv("MARKEDUP_EMBED_MODEL")
		embedAPIKey := os.Getenv("MARKEDUP_EMBED_API_KEY")

		if embedEndpoint == "" || embedModel == "" {
			log.Println("search: --semantic requires MARKEDUP_EMBED_ENDPOINT and MARKEDUP_EMBED_MODEL env vars")
		} else {
			emb := embed.NewOpenAICompatible(embed.Config{
				Endpoint:  embedEndpoint,
				ModelName: embedModel,
				APIKey:    embedAPIKey,
			})
			vc := cache.NewVectorCache(path)
			searchOpts = append(searchOpts,
				index.WithEmbedder(emb),
				index.WithVectorCache(vc),
			)
		}
	}

	if searchRerank {
		rerankEndpoint := os.Getenv("MARKEDUP_RERANK_ENDPOINT")
		rerankModel := os.Getenv("MARKEDUP_RERANK_MODEL")
		rerankAPIKey := os.Getenv("MARKEDUP_RERANK_API_KEY")

		if rerankEndpoint == "" || rerankModel == "" {
			log.Println("search: --rerank requires MARKEDUP_RERANK_ENDPOINT and MARKEDUP_RERANK_MODEL env vars")
		} else {
			rr := rerank.NewCrossEncoder(rerank.Config{
				Endpoint: rerankEndpoint,
				Model:    rerankModel,
				APIKey:   rerankAPIKey,
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
