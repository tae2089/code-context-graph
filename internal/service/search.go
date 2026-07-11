// @index Search-document refresh and FTS content generation.
package service

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/identtoken"
	"github.com/tae2089/code-context-graph/internal/model"
)

// rebuildSearchNodes refreshes search documents for the given node IDs when a search backend is configured.
// @intent perform incremental search index updates after changed-node sets are known.
func (s *GraphService) rebuildSearchNodes(ctx context.Context, nodeIDs []uint) error {
	if s.SearchBackend == nil || s.DB == nil {
		return nil
	}
	return s.rebuildSearchNodesWithDB(ctx, s.DB, nodeIDs)
}

// rebuildSearchWithDB refreshes all search documents and rebuilds the backend index against the supplied DB handle.
// @intent let build paths share one transaction across search document refresh and FTS rebuild.
// @sideEffect rewrites search_documents and rebuilds the search backend index.
// @mutates search_documents
func (s *GraphService) rebuildSearchWithDB(ctx context.Context, db *gorm.DB) error {
	if s.SearchBackend == nil || db == nil {
		return nil
	}
	docCount, err := RefreshSearchDocuments(ctx, db)
	if err != nil {
		return err
	}
	if err := s.SearchBackend.Rebuild(ctx, db); err != nil {
		return trace.Wrap(err, "rebuild search index")
	}
	s.logger().Info("search index rebuilt", "documents", docCount)
	return nil
}

// rebuildSearchNodesWithDB refreshes search documents for the given node IDs and updates the backend index scope.
// @intent keep the FTS index incrementally consistent with the latest changed nodes.
// @sideEffect rewrites the affected search_documents rows and updates the search backend.
// @mutates search_documents
func (s *GraphService) rebuildSearchNodesWithDB(ctx context.Context, db *gorm.DB, nodeIDs []uint) error {
	if s.SearchBackend == nil || db == nil || len(nodeIDs) == 0 {
		return nil
	}
	docCount, err := RefreshSearchDocumentsFor(ctx, db, nodeIDs)
	if err != nil {
		return err
	}
	if err := s.SearchBackend.RebuildNodes(ctx, db, nodeIDs); err != nil {
		return trace.Wrap(err, "rebuild scoped search index")
	}
	s.logger().Info("search index partially rebuilt", "documents", docCount, "nodes", len(nodeIDs))
	return nil
}

// RefreshSearchDocuments rebuilds namespace-scoped search_documents from current graph nodes.
// @intent keep derived search documents consistent with graph state before FTS rebuilds
func RefreshSearchDocuments(ctx context.Context, db *gorm.DB) (int, error) {
	if db == nil {
		return 0, trace.New("search document refresh requires db handle")
	}
	return refreshSearchDocuments(ctx, db, nil, false)
}

// RefreshSearchDocumentsFor rebuilds search_documents for the specified node IDs only.
// @intent incremental update 경로에서 영향받은 문서만 갱신한다.
func RefreshSearchDocumentsFor(ctx context.Context, db *gorm.DB, nodeIDs []uint) (int, error) {
	if len(nodeIDs) == 0 {
		return 0, nil
	}
	if db == nil {
		return 0, trace.New("search document refresh requires db handle")
	}
	return refreshSearchDocuments(ctx, db, nodeIDs, true)
}

