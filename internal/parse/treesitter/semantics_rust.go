package treesitter

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/tae2089/code-context-graph/internal/model"
)

// RustSemantics normalizes Rust trait impl names beyond raw query captures.
// @intent keep trait implementation edges stable when impl headers contain paths or generic arguments.
type RustSemantics struct{}

// AdditionalEdges returns no Rust-only file-local edges beyond generic walker output.
// @intent satisfy LanguageSemantics while keeping Rust relationship normalization in definition hooks.
func (RustSemantics) AdditionalEdges(SemanticContext) []model.Edge {
	return nil
}

// CallRewriter normalizes Rust qualified trait path and UFCS calls into a stable resolver contract.
// @intent preserve exact trait path and optional concrete type information without changing generic walker logic.
func (RustSemantics) CallRewriter(ctx SemanticContext) CallRewriter {
	imports := rustImportAliases(ctx.Root, ctx.Content)
	return rustQualifiedCallRewriter{imports: imports}
}

// DefinitionName normalizes Rust impl target type names captured from impl headers.
// @intent keep impl_item class names stable when the captured type includes generic arguments.
func (RustSemantics) DefinitionName(ctx DefinitionContext) string {
	if ctx.Definition == nil || ctx.Definition.Type() != "impl_item" {
		return ctx.Name
	}
	if typ := ctx.Definition.ChildByFieldName("type"); typ != nil {
		if normalized := rustNormalizeTypeName(typ.Content(ctx.Content)); normalized != "" {
			return normalized
		}
	}
	return ctx.Name
}

// ImplementedTypes normalizes trait names captured from Rust impl blocks.
// @intent strip generic arguments and preserve full path segments for trait implementation edges.
func (RustSemantics) ImplementedTypes(ctx DefinitionContext) []string {
	if ctx.Definition == nil || ctx.Definition.Type() != "impl_item" {
		return append([]string(nil), ctx.ImplementedTypes...)
	}
	trait := rustImplTraitName(ctx.Definition, ctx.Content)
	if trait == "" {
		return append([]string(nil), ctx.ImplementedTypes...)
	}
	imports := rustImportAliases(ctx.Root, ctx.Content)
	trait = rustQualifyImportedTypeName(trait, imports)
	return []string{trait}
}

func rustImplTraitName(def *sitter.Node, content []byte) string {
	if def == nil {
		return ""
	}
	trait := def.ChildByFieldName("trait")
	if trait == nil {
		return ""
	}
	return rustNormalizeTypeName(trait.Content(content))
}

type rustQualifiedCallRewriter struct {
	imports map[string]string
}

func (r rustQualifiedCallRewriter) RewriteCall(ctx CallRewriteContext) string {
	callee := strings.TrimSpace(ctx.Callee)
	if callee == "" || !strings.Contains(callee, "::") {
		return callee
	}
	if concrete, trait, method, ok := rustParseUFCSCall(callee); ok {
		trait = rustQualifyImportedTypeName(trait, r.imports)
		trait = rustNormalizeTypeName(trait)
		concrete = rustNormalizeTypeName(concrete)
		if trait == "" || method == "" || concrete == "" {
			return callee
		}
		return "<" + concrete + " as " + trait + ">::" + method
	}
	if trait, method, ok := rustParseQualifiedTraitCall(callee); ok {
		trait = rustQualifyImportedTypeName(trait, r.imports)
		trait = rustNormalizeTypeName(trait)
		if trait == "" || method == "" {
			return callee
		}
		return trait + "::" + method
	}
	return callee
}

func rustParseQualifiedTraitCall(callee string) (string, string, bool) {
	parts := strings.Split(callee, "::")
	if len(parts) < 2 {
		return "", "", false
	}
	method := strings.TrimSpace(parts[len(parts)-1])
	trait := strings.TrimSpace(strings.Join(parts[:len(parts)-1], "::"))
	if trait == "" || method == "" {
		return "", "", false
	}
	return trait, method, true
}

func rustParseUFCSCall(callee string) (string, string, string, bool) {
	callee = strings.TrimSpace(callee)
	if !strings.HasPrefix(callee, "<") {
		return "", "", "", false
	}
	close := rustMatchingAngle(callee, 0)
	if close < 0 || close+2 >= len(callee) || callee[close+1] != ':' || callee[close+2] != ':' {
		return "", "", "", false
	}
	method := strings.TrimSpace(callee[close+3:])
	inner := strings.TrimSpace(callee[1:close])
	idx := rustTopLevelAsIndex(inner)
	if idx < 0 {
		return "", "", "", false
	}
	concrete := strings.TrimSpace(inner[:idx])
	trait := strings.TrimSpace(inner[idx+len(" as "):])
	if concrete == "" || trait == "" || method == "" {
		return "", "", "", false
	}
	return concrete, trait, method, true
}

