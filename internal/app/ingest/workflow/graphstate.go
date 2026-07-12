// @index Existing-graph file-state loading and change/force detection.
package workflow

import (
	"context"

	"github.com/tae2089/code-context-graph/internal/app/ingest"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
	"github.com/tae2089/code-context-graph/internal/pathspec"
)

// ExistingGraphFiles returns namespace-scoped graph file paths currently stored.
// @intent share deletion-scope discovery across CLI and MCP incremental updates
func ExistingGraphFiles(ctx context.Context, graphStore ingest.GraphStore) ([]string, error) {
	filePaths, _, err := existingGraphFileState(ctx, graphStore)
	return filePaths, err
}

// existingGraphFileState loads the namespace-scoped current graph state grouped by file path.
// @intent provide both deletion-scope file paths and per-file node projections from a single query.
func existingGraphFileState(ctx context.Context, graphStore ingest.GraphStore) ([]string, map[string][]graph.Node, error) {
	if graphStore == nil {
		return nil, map[string][]graph.Node{}, nil
	}

	nodes, err := graphStore.ListFileNodes(ctx)
	if err != nil {
		return nil, nil, err
	}
	nodesByFile := make(map[string][]graph.Node)
	fileSeen := make(map[string]struct{})
	filePaths := make([]string, 0)
	for _, node := range nodes {
		nodesByFile[node.FilePath] = append(nodesByFile[node.FilePath], node)
		if _, ok := fileSeen[node.FilePath]; !ok {
			fileSeen[node.FilePath] = struct{}{}
			filePaths = append(filePaths, node.FilePath)
		}
	}
	return filePaths, nodesByFile, nil
}

// filterExistingStateByInclude restricts existing graph state to file paths that match the include filter.
// @intent prevent partial-scope updates from deleting files that live outside the requested include paths.
func filterExistingStateByInclude(filePaths []string, nodesByFile map[string][]graph.Node, includePaths []string) ([]string, map[string][]graph.Node) {
	filteredFiles := make([]string, 0, len(filePaths))
	filteredNodes := make(map[string][]graph.Node)
	for _, fp := range filePaths {
		if !pathspec.MatchIncludePaths(fp, includePaths) {
			continue
		}
		filteredFiles = append(filteredFiles, fp)
		filteredNodes[fp] = nodesByFile[fp]
	}
	return filteredFiles, filteredNodes
}

// forceReparseFiles finds files whose edges reference nodes from changed files and therefore must be reparsed.
// @intent keep cross-file edges consistent by reparsing edge-source files when their referenced nodes change.
func forceReparseFiles(ctx context.Context, graphStore ingest.GraphStore, existingNodesByFile map[string][]graph.Node, currentHashes map[string]string) (map[string]struct{}, error) {
	forceFiles := make(map[string]struct{})
	if graphStore == nil || len(existingNodesByFile) == 0 || len(currentHashes) == 0 {
		return forceFiles, nil
	}

	var changedNodeIDs []uint
	for filePath, nodes := range existingNodesByFile {
		if len(nodes) == 0 {
			continue
		}
		currentHash, stillPresent := currentHashes[filePath]
		if !stillPresent || nodes[0].Hash != currentHash {
			for _, node := range nodes {
				changedNodeIDs = append(changedNodeIDs, node.ID)
			}
		}
	}
	if len(changedNodeIDs) == 0 {
		return forceFiles, nil
	}

	edgeFileSeen := make(map[string]struct{})
	for start := 0; start < len(changedNodeIDs); start += forceReparseEdgeChunkSize {
		end := min(start+forceReparseEdgeChunkSize, len(changedNodeIDs))
		chunk := changedNodeIDs[start:end]
		outgoing, err := graphStore.GetEdgesFromNodes(ctx, chunk)
		if err != nil {
			return nil, err
		}
		incoming, err := graphStore.GetEdgesToNodes(ctx, chunk)
		if err != nil {
			return nil, err
		}

		var relatedTypeIDs []uint
		seenTypeID := make(map[uint]struct{}, (len(outgoing)+len(incoming))*2)
		for _, edge := range append(outgoing, incoming...) {
			if edge.FilePath != "" {
				edgeFileSeen[edge.FilePath] = struct{}{}
			}
			if edge.Kind != graph.EdgeKindImplements || edge.FilePath == "" {
				continue
			}
			for _, id := range []uint{edge.FromNodeID, edge.ToNodeID} {
				if id == 0 {
					continue
				}
				if _, ok := seenTypeID[id]; ok {
					continue
				}
				seenTypeID[id] = struct{}{}
				relatedTypeIDs = append(relatedTypeIDs, id)
			}
		}
		for relatedStart := 0; relatedStart < len(relatedTypeIDs); relatedStart += forceReparseEdgeChunkSize {
			relatedEnd := min(relatedStart+forceReparseEdgeChunkSize, len(relatedTypeIDs))
			relatedChunk := relatedTypeIDs[relatedStart:relatedEnd]
			relatedOutgoing, err := graphStore.GetEdgesFromNodes(ctx, relatedChunk)
			if err != nil {
				return nil, err
			}
			relatedIncoming, err := graphStore.GetEdgesToNodes(ctx, relatedChunk)
			if err != nil {
				return nil, err
			}
			for _, edge := range append(relatedOutgoing, relatedIncoming...) {
				if graph.IsCallKind(edge.Kind) && edge.FilePath != "" {
					edgeFileSeen[edge.FilePath] = struct{}{}
				}
			}
		}
	}
	for filePath := range edgeFileSeen {
		currentHash, stillPresent := currentHashes[filePath]
		if !stillPresent {
			continue
		}
		nodes := existingNodesByFile[filePath]
		if len(nodes) == 0 || nodes[0].Hash != currentHash {
			continue
		}
		forceFiles[filePath] = struct{}{}
	}
	return forceFiles, nil
}

// splitForcedFiles partitions inputs into normal and forced-reparse buckets and marks the forced ones.
// @intent process unchanged-hash forced files separately so the syncer can bypass its hash short-circuit.
func splitForcedFiles(files map[string]ingest.FileInfo, forceFiles map[string]struct{}) (map[string]ingest.FileInfo, map[string]ingest.FileInfo) {
	if len(files) == 0 {
		return nil, nil
	}
	if len(forceFiles) == 0 {
		return files, nil
	}
	normal := make(map[string]ingest.FileInfo, len(files))
	forced := make(map[string]ingest.FileInfo)
	for filePath, info := range files {
		if _, ok := forceFiles[filePath]; ok {
			info.Force = true
			forced[filePath] = info
			continue
		}
		normal[filePath] = info
	}
	return normal, forced
}