// refreshSearchDocuments rebuilds the namespace-scoped search_documents table either fully or for a node-id scope.
// @intent regenerate FTS content from the latest nodes and annotations in batches to bound memory.
// @sideEffect deletes and re-inserts rows in search_documents within a DB transaction.
// @mutates search_documents
func refreshSearchDocuments(ctx context.Context, db *gorm.DB, nodeIDs []uint, scoped bool) (int, error) {
	ns := ctxns.FromContext(ctx)
	count := 0
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		docsQ := tx.WithContext(ctx).Where("namespace = ?", ns)
		nodesQ := tx.WithContext(ctx).
			Where("kind IN ?", []string{"function", "class", "type", "test", "file"}).
			Where("namespace = ?", ns)
		if scoped {
			for start := 0; start < len(nodeIDs); start += scopedINQueryChunkSize {
				chunk := scopedNodeIDsForChunk(nodeIDs, start)
				if err := tx.WithContext(ctx).Where("namespace = ?", ns).Where("node_id IN ?", chunk).Delete(&model.SearchDocument{}).Error; err != nil {
					return trace.Wrap(err, "clear search documents")
				}
			}
		} else {
			if err := docsQ.Delete(&model.SearchDocument{}).Error; err != nil {
				return trace.Wrap(err, "clear search documents")
			}
		}

		loadNodes := func(query *gorm.DB) error {
			var batchNodes []model.Node
			result := query.FindInBatches(&batchNodes, 500, func(batchTx *gorm.DB, batch int) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				_ = batchTx
				nodeIDs := make([]uint, len(batchNodes))
				for i, n := range batchNodes {
					nodeIDs[i] = n.ID
				}
				annByNode := map[uint]*model.Annotation{}
				if len(nodeIDs) > 0 {
					var annotations []model.Annotation
					annQ := tx.Session(&gorm.Session{NewDB: true}).WithContext(ctx).Model(&model.Annotation{})
					if err := annQ.Where("node_id IN ?", nodeIDs).Find(&annotations).Error; err != nil {
						return trace.Wrap(err, "load annotations batch "+strconv.Itoa(batch))
					}
					if len(annotations) > 0 {
						annotationIDs := make([]uint, len(annotations))
						for i := range annotations {
							annotationIDs[i] = annotations[i].ID
						}
						var tags []model.DocTag
						tagsQ := tx.Session(&gorm.Session{NewDB: true}).WithContext(ctx).Model(&model.DocTag{})
						if err := tagsQ.Where("annotation_id IN ?", annotationIDs).Order("annotation_id, ordinal").Find(&tags).Error; err != nil {
							return trace.Wrap(err, "load annotation tags batch "+strconv.Itoa(batch))
						}
						tagsByAnnotation := make(map[uint][]model.DocTag, len(annotations))
						for _, tag := range tags {
							tagsByAnnotation[tag.AnnotationID] = append(tagsByAnnotation[tag.AnnotationID], tag)
						}
						for i := range annotations {
							annotations[i].Tags = tagsByAnnotation[annotations[i].ID]
						}
					}
					for i := range annotations {
						annByNode[annotations[i].NodeID] = &annotations[i]
					}
				}
				docs := make([]model.SearchDocument, 0, len(batchNodes))
				for _, n := range batchNodes {
					docs = append(docs, model.SearchDocument{
						Namespace: n.Namespace,
						NodeID:    n.ID,
						Content:   buildSearchContent(n, annByNode),
						Language:  n.Language,
					})
				}
				if len(docs) > 0 {
					if err := tx.WithContext(ctx).CreateInBatches(docs, 100).Error; err != nil {
						return trace.Wrap(err, "batch insert search documents")
					}
				}
				count += len(docs)
				return nil
			})
			if result.Error != nil {
				return trace.Wrap(result.Error, "load index nodes")
			}
			return nil
		}

		if scoped {
			for start := 0; start < len(nodeIDs); start += scopedINQueryChunkSize {
				chunk := scopedNodeIDsForChunk(nodeIDs, start)
				chunkNodesQ := tx.WithContext(ctx).
					Where("kind IN ?", []string{"function", "class", "type", "test", "file"}).
					Where("namespace = ?", ns).
					Where("id IN ?", chunk)
				if err := loadNodes(chunkNodesQ); err != nil {
					return err
				}
			}
			return nil
		}
		return loadNodes(nodesQ)
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

