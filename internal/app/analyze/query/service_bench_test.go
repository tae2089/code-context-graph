// Graph query application benchmarks.
package query

import (
	"context"
	"fmt"
	"testing"

	"github.com/tae2089/code-context-graph/internal/adapters/outbound/graphgorm"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const (
	benchNodeCount    = 4000
	benchEdgesPerNode = 12
)

func boolRef(v bool) *bool {
	return &v
}

func setupBenchDB(b *testing.B) *gorm.DB {
	b.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: gormlogger.Discard,
	})
	if err != nil {
		b.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&graph.Node{}, &graph.Edge{}); err != nil {
		b.Fatalf("migrate: %v", err)
	}
	return db
}

func setupQueryBenchDB(b *testing.B, includeFallback bool) *gorm.DB {
	db := setupBenchDB(b)

	nodes := make([]graph.Node, benchNodeCount)
	for i := 0; i < benchNodeCount; i++ {
		nodes[i] = graph.Node{
			QualifiedName: fmt.Sprintf("pkg.Service%d", i),
			Name:          fmt.Sprintf("Service%d", i),
			Kind:          graph.NodeKindFunction,
			FilePath:      fmt.Sprintf("svc_%d.go", i%100),
			StartLine:     1,
			EndLine:       10,
			Language:      "go",
		}
	}
	if err := db.CreateInBatches(nodes, 512).Error; err != nil {
		b.Fatalf("seed nodes: %v", err)
	}

	edges := make([]graph.Edge, 0, benchNodeCount*benchEdgesPerNode)
	for i := 0; i < benchNodeCount; i++ {
		for j := 1; j <= benchEdgesPerNode; j++ {
			kind := graph.EdgeKindCalls
			fromID := uint(i + 1)
			toID := uint((i+j)%benchNodeCount + 1)
			if includeFallback && ((i+j)%3 == 0) {
				kind = graph.EdgeKindFallbackCalls
			}
			edges = append(edges, graph.Edge{
				FromNodeID:  fromID,
				ToNodeID:    toID,
				Kind:        kind,
				Namespace:   "",
				Fingerprint: fmt.Sprintf("%d-%d-%s", fromID, toID, kind),
			})
		}
	}
	if err := db.CreateInBatches(edges, 1024).Error; err != nil {
		b.Fatalf("seed edges: %v", err)
	}

	return db
}

func benchmarkCalleesOf(b *testing.B, includeFallback *bool) {
	db := setupQueryBenchDB(b, true)
	svc := New(graphgorm.New(db))
	ctx := context.Background()
	targetID := uint(benchNodeCount / 2)
	if targetID == 0 {
		targetID = 1
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := svc.CalleesOfWithOptions(ctx, targetID, QueryOptions{IncludeFallbackCalls: includeFallback})
		if err != nil {
			b.Fatalf("query: %v", err)
		}
		if got == nil {
			b.Fatal("query result should not be nil")
		}
	}
}

func benchmarkCalleesOfPage(b *testing.B, includeFallback *bool, limit, offset int) {
	db := setupQueryBenchDB(b, true)
	svc := New(graphgorm.New(db))
	ctx := context.Background()
	targetID := uint(benchNodeCount / 2)
	if targetID == 0 {
		targetID = 1
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := svc.CalleesOfPage(ctx, targetID, QueryOptions{
			IncludeFallbackCalls: includeFallback,
			Limit:                limit,
			Offset:               offset,
		})
		if err != nil {
			b.Fatalf("query: %v", err)
		}
		if got.Nodes == nil {
			b.Fatal("query result should not be nil")
		}
	}
}

func BenchmarkQueryService_CalleesOf_WithFallbackCalls(b *testing.B) {
	benchmarkCalleesOf(b, nil)
}

func BenchmarkQueryService_CalleesOf_StrictCallsOnly(b *testing.B) {
	benchmarkCalleesOf(b, boolRef(false))
}

func BenchmarkQueryService_CalleesOfPage_FirstPage(b *testing.B) {
	benchmarkCalleesOfPage(b, nil, 50, 0)
}

func BenchmarkQueryService_CalleesOfPage_MiddlePage(b *testing.B) {
	benchmarkCalleesOfPage(b, nil, 50, 200)
}

func BenchmarkQueryService_CalleesOfPage_StrictCallsOnly(b *testing.B) {
	benchmarkCalleesOfPage(b, boolRef(false), 50, 0)
}

func BenchmarkQueryService_CallersOf_StrictCallsOnly(b *testing.B) {
	db := setupQueryBenchDB(b, true)
	svc := New(graphgorm.New(db))
	ctx := context.Background()
	targetID := uint(benchNodeCount / 2)
	if targetID == 0 {
		targetID = 1
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := svc.CallersOfWithOptions(ctx, targetID, QueryOptions{IncludeFallbackCalls: boolRef(false)})
		if err != nil {
			b.Fatalf("query: %v", err)
		}
		if got == nil {
			b.Fatal("query result should not be nil")
		}
	}
}

func BenchmarkQueryService_FindExactNameMatches(b *testing.B) {
	db := setupQueryBenchDB(b, false)
	const duplicated = 100
	if err := db.Model(&graph.Node{}).Where("id <= ?", duplicated).Update("name", "CommonName").Error; err != nil {
		b.Fatalf("seed duplicate names: %v", err)
	}

	svc := New(graphgorm.New(db))
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := svc.FindExactNameMatches(ctx, "CommonName", 25)
		if err != nil {
			b.Fatalf("query: %v", err)
		}
		if len(got) == 0 {
			b.Fatal("expected at least one exact-name match")
		}
	}
}
