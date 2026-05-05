package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/analysis/community"
	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/docs"
	"github.com/tae2089/code-context-graph/internal/ragindex"
	"github.com/tae2089/code-context-graph/internal/wikiindex"
)

// newDocsCmd creates the documentation generation command.
// @intent 그래프 데이터를 파일별 Markdown 문서와 에이전트용 RAG 인덱스로 변환하는 명령을 노출한다.
// @requires deps.DB가 초기화되어 있어야 한다.
// @sideEffect docs.Generator와 ragindex.Builder를 통해 문서 및 doc-index.json 파일을 기록한다.
func newDocsCmd(deps *Deps) *cobra.Command {
	var outDir string
	var ragIndexDir string
	var projectDesc string
	var excludePatterns []string
	var prune bool
	var buildRAG bool
	var refreshRAG bool
	var communityDepth int

	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Generate markdown documentation and the default RAG index from the code graph",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps.DB == nil {
				return errDBNotInitialized
			}

			absOut, err := filepath.Abs(resolveOutDir(outDir))
			if err != nil {
				return trace.Wrap(err, "resolve out path")
			}

			gen := &docs.Generator{
				DB:        deps.DB,
				OutDir:    absOut,
				Exclude:   resolveExcludes(excludePatterns),
				Namespace: viper.GetString("namespace"),
				Prune:     prune,
			}

			if err := gen.Run(); err != nil {
				return trace.Wrap(err, "generate docs")
			}

			fmt.Fprintf(stdout(cmd), "Docs written to %s\n", absOut)
			wikiPackages, wikiFiles, err := buildDocsWikiIndex(cmd.Context(), deps, docsWikiOptions{
				OutDir:      absOut,
				IndexDir:    resolveRagIndexDir(ragIndexDir),
				ProjectDesc: resolveRagDescription(projectDesc),
				Namespace:   viper.GetString("namespace"),
				Exclude:     resolveExcludes(excludePatterns),
			})
			if err != nil {
				return trace.Wrap(err, "build wiki index")
			}
			fmt.Fprintf(stdout(cmd), "Wiki index written: %d packages, %d files\n", wikiPackages, wikiFiles)
			if buildRAG {
				communities, files, rebuilt, err := buildDocsRAGIndex(cmd.Context(), deps, docsRAGOptions{
					OutDir:         absOut,
					IndexDir:       resolveRagIndexDir(ragIndexDir),
					ProjectDesc:    resolveRagDescription(projectDesc),
					Refresh:        refreshRAG,
					CommunityDepth: communityDepth,
					Namespace:      viper.GetString("namespace"),
				})
				if err != nil {
					return trace.Wrap(err, "build rag index")
				}
				if rebuilt {
					fmt.Fprintf(stdout(cmd), "Communities rebuilt for RAG index\n")
				}
				fmt.Fprintf(stdout(cmd), "RAG index written: %d communities, %d files\n", communities, files)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&outDir, "out", "docs", "Output directory for generated documentation")
	cmd.Flags().BoolVar(&buildRAG, "rag", true, "Build RAG index from generated docs and community structure")
	cmd.Flags().BoolVar(&refreshRAG, "rag-refresh", true, "Refresh community structure before building the RAG index")
	cmd.Flags().StringVar(&ragIndexDir, "rag-index-dir", ".ccg", "Directory for doc-index.json output")
	cmd.Flags().StringVar(&projectDesc, "rag-desc", "", "Project description for root RAG node summary")
	cmd.Flags().IntVar(&communityDepth, "rag-community-depth", 2, "Directory depth for community detection when refreshing RAG communities")
	cmd.Flags().StringArrayVar(&excludePatterns, "exclude", nil, "Exclude files/paths matching pattern (repeatable, e.g. --exclude vendor --exclude '*.pb.go')")
	cmd.Flags().BoolVar(&prune, "prune", true, "Prune stale generator-managed docs no longer present in the graph")
	return cmd
}

// docsRAGOptions carries the RAG build controls derived from docs command flags.
// @intent keep docs generation and RAG indexing options explicit without widening cobra command state.
type docsRAGOptions struct {
	OutDir         string
	IndexDir       string
	ProjectDesc    string
	Refresh        bool
	CommunityDepth int
	Namespace      string
}

// @intent keep ccg docs Wiki-index settings separate from community-based RAG options.
type docsWikiOptions struct {
	OutDir      string
	IndexDir    string
	ProjectDesc string
	Namespace   string
	Exclude     []string
}

// buildDocsWikiIndex creates the browser-facing Wiki tree after docs generation.
// @intent keep ccg-server Wiki navigation independent from community-based RAG retrieval.
// @sideEffect writes wiki-index.json.
func buildDocsWikiIndex(ctx context.Context, deps *Deps, opts docsWikiOptions) (int, int, error) {
	ns := opts.Namespace
	if ns == "" {
		ns = ctxns.DefaultNamespace
	}
	ctx = ctxns.WithNamespace(ctx, ns)
	b := &wikiindex.Builder{
		DB:          deps.DB,
		OutDir:      opts.OutDir,
		IndexDir:    opts.IndexDir,
		ProjectDesc: opts.ProjectDesc,
		Namespace:   ns,
		Exclude:     opts.Exclude,
	}
	return b.Build(ctx)
}

// buildDocsRAGIndex creates the vectorless RAG index after docs generation.
// @intent make ccg docs the default entrypoint for natural-language agent exploration.
// @requires opts.OutDir must point to generated Markdown docs.
// @sideEffect may rebuild community rows and writes doc-index.json.
func buildDocsRAGIndex(ctx context.Context, deps *Deps, opts docsRAGOptions) (int, int, bool, error) {
	if opts.CommunityDepth < 1 || opts.CommunityDepth > 8 {
		return 0, 0, false, trace.New("rag-community-depth must be between 1 and 8")
	}
	ns := opts.Namespace
	if ns == "" {
		ns = ctxns.DefaultNamespace
	}
	ctx = ctxns.WithNamespace(ctx, ns)

	rebuilt := opts.Refresh
	if rebuilt {
		if _, err := community.New(deps.DB).Rebuild(ctx, community.Config{Depth: opts.CommunityDepth}); err != nil {
			return 0, 0, false, err
		}
	}

	b := &ragindex.Builder{
		DB:          deps.DB,
		OutDir:      opts.OutDir,
		IndexDir:    namespaceRagIndexDir(opts.IndexDir, ns),
		ProjectDesc: opts.ProjectDesc,
	}
	communities, files, err := b.Build(ctx)
	return communities, files, rebuilt, err
}

// @intent align CLI RAG index output with MCP and Wiki server namespace lookup paths.
func namespaceRagIndexDir(baseDir, namespace string) string {
	if ctxns.Normalize(namespace) == ctxns.DefaultNamespace {
		return baseDir
	}
	return filepath.Join(baseDir, namespace)
}

// resolveRagIndexDir honors rag.index_dir when the CLI flag is left at its default.
// @intent keep docs and rag-index commands aligned with config-based RAG output paths.
func resolveRagIndexDir(flagValue string) string {
	if flagValue != ".ccg" {
		return flagValue
	}
	if cfgDir := viper.GetString("rag.index_dir"); cfgDir != "" {
		return cfgDir
	}
	return flagValue
}

// resolveRagDescription honors rag.description when the CLI flag is omitted.
// @intent keep docs-generated RAG root summaries consistent with standalone rag-index.
func resolveRagDescription(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	return viper.GetString("rag.description")
}