// buildSearchContent assembles the text indexed for one node's search document.
// @intent 심볼명, 경로 토큰, 어노테이션 태그를 합쳐 검색 recall을 높이는 색인 본문을 만든다.
func buildSearchContent(n model.Node, annByNode map[uint]*model.Annotation) string {
	var sb strings.Builder
	sb.WriteString(n.Name)
	sb.WriteByte(' ')
	sb.WriteString(n.QualifiedName)
	sb.WriteByte(' ')
	sb.WriteString(string(n.Kind))
	// Emit identifier sub-tokens so camelCase names are searchable by their
	// inner words (getUserById -> get, user, by, id). The verbatim name above
	// preserves existing whole-name and prefix matching.
	for _, token := range identSubtokens(n.Name, n.QualifiedName) {
		sb.WriteByte(' ')
		sb.WriteString(token)
	}
	for _, token := range searchPathTokens(n.FilePath) {
		sb.WriteByte(' ')
		sb.WriteString(token)
	}
	if ann := annByNode[n.ID]; ann != nil {
		if ann.Summary != "" {
			sb.WriteByte(' ')
			sb.WriteString(ann.Summary)
		}
		if ann.Context != "" {
			sb.WriteByte(' ')
			sb.WriteString(ann.Context)
		}
		for _, tag := range ann.Tags {
			sb.WriteByte(' ')
			sb.WriteString(tag.Value)
		}
	}
	return sb.String()
}

// identSubtokens returns the deduplicated camelCase/separator sub-tokens of a
// node's name and qualified name. Dedup keeps a token's term frequency from
// being inflated just because it appears in both the name and the qualified name.
func identSubtokens(name, qualifiedName string) []string {
	seen := map[string]struct{}{}
	var tokens []string
	for _, raw := range []string{name, qualifiedName} {
		for _, tok := range identtoken.Split(raw) {
			if _, ok := seen[tok]; ok {
				continue
			}
			seen[tok] = struct{}{}
			tokens = append(tokens, tok)
		}
	}
	return tokens
}

// searchPathTokens derives lowercase filename tokens and optional language aliases for search indexing.
// @intent 파일 경로 자체도 검색 힌트가 되도록 basename과 언어 별칭을 토큰화한다.
func searchPathTokens(filePath string) []string {
	base := strings.ToLower(filepath.Base(filePath))
	if base == "" || base == "." {
		return nil
	}
	parts := strings.Split(base, ".")
	if len(parts) == 0 {
		return nil
	}
	tokens := make([]string, 0, len(parts)+1)
	for _, part := range parts {
		if part == "" {
			continue
		}
		tokens = append(tokens, part)
	}
	if len(parts) > 1 {
		if alias, ok := searchLanguageAlias(parts[len(parts)-1]); ok {
			if alias != parts[len(parts)-1] {
				tokens = append(tokens, alias)
			}
		}
	}
	return tokens
}

// searchLanguageAlias maps file extensions to human-friendly language tokens used in search content.
// @intent 확장자 토큰만으로는 부족한 언어 검색 질의를 보완한다.
func searchLanguageAlias(ext string) (string, bool) {
	switch ext {
	case "go":
		return "go", true
	case "py":
		return "python", true
	case "ts":
		return "typescript", true
	case "java":
		return "java", true
	case "rb":
		return "ruby", true
	case "js":
		return "javascript", true
	case "c":
		return "c", true
	case "cpp":
		return "cpp", true
	case "rs":
		return "rust", true
	case "kt":
		return "kotlin", true
	case "php":
		return "php", true
	case "lua", "luau":
		return "lua", true
	default:
		return "", false
	}
}

// scopedNodeIDsForChunk slices a node ID list using the configured IN-query chunk size.
// @intent keep search rebuild SQL within the SQLite/Postgres parameter limit.
func scopedNodeIDsForChunk(nodeIDs []uint, start int) []uint {
	end := min(start+scopedINQueryChunkSize, len(nodeIDs))
	return nodeIDs[start:end]
}
