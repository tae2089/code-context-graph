// @index Structural outlines: what the graph holds under one path, without ranking it.
package describe

import (
	"context"
	"path"
	"sort"
	"strings"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// Scope says which kind of thing the target turned out to name.
//
// It is on the answer rather than on the request because a caller holding a
// string from somewhere else — a stack frame, a search hit, a diff — does not
// always know whether it points at a folder, a file, or nothing at all.
// @intent let one call answer for a folder, a file, or a miss, and say which it was.
type Scope string

const (
	ScopeDirectory Scope = "directory"
	ScopeFile      Scope = "file"
	// ScopeUnknown is the honest answer for a target the graph does not hold.
	// It is a case of the result rather than an error because the useful reply
	// to a mistyped or half-remembered name is a list of places that name lives.
	ScopeUnknown Scope = "unknown"
)

// MaxSuggestions bounds how many places a missed target is offered back.
const MaxSuggestions = 10

// declarationKinds are the node kinds that count as something written in a file.
//
// File and package nodes are excluded: they are the containers being described,
// so counting them would make every folder look one declaration busier than it is.
// @intent keep "what is written here" separate from "where it is written".
func declarationKinds() []graph.NodeKind {
	return []graph.NodeKind{
		graph.NodeKindClass,
		graph.NodeKindFunction,
		graph.NodeKindType,
		graph.NodeKindTest,
	}
}

// outlineKinds are what a folder walk has to load: the declarations plus the
// file nodes that hold them, so a file with nothing in it still appears.
func outlineKinds() []graph.NodeKind {
	return append([]graph.NodeKind{graph.NodeKindFile}, declarationKinds()...)
}

// Repository supplies the namespace-scoped graph rows an outline is built from.
// @intent keep outline shaping independent of how the rows are queried.
type Repository interface {
	NodesByFile(ctx context.Context, filePath string) ([]graph.Node, error)
	PathNodes(ctx context.Context, folderPath string, kinds []graph.NodeKind) ([]graph.Node, error)
	NodesByExactName(ctx context.Context, name string, limit int) ([]graph.Node, error)
	Annotations(ctx context.Context, nodeIDs []uint) (map[uint]*graph.Annotation, error)
}

// Decl is one declaration written in a file.
// @intent give a reader a name, a place to open, and why it exists.
type Decl struct {
	NodeID        uint   `json:"node_id"`
	Name          string `json:"name"`
	QualifiedName string `json:"qualified_name"`
	Kind          string `json:"kind"`
	StartLine     int    `json:"start_line"`
	EndLine       int    `json:"end_line"`
	Intent        string `json:"intent,omitempty"`
}

// Child is one folder or file directly inside the described folder.
//
// Only one level down is reported. A folder walk that returned everything
// underneath would hand back thousands of rows for a target like `internal`,
// and the caller cannot choose between thousands of rows — it can choose
// between eight.
// @intent let a caller descend one deliberate step at a time.
type Child struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	FileCount int    `json:"file_count"`
	DeclCount int    `json:"decl_count"`
}

// Suggestion is one place a missed target's name actually lives.
// @intent turn a wrong path into the right one instead of into an empty answer.
type Suggestion struct {
	QualifiedName string `json:"qualified_name"`
	Kind          string `json:"kind"`
	FilePath      string `json:"file_path"`
	StartLine     int    `json:"start_line"`
}

// Outline is everything the graph holds under one target.
//
// Nothing here is ranked, scored, or filtered. That is the whole point: a
// search answer can be wrong about what matters, and an outline can only be
// wrong about what exists. Which field is filled is decided by Scope.
// @intent answer "what is in here" exactly, so the ranked tools do not have to.
type Outline struct {
	Target       string       `json:"target"`
	Scope        Scope        `json:"scope"`
	Children     []Child      `json:"children,omitempty"`
	Declarations []Decl       `json:"declarations,omitempty"`
	Suggestions  []Suggestion `json:"suggestions,omitempty"`
}

// Service answers structural questions from stored graph rows.
// @intent provide one application entry point for "what is in here".
type Service struct {
	Repository Repository
}

// New constructs an outline service from a consumer-owned outbound port.
// @intent make the graph dependency explicit at composition time.
func New(repository Repository) *Service {
	return &Service{Repository: repository}
}

// Describe returns what the graph holds under target.
//
// The target is resolved in one order — file, then folder, then miss — because
// the three cannot collide: a file path holds no rows underneath it, and a
// folder path holds no rows of its own.
//
// @requires target must not be blank.
// @return returns Declarations for a file, Children for a folder, and Suggestions for neither.
func (s *Service) Describe(ctx context.Context, target string) (Outline, error) {
	if s == nil || s.Repository == nil {
		return Outline{}, trace.New("describe service is not configured")
	}
	cleaned := cleanTarget(target)
	if cleaned == "" {
		return Outline{}, trace.New("describe target must not be empty")
	}

	nodes, err := s.Repository.NodesByFile(ctx, cleaned)
	if err != nil {
		return Outline{}, err
	}
	if len(nodes) > 0 {
		declarations, err := s.declarationsOf(ctx, nodes)
		if err != nil {
			return Outline{}, err
		}
		return Outline{Target: cleaned, Scope: ScopeFile, Declarations: declarations}, nil
	}

	under, err := s.Repository.PathNodes(ctx, cleaned, outlineKinds())
	if err != nil {
		return Outline{}, err
	}
	if len(under) > 0 {
		return Outline{Target: cleaned, Scope: ScopeDirectory, Children: childrenOf(cleaned, under)}, nil
	}

	suggestions, err := s.suggestionsFor(ctx, cleaned)
	if err != nil {
		return Outline{}, err
	}
	return Outline{Target: cleaned, Scope: ScopeUnknown, Suggestions: suggestions}, nil
}

