// @index Wire DTOs exchanged with out-of-process parser plugins and their conversion to domain graph values.
package parser

import (
	"fmt"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// wireNode is the JSON DTO a parser plugin emits per declaration.
// @intent carry a parsed declaration as parser-neutral fields for conversion into a domain node.
type wireNode struct {
	QualifiedName string `json:"qualified_name"`
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	FilePath      string `json:"file_path"`
	StartLine     int    `json:"start_line"`
	EndLine       int    `json:"end_line"`
	Hash          string `json:"hash,omitempty"`
	Language      string `json:"language"`
}

// validNodeKinds is the closed set of node kinds a plugin may emit.
// @intent reject mistyped/unknown kinds at the boundary instead of persisting a bogus enum.
var validNodeKinds = map[graph.NodeKind]bool{
	graph.NodeKindFile:     true,
	graph.NodeKindPackage:  true,
	graph.NodeKindClass:    true,
	graph.NodeKindFunction: true,
	graph.NodeKindType:     true,
	graph.NodeKindTest:     true,
}

// toNode converts a wire node into a domain node, rejecting unknown kinds.
// @intent map plugin-supplied declaration fields onto the graph node while guarding the Kind enum.
func (n wireNode) toNode() (graph.Node, error) {
	kind := graph.NodeKind(n.Kind)
	if !validNodeKinds[kind] {
		return graph.Node{}, fmt.Errorf("unsupported node kind %q", n.Kind)
	}
	return graph.Node{
		QualifiedName: n.QualifiedName,
		Kind:          kind,
		Name:          n.Name,
		FilePath:      n.FilePath,
		StartLine:     n.StartLine,
		EndLine:       n.EndLine,
		Hash:          n.Hash,
		Language:      n.Language,
	}, nil
}

// wireEdge is the structured edge a parser plugin emits per relationship.
// @intent carry edge endpoints as fields so core builds the fingerprint, keeping the fingerprint format internal.
type wireEdge struct {
	Kind       string `json:"kind"`
	FilePath   string `json:"file_path"`
	Line       int    `json:"line,omitempty"`
	ToQN       string `json:"to_qn,omitempty"`
	ToName     string `json:"to_name,omitempty"`
	ImportPath string `json:"import_path,omitempty"`
	ProdName   string `json:"prod_name,omitempty"`
	TestQN     string `json:"test_qn,omitempty"`
	ImplQN     string `json:"impl_qn,omitempty"`
	IfaceName  string `json:"iface_name,omitempty"`
	ChildQN    string `json:"child_qn,omitempty"`
	ParentName string `json:"parent_name,omitempty"`
}

// toEdge converts a wire edge into a domain edge, building the fingerprint core-side.
// @intent turn plugin-supplied endpoints into a resolvable graph edge without exposing fingerprint formatting to plugins.
func (e wireEdge) toEdge() (graph.Edge, error) {
	switch graph.EdgeKind(e.Kind) {
	case graph.EdgeKindContains:
		return graph.Edge{
			Kind:        graph.EdgeKindContains,
			FilePath:    e.FilePath,
			Fingerprint: fmt.Sprintf("contains:%s:%s", e.FilePath, e.ToQN),
		}, nil
	case graph.EdgeKindCalls:
		return graph.Edge{
			Kind:        graph.EdgeKindCalls,
			FilePath:    e.FilePath,
			Line:        e.Line,
			Fingerprint: fmt.Sprintf("calls:%s:%s:%d", e.FilePath, e.ToName, e.Line),
		}, nil
	case graph.EdgeKindImportsFrom:
		return graph.Edge{
			Kind:        graph.EdgeKindImportsFrom,
			FilePath:    e.FilePath,
			Line:        e.Line,
			Fingerprint: fmt.Sprintf("imports_from:%s:%s:%d", e.FilePath, e.ImportPath, e.Line),
		}, nil
	case graph.EdgeKindTestedBy:
		return graph.Edge{
			Kind:        graph.EdgeKindTestedBy,
			FilePath:    e.FilePath,
			Fingerprint: fmt.Sprintf("tested_by:%s:%s:%s", e.FilePath, e.ProdName, e.TestQN),
		}, nil
	case graph.EdgeKindImplements:
		return graph.Edge{
			Kind:        graph.EdgeKindImplements,
			FilePath:    e.FilePath,
			Fingerprint: fmt.Sprintf("implements:%s:%s:%s", e.FilePath, e.ImplQN, e.IfaceName),
		}, nil
	case graph.EdgeKindInherits:
		return graph.Edge{
			Kind:        graph.EdgeKindInherits,
			FilePath:    e.FilePath,
			Fingerprint: graph.BuildInheritsFingerprintV2(e.FilePath, e.ChildQN, e.ParentName),
		}, nil
	default:
		return graph.Edge{}, fmt.Errorf("unsupported edge kind %q", e.Kind)
	}
}
