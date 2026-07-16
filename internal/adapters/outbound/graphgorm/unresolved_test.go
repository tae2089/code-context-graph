package graphgorm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

func TestUnresolvedEdges_PreservesAndIndexesLongValues(t *testing.T) {
	store := setupTestDB(t)
	ctx := requestctx.WithNamespace(context.Background(), "long-values")
	lookupKey := strings.Repeat("lookup.", 600)
	fingerprint := "calls:source.go:" + strings.Repeat("receiver.", 34_000) + ":1"
	candidate := graph.UnresolvedEdgeCandidate{
		LookupKey: lookupKey, Fingerprint: fingerprint, FilePath: "source.go", Kind: graph.EdgeKindCalls, Line: 1,
	}

	if err := store.UpsertUnresolvedEdges(ctx, []graph.UnresolvedEdgeCandidate{candidate}); err != nil {
		t.Fatalf("UpsertUnresolvedEdges: %v", err)
	}
	var hashes struct {
		LookupKeyHash   string
		FingerprintHash string
	}
	if err := store.db.Model(&graph.UnresolvedEdgeCandidate{}).
		Select("lookup_key_hash", "fingerprint_hash").
		Where("namespace = ?", "long-values").
		First(&hashes).Error; err != nil {
		t.Fatalf("load unresolved hashes: %v", err)
	}
	if hashes.LookupKeyHash != sha256HexForTest(lookupKey) {
		t.Fatalf("lookup key hash = %q, want SHA-256", hashes.LookupKeyHash)
	}
	if hashes.FingerprintHash != sha256HexForTest(fingerprint) {
		t.Fatalf("fingerprint hash = %q, want SHA-256", hashes.FingerprintHash)
	}

	got, err := store.FindUnresolvedEdgesByLookupKeys(ctx, []string{lookupKey})
	if err != nil || len(got) != 1 {
		t.Fatalf("FindUnresolvedEdgesByLookupKeys() = %d edges, err=%v; want one", len(got), err)
	}
	if got[0].Fingerprint != fingerprint {
		t.Fatalf("fingerprint length = %d, want exact length %d", len(got[0].Fingerprint), len(fingerprint))
	}
	if err := store.DeleteUnresolvedEdgesByFingerprints(ctx, []string{fingerprint}); err != nil {
		t.Fatalf("DeleteUnresolvedEdgesByFingerprints: %v", err)
	}
	if got, err := store.FindUnresolvedEdgesByLookupKeys(ctx, []string{lookupKey}); err != nil || len(got) != 0 {
		t.Fatalf("remaining unresolved edges = %d, err=%v; want none", len(got), err)
	}
}

func TestFindUnresolvedEdgesByLookupKeys_FiltersHashCollisionByOriginalText(t *testing.T) {
	store := setupTestDB(t)
	ctx := requestctx.WithNamespace(context.Background(), "hash-collision")
	candidates := []graph.UnresolvedEdgeCandidate{
		{LookupKey: "Wanted", Fingerprint: "calls:a.go:Wanted:1", FilePath: "a.go", Kind: graph.EdgeKindCalls, Line: 1},
		{LookupKey: "Other", Fingerprint: "calls:b.go:Other:1", FilePath: "b.go", Kind: graph.EdgeKindCalls, Line: 1},
	}
	if err := store.UpsertUnresolvedEdges(ctx, candidates); err != nil {
		t.Fatalf("UpsertUnresolvedEdges: %v", err)
	}
	if err := store.db.Model(&graph.UnresolvedEdgeCandidate{}).
		Where("namespace = ? AND lookup_key = ?", "hash-collision", "Other").
		Update("lookup_key_hash", sha256HexForTest("Wanted")).Error; err != nil {
		t.Fatalf("force lookup hash collision: %v", err)
	}

	got, err := store.FindUnresolvedEdgesByLookupKeys(ctx, []string{"Wanted"})
	if err != nil {
		t.Fatalf("FindUnresolvedEdgesByLookupKeys: %v", err)
	}
	if len(got) != 1 || got[0].Fingerprint != "calls:a.go:Wanted:1" {
		t.Fatalf("collision lookup returned %+v, want only Wanted", got)
	}
}

func sha256HexForTest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
