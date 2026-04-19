package docs

import (
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/pathutil"
)

// Generator reads the SQLite graph and writes markdown documentation.
// @intent 그래프에 저장된 심볼과 어노테이션을 문서 생성 단계로 전달한다.
type Generator struct {
	DB      *gorm.DB
	OutDir  string
	Exclude []string // path/glob patterns to exclude (see pathutil.MatchExcludes)
}

// Run generates index.md and per-file docs into g.OutDir.
// @intent 전체 문서 산출물을 한 번에 다시 생성한다.
// @sideEffect 파일별 Markdown과 index.md를 출력 디렉터리에 기록한다.
func (g *Generator) Run() error {
	nodes, annByID, err := g.loadNodes()
	if err != nil {
		return fmt.Errorf("load nodes: %w", err)
	}

	ids := symbolNodeIDs(nodes)
	edgesByFromID, err := g.loadEdges(ids)
	if err != nil {
		return fmt.Errorf("load edges: %w", err)
	}

	if len(g.Exclude) > 0 {
		filtered := nodes[:0]
		for _, n := range nodes {
			if !pathutil.MatchExcludes(g.Exclude, n.FilePath) {
				filtered = append(filtered, n)
			}
		}
		nodes = filtered
	}

	groups := groupByFile(nodes, annByID, edgesByFromID)

	var errs []error
	for _, grp := range groups {
		if err := g.writeFileDoc(grp); err != nil {
			errs = append(errs, fmt.Errorf("write file doc %s: %w", grp.FilePath, err))
		}
	}
	if err := g.writeIndex(groups); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// RunIndex regenerates only index.md without rewriting per-file docs.
// @intent 개별 문서를 유지한 채 파일 인덱스만 빠르게 갱신한다.
// @sideEffect 출력 디렉터리의 index.md를 다시 기록한다.
func (g *Generator) RunIndex() error {
	nodes, annByID, err := g.loadNodes()
	if err != nil {
		return fmt.Errorf("load nodes: %w", err)
	}

	if len(g.Exclude) > 0 {
		filtered := nodes[:0]
		for _, n := range nodes {
			if !pathutil.MatchExcludes(g.Exclude, n.FilePath) {
				filtered = append(filtered, n)
			}
		}
		nodes = filtered
	}

	groups := groupByFile(nodes, annByID, nil)
	return g.writeIndex(groups)
}

// loadNodes loads documentable graph nodes and their annotations.
// @intent 문서 렌더링에 필요한 노드와 태그를 한 번에 조회한다.
// @return 문서 대상 노드 목록과 node_id 기준 어노테이션 맵을 반환한다.
func (g *Generator) loadNodes() ([]model.Node, map[uint]*model.Annotation, error) {
	var nodes []model.Node
	if err := g.DB.Where("kind IN ?", []string{
		string(model.NodeKindFunction),
		string(model.NodeKindClass),
		string(model.NodeKindType),
		string(model.NodeKindTest),
		string(model.NodeKindFile),
	}).Find(&nodes).Error; err != nil {
		return nil, nil, fmt.Errorf("query nodes: %w", err)
	}

	ids := nodeIDsFrom(nodes)
	annByID := make(map[uint]*model.Annotation)
	if len(ids) > 0 {
		var annotations []model.Annotation
		if err := g.DB.Where("node_id IN ?", ids).Preload("Tags").Find(&annotations).Error; err != nil {
			return nil, nil, fmt.Errorf("query annotations: %w", err)
		}
		for i := range annotations {
			annByID[annotations[i].NodeID] = &annotations[i]
		}
	}

	return nodes, annByID, nil
}

// loadEdges loads call and import edges keyed by source node.
// @intent 심볼 문서에 호출 관계를 표시할 최소 엣지 집합만 조회한다.
// @param nodeIDs 파일 노드를 제외한 심볼 노드 ID 목록이다.
// @return from_node_id 기준 엣지 맵을 반환한다.
func (g *Generator) loadEdges(nodeIDs []uint) (map[uint][]model.Edge, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	var edges []model.Edge
	if err := g.DB.Preload("ToNode").
		Where("from_node_id IN ? AND kind IN ?", nodeIDs,
			[]string{string(model.EdgeKindCalls), string(model.EdgeKindImportsFrom)}).
		Find(&edges).Error; err != nil {
		return nil, fmt.Errorf("query edges: %w", err)
	}
	result := make(map[uint][]model.Edge, len(edges))
	for _, e := range edges {
		result[e.FromNodeID] = append(result[e.FromNodeID], e)
	}
	return result, nil
}

// nodeIDsFrom collects node IDs from a node slice.
// @intent 후속 배치 조회용 IN 절 입력을 간단히 만든다.
func nodeIDsFrom(nodes []model.Node) []uint {
	ids := make([]uint, len(nodes))
	for i, n := range nodes {
		ids[i] = n.ID
	}
	return ids
}

// symbolNodeIDs returns IDs of non-file nodes only.
// File nodes do not originate call/import edges, so excluding them
// keeps the loadEdges IN clause minimal.
// @intent 엣지 조회가 필요한 심볼 노드만 남겨 배치 크기를 줄인다.
func symbolNodeIDs(nodes []model.Node) []uint {
	ids := make([]uint, 0, len(nodes))
	for _, n := range nodes {
		if n.Kind != model.NodeKindFile {
			ids = append(ids, n.ID)
		}
	}
	return ids
}