func rustNormalizeTypeName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var b strings.Builder
	depth := 0
	for _, r := range raw {
		switch r {
		case '<':
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func rustQualifyImportedTypeName(typeName string, imports map[string]string) string {
	typeName = strings.TrimSpace(typeName)
	if typeName == "" || strings.Contains(typeName, "::") {
		return typeName
	}
	if imports != nil {
		if qualified := imports[typeName]; qualified != "" {
			return qualified
		}
	}
	return typeName
}

func rustImportAliases(root *sitter.Node, content []byte) map[string]string {
	if root == nil {
		return nil
	}
	aliases := make(map[string]string)
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if n.Type() == "use_declaration" {
			rustCollectImportAliases(aliases, n.Content(content))
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
	if len(aliases) == 0 {
		return nil
	}
	return aliases
}

func rustCollectImportAliases(aliases map[string]string, raw string) {
	for _, path := range rustExpandUseDeclaration(raw) {
		if path == "" || strings.HasSuffix(path, "::*") {
			continue
		}
		alias, qualified := rustImportAliasEntry(path)
		if alias == "" || qualified == "" {
			continue
		}
		aliases[alias] = qualified
	}
}

func rustExpandUseDeclaration(raw string) []string {
	raw = rustTrimUseDeclaration(raw)
	if raw == "" {
		return nil
	}
	open := strings.Index(raw, "{")
	if open < 0 {
		return []string{strings.TrimSpace(raw)}
	}
	close := rustMatchingBrace(raw, open)
	if close < 0 {
		return []string{strings.TrimSpace(raw)}
	}
	prefix := strings.TrimSuffix(strings.TrimSpace(raw[:open]), "::")
	inner := raw[open+1 : close]
	parts := rustSplitTopLevel(inner, ',')
	var expanded []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if part == "self" {
			if prefix != "" {
				expanded = append(expanded, prefix)
			}
			continue
		}
		candidate := part
		if prefix != "" {
			candidate = prefix + "::" + part
		}
		expanded = append(expanded, rustExpandUseDeclaration(candidate)...)
	}
	return expanded
}

func rustTrimUseDeclaration(raw string) string {
	raw = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(raw), ";"))
	if idx := strings.Index(raw, "use "); idx >= 0 {
		return strings.TrimSpace(raw[idx+len("use "):])
	}
	return raw
}

func rustImportAliasEntry(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	if before, after, ok := strings.Cut(raw, " as "); ok {
		qualified := strings.TrimSpace(before)
		alias := strings.TrimSpace(after)
		return alias, qualified
	}
	return pathBaseName(raw, "::"), raw
}

func rustMatchingBrace(raw string, open int) int {
	depth := 0
	for i := open; i < len(raw); i++ {
		switch raw[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func rustMatchingAngle(raw string, open int) int {
	depth := 0
	for i := open; i < len(raw); i++ {
		switch raw[i] {
		case '<':
			depth++
		case '>':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func rustTopLevelAsIndex(raw string) int {
	depthAngle := 0
	depthParen := 0
	depthBracket := 0
	for i := 0; i+4 <= len(raw); i++ {
		switch raw[i] {
		case '<':
			depthAngle++
		case '>':
			if depthAngle > 0 {
				depthAngle--
			}
		case '(':
			depthParen++
		case ')':
			if depthParen > 0 {
				depthParen--
			}
		case '[':
			depthBracket++
		case ']':
			if depthBracket > 0 {
				depthBracket--
			}
		}
		if depthAngle == 0 && depthParen == 0 && depthBracket == 0 && strings.HasPrefix(raw[i:], " as ") {
			return i
		}
	}
	return -1
}

func rustSplitTopLevel(raw string, sep byte) []string {
	var parts []string
	depth := 0
	start := 0
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case '{':
			depth++
		case '}':
			if depth > 0 {
				depth--
			}
		case sep:
			if depth == 0 {
				parts = append(parts, strings.TrimSpace(raw[start:i]))
				start = i + 1
			}
		}
	}
	parts = append(parts, strings.TrimSpace(raw[start:]))
	return parts
}
