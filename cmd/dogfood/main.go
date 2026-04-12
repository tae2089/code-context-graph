package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/imtaebin/code-context-graph/internal/analysis/flows"
	"github.com/imtaebin/code-context-graph/internal/analysis/impact"
	"github.com/imtaebin/code-context-graph/internal/analysis/incremental"
	mcpserver "github.com/imtaebin/code-context-graph/internal/mcp"
	"github.com/imtaebin/code-context-graph/internal/model"
	"github.com/imtaebin/code-context-graph/internal/parse"
	"github.com/imtaebin/code-context-graph/internal/parse/treesitter"
	"github.com/imtaebin/code-context-graph/internal/store/gormstore"
	"github.com/imtaebin/code-context-graph/internal/store/search"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	projectRoot := "."
	if len(os.Args) > 1 {
		projectRoot = os.Args[1]
	}

	absRoot, _ := filepath.Abs(projectRoot)
	logger.Info("dogfood: analyzing project", "root", absRoot)

	db, err := gorm.Open(sqlite.Open("dogfood.db"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		logger.Error("failed to open database", "error", err)
		os.Exit(1)
	}

	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		logger.Error("auto-migrate failed", "error", err)
		os.Exit(1)
	}
	if err := db.AutoMigrate(&model.SearchDocument{}); err != nil {
		logger.Error("search doc migrate failed", "error", err)
		os.Exit(1)
	}

	sb := search.NewSQLiteBackend()
	if err := sb.Migrate(db); err != nil {
		logger.Error("search backend migrate failed", "error", err)
		os.Exit(1)
	}

	walker := treesitter.NewWalker(treesitter.GoSpec, treesitter.WithLogger(logger))
	registry := parse.NewRegistry()
	registry.Register(&parse.LanguageSpec{Name: "go", Extensions: []string{".go"}})
	binder := parse.NewBinder()
	impactAnalyzer := impact.New(st)
	flowTracer := flows.New(st)

	ctx := context.Background()

	fmt.Println("========================================")
	fmt.Println("  code-context-graph Dogfood Analysis")
	fmt.Println("========================================")
	fmt.Println()

	files := map[string]incremental.FileInfo{}
	err = filepath.Walk(absRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if info.Name() == "vendor" || info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		hash := fmt.Sprintf("%x", sha256.Sum256(content))
		relPath, _ := filepath.Rel(absRoot, path)
		files[relPath] = incremental.FileInfo{Hash: hash, Content: content}
		return nil
	})
	if err != nil {
		logger.Error("walk failed", "error", err)
		os.Exit(1)
	}

	fmt.Printf("📁 Found %d Go files\n\n", len(files))

	syncer := incremental.New(st, walker, incremental.WithLogger(logger))
	stats, err := syncer.Sync(ctx, files)
	if err != nil {
		logger.Error("sync failed", "error", err)
		os.Exit(1)
	}

	fmt.Printf("📊 Sync Stats: added=%d, modified=%d, skipped=%d, deleted=%d\n\n", stats.Added, stats.Modified, stats.Skipped, stats.Deleted)

	var allNodes []model.Node
	db.Find(&allNodes)
	var allEdges []model.Edge
	db.Find(&allEdges)

	fmt.Printf("📈 Total: %d nodes, %d edges\n\n", len(allNodes), len(allEdges))

	kindCounts := map[model.NodeKind]int{}
	for _, n := range allNodes {
		kindCounts[n.Kind]++
	}
	fmt.Println("--- Node Kinds ---")
	for kind, count := range kindCounts {
		fmt.Printf("  %-12s %d\n", kind, count)
	}
	fmt.Println()

	edgeKindCounts := map[model.EdgeKind]int{}
	for _, e := range allEdges {
		edgeKindCounts[e.Kind]++
	}
	fmt.Println("--- Edge Kinds ---")
	for kind, count := range edgeKindCounts {
		fmt.Printf("  %-15s %d\n", kind, count)
	}
	fmt.Println()

	fmt.Println("--- Annotation Binding ---")
	for filePath, info := range files {
		nodes, _, err := walker.Parse(filePath, info.Content)
		if err != nil {
			continue
		}
		comments := extractComments(info.Content)
		bindings := binder.Bind(comments, nodes, "go")
		for _, b := range bindings {
			if b.Annotation == nil || (b.Annotation.Summary == "" && len(b.Annotation.Tags) == 0) {
				continue
			}
			storedNode, _ := st.GetNode(ctx, b.Node.QualifiedName)
			if storedNode != nil {
				b.Annotation.NodeID = storedNode.ID
				st.UpsertAnnotation(ctx, b.Annotation)
				tagKinds := []string{}
				for _, t := range b.Annotation.Tags {
					tagKinds = append(tagKinds, string(t.Kind))
				}
				fmt.Printf("  ✅ %s → summary=%q tags=[%s]\n", b.Node.QualifiedName, truncate(b.Annotation.Summary, 50), strings.Join(tagKinds, ","))
			}
		}
	}
	fmt.Println()

	for _, n := range allNodes {
		if n.Kind == model.NodeKindFunction || n.Kind == model.NodeKindTest {
			db.Create(&model.SearchDocument{
				NodeID:   n.ID,
				Content:  n.Name + " " + n.QualifiedName,
				Language: n.Language,
			})
		}
	}
	sb.Rebuild(ctx, db)

	fmt.Println("--- Search: 'parse' ---")
	searchResults, err := sb.Query(ctx, db, "parse", 5)
	if err != nil {
		fmt.Printf("  ❌ search error: %v\n", err)
	} else {
		for _, r := range searchResults {
			fmt.Printf("  🔍 %s (%s) @ %s:%d\n", r.QualifiedName, r.Kind, r.FilePath, r.StartLine)
		}
	}
	fmt.Println()

	fmt.Println("--- Search: 'walker' ---")
	searchResults2, err := sb.Query(ctx, db, "walker", 5)
	if err != nil {
		fmt.Printf("  ❌ search error: %v\n", err)
	} else {
		for _, r := range searchResults2 {
			fmt.Printf("  🔍 %s (%s) @ %s:%d\n", r.QualifiedName, r.Kind, r.FilePath, r.StartLine)
		}
	}
	fmt.Println()

	fmt.Println("--- Impact Radius: Walker.Parse (depth=2) ---")
	parseNode, err := st.GetNode(ctx, "treesitter.Walker.Parse")
	if err == nil && parseNode != nil {
		impacted, err := impactAnalyzer.ImpactRadius(ctx, parseNode.ID, 2)
		if err != nil {
			fmt.Printf("  ❌ impact error: %v\n", err)
		} else {
			fmt.Printf("  Blast radius: %d nodes\n", len(impacted))
			for _, n := range impacted {
				fmt.Printf("    💥 %s (%s) @ %s\n", n.QualifiedName, n.Kind, n.FilePath)
			}
		}
	} else {
		fmt.Println("  (Walker.Parse node not found, trying alternative names...)")
		for _, n := range allNodes {
			if n.Kind == model.NodeKindFunction && strings.Contains(n.QualifiedName, "Parse") && !strings.Contains(n.QualifiedName, "Test") {
				fmt.Printf("  Found: %s (ID=%d)\n", n.QualifiedName, n.ID)
				impacted, err := impactAnalyzer.ImpactRadius(ctx, n.ID, 2)
				if err == nil {
					fmt.Printf("    Blast radius: %d nodes\n", len(impacted))
					for _, imp := range impacted {
						fmt.Printf("      💥 %s (%s)\n", imp.QualifiedName, imp.Kind)
					}
				}
				break
			}
		}
	}
	fmt.Println()

	fmt.Println("--- Flow Trace: from a function ---")
	for _, n := range allNodes {
		if n.Kind == model.NodeKindFunction && !strings.HasPrefix(n.Name, "Test") {
			flow, err := flowTracer.TraceFlow(ctx, n.ID)
			if err == nil && len(flow.Members) > 1 {
				fmt.Printf("  🔗 Flow from %s: %d members\n", n.QualifiedName, len(flow.Members))
				for _, m := range flow.Members {
					mn, _ := st.GetNodeByID(ctx, m.NodeID)
					if mn != nil {
						fmt.Printf("    → %s\n", mn.QualifiedName)
					}
				}
				break
			}
		}
	}
	fmt.Println()

	fmt.Println("--- MCP Server Tool Listing ---")
	deps := &mcpserver.Deps{
		Store:          st,
		DB:             db,
		Parser:         walker,
		SearchBackend:  sb,
		ImpactAnalyzer: impactAnalyzer,
		FlowTracer:     flowTracer,
		Logger:         logger,
	}
	srv := mcpserver.NewServer(deps)
	tools := srv.ListTools()
	for name := range tools {
		fmt.Printf("  🔧 %s\n", name)
	}
	fmt.Println()

	fmt.Println("--- MCP Tool Call: parse_project (via JSON-RPC) ---")
	firstDir := ""
	for f := range files {
		d := filepath.Dir(f)
		if d != "." {
			firstDir = filepath.Join(absRoot, d)
			break
		}
	}
	if firstDir != "" {
		argsJSON, _ := json.Marshal(map[string]any{"path": firstDir})
		msg, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "tools/call",
			"params":  map[string]any{"name": "parse_project", "arguments": json.RawMessage(argsJSON)},
		})
		resp := srv.HandleMessage(ctx, msg)
		respJSON, _ := json.MarshalIndent(resp, "  ", "  ")
		fmt.Printf("  Request: parse_project(%s)\n", firstDir)
		fmt.Printf("  Response: %s\n", string(respJSON))
	}
	fmt.Println()

	_ = registry
	_ = binder

	fmt.Println("========================================")
	fmt.Printf("✅ Dogfood analysis complete: %d nodes, %d edges across %d files\n", len(allNodes), len(allEdges), len(files))
	fmt.Println("========================================")

	os.Remove("dogfood.db")
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func extractComments(content []byte) []parse.CommentBlock {
	var blocks []parse.CommentBlock
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	lineNum := 0
	var current *parse.CommentBlock

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		if strings.HasPrefix(line, "//") {
			text := line
			if current == nil {
				current = &parse.CommentBlock{StartLine: lineNum, EndLine: lineNum, Text: text}
			} else {
				current.EndLine = lineNum
				current.Text += "\n" + text
			}
		} else {
			if current != nil {
				blocks = append(blocks, *current)
				current = nil
			}
		}
	}
	if current != nil {
		blocks = append(blocks, *current)
	}
	return blocks
}