// declarationsOf turns one file's rows into line-ordered declarations with
// their recorded intent attached.
//
// The file's own node is dropped: it is the thing being described, not
// something written in it.
// @intent hand back a file's contents in the order a reader would scroll through them.
func (s *Service) declarationsOf(ctx context.Context, nodes []graph.Node) ([]Decl, error) {
	kept := make([]graph.Node, 0, len(nodes))
	ids := make([]uint, 0, len(nodes))
	for _, node := range nodes {
		if node.Kind == graph.NodeKindFile || node.Kind == graph.NodeKindPackage {
			continue
		}
		kept = append(kept, node)
		ids = append(ids, node.ID)
	}
	if len(kept) == 0 {
		return nil, nil
	}

	annotations, err := s.Repository.Annotations(ctx, ids)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].StartLine != kept[j].StartLine {
			return kept[i].StartLine < kept[j].StartLine
		}
		return kept[i].QualifiedName < kept[j].QualifiedName
	})

	declarations := make([]Decl, 0, len(kept))
	for _, node := range kept {
		node.Annotation = annotations[node.ID]
		declarations = append(declarations, Decl{
			NodeID:        node.ID,
			Name:          node.Name,
			QualifiedName: node.QualifiedName,
			Kind:          string(node.Kind),
			StartLine:     node.StartLine,
			EndLine:       node.EndLine,
			Intent:        node.Intent(),
		})
	}
	return declarations, nil
}

// suggestionsFor looks up the places a missed target's name is actually
// declared, trying the whole target first and then its last segment.
//
// The last segment is tried because the shapes people mistype are `pkg.Type`
// and `pkg/Type`, and in both the part after the separator is the name the
// graph stores.
// @intent answer a wrong path with the right one.
func (s *Service) suggestionsFor(ctx context.Context, target string) ([]Suggestion, error) {
	candidates := []string{target}
	if tail := lastSegment(target); tail != target {
		candidates = append(candidates, tail)
	}
	for _, candidate := range candidates {
		nodes, err := s.Repository.NodesByExactName(ctx, candidate, MaxSuggestions)
		if err != nil {
			return nil, err
		}
		if len(nodes) == 0 {
			continue
		}
		suggestions := make([]Suggestion, 0, len(nodes))
		for _, node := range nodes {
			suggestions = append(suggestions, Suggestion{
				QualifiedName: node.QualifiedName,
				Kind:          string(node.Kind),
				FilePath:      node.FilePath,
				StartLine:     node.StartLine,
			})
		}
		return suggestions, nil
	}
	return nil, nil
}

// childrenOf collapses every row under a folder into its immediate children.
//
// A row's child is the one path segment following the folder. When anything
// follows that segment the child is a folder, and every row beneath it folds
// into the same bucket.
// @intent turn a recursive row set into the one level a caller can choose from.
// @domainRule a child folder's counts include everything nested below it.
func childrenOf(folder string, nodes []graph.Node) []Child {
	byPath := make(map[string]*Child)
	for _, node := range nodes {
		name, isFolder := childSegment(folder, node.FilePath)
		if name == "" {
			continue
		}
		childPath := name
		if folder != "." {
			childPath = folder + "/" + name
		}
		child, seen := byPath[childPath]
		if !seen {
			kind := "file"
			if isFolder {
				kind = "directory"
			}
			child = &Child{Path: childPath, Kind: kind}
			byPath[childPath] = child
		}
		switch node.Kind {
		case graph.NodeKindFile:
			child.FileCount++
		case graph.NodeKindPackage:
		default:
			child.DeclCount++
		}
	}

	children := make([]Child, 0, len(byPath))
	for _, child := range byPath {
		children = append(children, *child)
	}
	sort.SliceStable(children, func(i, j int) bool {
		if children[i].Kind != children[j].Kind {
			return children[i].Kind == "directory"
		}
		return children[i].Path < children[j].Path
	})
	return children
}

// childSegment returns the one path segment of filePath directly inside folder,
// and whether anything is nested below it.
// @intent decide a row's immediate bucket without walking the whole path.
func childSegment(folder, filePath string) (string, bool) {
	rest := filePath
	if folder != "." {
		prefix := folder + "/"
		if !strings.HasPrefix(filePath, prefix) {
			return "", false
		}
		rest = filePath[len(prefix):]
	}
	if rest == "" {
		return "", false
	}
	if head, _, nested := strings.Cut(rest, "/"); nested {
		return head, true
	}
	return rest, false
}

// cleanTarget normalizes a caller-supplied path or name for lookup.
// @intent make "./internal/app/", "internal/app" and "internal//app" the same target.
func cleanTarget(target string) string {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return ""
	}
	if !strings.ContainsAny(trimmed, "/\\") {
		return trimmed
	}
	cleaned := path.Clean(strings.ReplaceAll(trimmed, "\\", "/"))
	cleaned = strings.Trim(cleaned, "/")
	if cleaned == "." || cleaned == "" {
		return "."
	}
	return cleaned
}

// lastSegment returns the part of target after its final separator.
// @intent recover the stored short name from a dotted or slashed guess.
func lastSegment(target string) string {
	if idx := strings.LastIndexAny(target, "./"); idx >= 0 && idx+1 < len(target) {
		return target[idx+1:]
	}
	return target
}
