// @index Existing-graph file-state loading and change/force detection.
package service

import (
	"context"

	"gorm.io/gorm"

	"github.com/tae2089/code-context-graph/internal/analysis/incremental"
	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/pathutil"
)

// ExistingGraphFiles returns namespace-scoped graph file paths currently stored.
// @intent share deletion-scope discovery across CLI and MCP incremental updates
func ExistingGraphFiles(ctx context.Context, db *gorm.DB) ([]string, error) {
	filePaths, _, err := existingGraphFileState(ctx, db)
	return filePaths, err
}

// existingGraphFileState loads the namespace-scoped current graph state grouped by file path.
// @intent provide both deletion-scope file paths and per-file node projections from a single query.
func existingGraphFileState(ctx context.Context, db *gorm.DB) ([]string, map[string][]model.Node, error) {
	if db == nil {
		return nil, map[string][]model.Node{}, nil
	}

	ns := ctxns.FromContext(ctx)
	var nodes []graphFileNodeState
	if err := db.WithContext(ctx).
		Model(&model.Node{}).
		Select("id", "file_path", "hash").
		Where("namespace = ? AND kind <> ?", ns, model.NodeKindPackage).
		Find(&nodes).Error; err != nil {
		return nil, nil, err
	}
	nodesByFile := make(map[string][]model.Node)
	fileSeen := make(map[string]struct{})
	filePaths := make([]string, 0)
	for _, node := range nodes {
		minimalNode := model.Node{ID: node.ID, FilePath: node.FilePath, Hash: node.Hash}
		nodesByFile[node.FilePath] = append(nodesByFile[node.FilePath], minimalNode)
		if _, ok := fileSeen[node.FilePath]; !ok {
			fileSeen[node.FilePath] = struct{}{}
			filePaths = append(filePaths, node.FilePath)
		}
	}
	return filePaths, nodesByFile, nil
}

// filterExistingStateByInclude restricts existing graph state to file paths that match the include filter.
// @intent prevent partial-scope updates from deleting files that live outside the requested include paths.
func filterExistingStateByInclude(filePaths []string, nodesByFile map[string][]model.Node, includePaths []string) ([]string, map[string][]model.Node) {
	filteredFiles := make([]string, 0, len(filePaths))
	filteredNodes := make(map[string][]model.Node)
	for _, fp := range filePaths {
		if !pathutil.MatchIncludePaths(fp, includePaths) {
			continue
		}
		filteredFiles = append(filteredFiles, fp)
		filteredNodes[fp] = nodesByFile[fp]
	}
	return filteredFiles, filteredNodes
}

// forceReparseFiles finds files whose edges reference nodes from changed files and therefore must be reparsed.
// @intent keep cross-file edges consistent by reparsing edge-source files when their referenced nodes change.
func forceReparseFiles(ctx context.Context, db *gorm.DB, existingNodesByFile map[string][]model.Node, currentHashes map[string]string) (map[string]struct{}, error) {
	forceFiles := make(map[string]struct{})
	if db == nil || len(existingNodesByFile) == 0 || len(currentHashes) == 0 {
		return forceFiles, nil
	}
	if !db.Migrator().HasTable(&model.Edge{}) {
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

	ns := ctxns.FromContext(ctx)
	edgeFileSeen := make(map[string]struct{})
	for start := 0; start < len(changedNodeIDs); start += forceReparseEdgeChunkSize {
		end := min(start+forceReparseEdgeChunkSize, len(changedNodeIDs))
		chunk := changedNodeIDs[start:end]
		var chunkFiles []string
		if err := db.WithContext(ctx).
			Model(&model.Edge{}).
			Where("namespace = ? AND file_path <> '' AND (from_node_id IN ? OR to_node_id IN ?)", ns, chunk, chunk).
			Distinct().
			Pluck("file_path", &chunkFiles).Error; err != nil {
			return nil, err
		}
		for _, filePath := range chunkFiles {
			edgeFileSeen[filePath] = struct{}{}
		}

		var relatedImplements []model.Edge
		if err := db.WithContext(ctx).
			Model(&model.Edge{}).
			Where("namespace = ? AND kind = ? AND file_path <> '' AND (from_node_id IN ? OR to_node_id IN ?)", ns, model.EdgeKindImplements, chunk, chunk).
			Find(&relatedImplements).Error; err != nil {
			return nil, err
		}
		var relatedTypeIDs []uint
		seenTypeID := make(map[uint]struct{}, len(relatedImplements)*2)
		for _, edge := range relatedImplements {
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
			var dispatchFiles []string
			if err := db.WithContext(ctx).
				Model(&model.Edge{}).
				Where("namespace = ? AND kind IN ? AND file_path <> '' AND (from_node_id IN ? OR to_node_id IN ?)", ns, model.CallEdgeKinds(), relatedChunk, relatedChunk).
				Distinct().
				Pluck("file_path", &dispatchFiles).Error; err != nil {
				return nil, err
			}
			for _, filePath := range dispatchFiles {
				edgeFileSeen[filePath] = struct{}{}
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
func splitForcedFiles(files map[string]incremental.FileInfo, forceFiles map[string]struct{}) (map[string]incremental.FileInfo, map[string]incremental.FileInfo) {
	if len(files) == 0 {
		return nil, nil
	}
	if len(forceFiles) == 0 {
		return files, nil
	}
	normal := make(map[string]incremental.FileInfo, len(files))
	forced := make(map[string]incremental.FileInfo)
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
