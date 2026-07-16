// @index Parse-result cache identity and serialization for repeatable full builds.
package workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"sort"

	ingestapp "github.com/tae2089/code-context-graph/internal/app/ingest"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// cachedParseRecord is the parser-owned portion of a build spool record.
// @intent cache reusable syntax results without duplicating source text or invocation-local byte accounting.
type cachedParseRecord struct {
	Nodes       []graph.Node
	PackageName string
	Interfaces  []ingestapp.PackageInterfaceInfo
	Comments    []ingestapp.CommentBlock
	Language    string
	Edges       []graph.Edge
}

// cachedParseRecordFrom removes invocation-local source and byte data before serialization.
// @intent keep durable cache payloads limited to parser output reused by later builds.
func cachedParseRecordFrom(record spooledBuildRecord) cachedParseRecord {
	return cachedParseRecord{
		Nodes: record.Nodes, PackageName: record.PackageName, Interfaces: record.Interfaces,
		Comments: record.Comments, Language: record.Language, Edges: record.Edges,
	}
}

// toSpooledRecord restores source-derived fields that intentionally are not cached.
// @intent reconstruct the same build spool contract on a cache hit as on a fresh parse.
func (r cachedParseRecord) toSpooledRecord(relPath string, content []byte) spooledBuildRecord {
	nodeBatch := newParsedBuildNodeBatch(relPath, content, r.Nodes, r.PackageName, r.Interfaces, r.Comments, r.Language)
	return spooledBuildRecord{
		RelPath: relPath, Nodes: r.Nodes, PackageName: r.PackageName, Interfaces: r.Interfaces,
		Comments: r.Comments, Language: r.Language, SourceLines: nodeBatch.sourceLines,
		Edges: r.Edges, Bytes: int64(len(content)),
	}
}

// encodeCachedParseRecord serializes parser output into an opaque adapter payload.
// @intent keep cache persistence independent of workflow-internal record types.
func encodeCachedParseRecord(record cachedParseRecord) ([]byte, error) {
	var out bytes.Buffer
	err := gob.NewEncoder(&out).Encode(record)
	return out.Bytes(), err
}

// decodeCachedParseRecord validates and restores an opaque cached parser payload.
// @intent treat malformed or incompatible cache bytes as a recoverable cache miss.
func decodeCachedParseRecord(payload []byte) (cachedParseRecord, error) {
	var record cachedParseRecord
	err := gob.NewDecoder(bytes.NewReader(payload)).Decode(&record)
	return record, err
}

// parseResultCacheKey builds a complete semantic cache identity for versioned parsers.
// @intent bypass caching when a parser cannot prove how its output version is invalidated.
func parseResultCacheKey(input buildParseInput, sourceHash string) (ingestapp.ParseCacheKey, bool) {
	versioned, ok := input.parser.(ingestapp.VersionedParser)
	if !ok || versioned.ParseCacheVersion() == "" {
		return ingestapp.ParseCacheKey{}, false
	}
	return ingestapp.ParseCacheKey{
		FilePath: input.relPath, SourceHash: sourceHash,
		ParserVersion: versioned.ParseCacheVersion(), ContextHash: input.contextHash,
	}, true
}

// parseSemanticContextHash fingerprints repository maps that can change semantic parser output.
// @intent invalidate cached syntax results when import or file-package normalization changes.
func parseSemanticContextHash(ctx context.Context) string {
	h := sha256.New()
	writeMap := func(prefix string, values map[string]string) {
		keys := make([]string, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			h.Write([]byte(prefix))
			h.Write([]byte{0})
			h.Write([]byte(key))
			h.Write([]byte{0})
			h.Write([]byte(values[key]))
			h.Write([]byte{0})
		}
	}
	writeMap("import", ingestapp.ImportPackagesFromContext(ctx))
	writeMap("file", ingestapp.FilePackagesFromContext(ctx))
	return hex.EncodeToString(h.Sum(nil))
}

// setBuildNodeHashes applies the current source hash after a fresh parse or cache hit.
// @intent keep change detection based on current content rather than serialized node state.
// @mutates nodes
func setBuildNodeHashes(nodes []graph.Node, hash string) {
	for i := range nodes {
		nodes[i].Hash = hash
	}
}
