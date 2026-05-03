package treesitter

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/tae2089/code-context-graph/internal/model"
)

// LanguageSemantics provides optional language-specific graph enrichment hooks.
// @intent keep language-specific inference opt-in while the generic parser remains shared.
type LanguageSemantics interface {
	AdditionalEdges(ctx SemanticContext) []model.Edge
}

// SemanticContext carries parsed state into language-specific enrichment hooks.
// @intent avoid expanding Walker with one-off language branches as graph inference grows.
type SemanticContext struct {
	Root       *sitter.Node
	Content    []byte
	FilePath   string
	Package    string
	Nodes      []model.Node
	Interfaces []interfaceInfo
}

// NoopSemantics is the default implementation for languages without extra inference.
type NoopSemantics struct{}

// AdditionalEdges returns no extra relationships for unsupported language hooks.
func (NoopSemantics) AdditionalEdges(SemanticContext) []model.Edge {
	return nil
}

// GoSemantics recovers Go-specific relationships that are not explicit call edges.
type GoSemantics struct{}

// AdditionalEdges adds Go structural and assertion-based implementation edges.
func (GoSemantics) AdditionalEdges(ctx SemanticContext) []model.Edge {
	var edges []model.Edge
	edges = append(edges, goStructuralImplements(ctx.Nodes, ctx.Interfaces, ctx.FilePath)...)
	edges = append(edges, goAssertionImplements(ctx.Root, ctx.Content, ctx.FilePath)...)
	return edges
}

func semanticsOrDefault(s *LangSpec) LanguageSemantics {
	if s != nil && s.Semantics != nil {
		return s.Semantics
	}
	return NoopSemantics{}
}

func goStructuralImplements(nodes []model.Node, ifaces []interfaceInfo, filePath string) []model.Edge {
	methodsByReceiver := make(map[string]map[string]bool)
	for _, n := range nodes {
		if n.Kind != model.NodeKindFunction {
			continue
		}
		parts := strings.Split(n.QualifiedName, ".")
		if len(parts) >= 3 {
			receiver := parts[len(parts)-2]
			method := parts[len(parts)-1]
			if methodsByReceiver[receiver] == nil {
				methodsByReceiver[receiver] = make(map[string]bool)
			}
			methodsByReceiver[receiver][method] = true
		}
	}

	var edges []model.Edge
	for _, iface := range ifaces {
		if len(iface.methods) == 0 {
			continue
		}
		for receiver, methods := range methodsByReceiver {
			allMatch := true
			for _, m := range iface.methods {
				if !methods[m] {
					allMatch = false
					break
				}
			}
			if allMatch {
				edges = append(edges, model.Edge{
					Kind:        model.EdgeKindImplements,
					FilePath:    filePath,
					Fingerprint: fmt.Sprintf("implements:%s:%s:%s", filePath, receiver, iface.name),
				})
			}
		}
	}
	return edges
}

func goAssertionImplements(root *sitter.Node, content []byte, filePath string) []model.Edge {
	if root == nil {
		return nil
	}
	importAliases := goImportAliases(root, content)
	var edges []model.Edge
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if n.Type() == "var_spec" {
			if impl, iface, ok := goAssertionSpec(n, content, importAliases); ok {
				edges = append(edges, model.Edge{
					Kind:        model.EdgeKindImplements,
					FilePath:    filePath,
					Line:        int(n.StartPoint().Row) + 1,
					Fingerprint: fmt.Sprintf("implements:%s:%s:%s", filePath, impl, iface),
				})
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
	return edges
}

func goImportAliases(root *sitter.Node, content []byte) map[string]string {
	aliases := make(map[string]string)
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if n.Type() == "import_spec" {
			importPath := ""
			if pathNode := n.ChildByFieldName("path"); pathNode != nil {
				importPath = strings.Trim(pathNode.Content(content), "\"`")
			}
			pkg := path.Base(importPath)
			if pkg != "." && pkg != "/" && pkg != "" {
				alias := pkg
				if nameNode := n.ChildByFieldName("name"); nameNode != nil {
					alias = nameNode.Content(content)
				}
				if alias != "_" && alias != "." {
					aliases[alias] = pkg
				}
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
	return aliases
}

var goAssertionConcreteRE = regexp.MustCompile(`\(\s*\*?\s*([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)?)\s*\)`)

func goAssertionSpec(n *sitter.Node, content []byte, aliases map[string]string) (string, string, bool) {
	nameNode := n.ChildByFieldName("name")
	typeNode := n.ChildByFieldName("type")
	valueNode := n.ChildByFieldName("value")
	if nameNode == nil || typeNode == nil || valueNode == nil {
		return "", "", false
	}
	if strings.TrimSpace(nameNode.Content(content)) != "_" {
		return "", "", false
	}
	iface := normalizeGoTypeName(typeNode.Content(content), aliases)
	value := valueNode.Content(content)
	match := goAssertionConcreteRE.FindStringSubmatch(value)
	if len(match) != 2 {
		return "", "", false
	}
	impl := normalizeGoTypeName(match[1], aliases)
	if impl == "" || iface == "" {
		return "", "", false
	}
	return impl, iface, true
}

func normalizeGoTypeName(name string, aliases map[string]string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "*")
	name = strings.Trim(name, "()")
	name = strings.TrimSpace(name)
	parts := strings.Split(name, ".")
	if len(parts) == 2 {
		if mapped, ok := aliases[parts[0]]; ok {
			parts[0] = mapped
		}
		return parts[0] + "." + parts[1]
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return ""
}
