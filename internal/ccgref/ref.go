// @index CCG reference parser for cross-namespace annotation links.
package ccgref

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

const Scheme = "ccg"

// Ref is the structured form of a ccg:// annotation reference.
// @intent represent cross-namespace @see links without coupling annotations to graph storage.
type Ref struct {
	Raw       string `json:"raw"`
	Namespace string `json:"namespace"`
	Path      string `json:"path,omitempty"`
	Symbol    string `json:"symbol,omitempty"`
	Scope     string `json:"scope"`
}

// Is reports whether value uses the CCG reference scheme.
// @intent let callers branch between local @see values and cross-namespace CCG refs cheaply.
func Is(value string) bool {
	return strings.HasPrefix(strings.TrimSpace(value), Scheme+"://")
}

// Parse validates and normalizes a ccg:// reference.
// @intent decode ccg://{namespace}/{path}#{symbol} values used by @see annotations.
// @domainRule namespace is required and must be a single safe path segment.
// @domainRule path and symbol are optional; a path with no symbol represents a file or package path.
func Parse(value string) (*Ref, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return nil, fmt.Errorf("empty ref")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse ref: %w", err)
	}
	if u.Scheme != Scheme {
		return nil, fmt.Errorf("ref scheme must be %q", Scheme)
	}
	if u.User != nil || u.RawQuery != "" || u.Opaque != "" {
		return nil, fmt.Errorf("ccg ref must not contain userinfo, query, or opaque data")
	}
	namespace := strings.TrimSpace(u.Host)
	if err := validateNamespace(namespace); err != nil {
		return nil, err
	}
	refPath, err := normalizeRefPath(u.EscapedPath())
	if err != nil {
		return nil, err
	}
	symbol, err := url.PathUnescape(u.EscapedFragment())
	if err != nil {
		return nil, fmt.Errorf("decode symbol: %w", err)
	}
	symbol = strings.TrimSpace(symbol)
	if strings.ContainsAny(symbol, "\r\n") {
		return nil, fmt.Errorf("symbol must be a single line")
	}
	return &Ref{
		Raw:       raw,
		Namespace: namespace,
		Path:      refPath,
		Symbol:    symbol,
		Scope:     scopeFor(refPath, symbol),
	}, nil
}

// Display returns a compact human-readable form for UI labels and logs.
// @intent shorten ccg refs while preserving namespace, path, and symbol identity.
func (r Ref) Display() string {
	var b strings.Builder
	b.WriteString(r.Namespace)
	if r.Path != "" {
		b.WriteString("/")
		b.WriteString(r.Path)
	}
	if r.Symbol != "" {
		b.WriteString("#")
		b.WriteString(r.Symbol)
	}
	return b.String()
}

// @intent reject namespace values that could escape namespace storage roots.
func validateNamespace(namespace string) error {
	if namespace == "" {
		return fmt.Errorf("namespace is required")
	}
	if namespace == "." || namespace == ".." || strings.ContainsAny(namespace, `/\`) || strings.HasPrefix(namespace, "..") {
		return fmt.Errorf("invalid namespace: must be a single safe name")
	}
	return nil
}

// @intent normalize the URI path part into the same slash-separated file paths used by graph nodes.
func normalizeRefPath(escapedPath string) (string, error) {
	if escapedPath == "" || escapedPath == "/" {
		return "", nil
	}
	decoded, err := url.PathUnescape(escapedPath)
	if err != nil {
		return "", fmt.Errorf("decode path: %w", err)
	}
	decoded = strings.TrimPrefix(decoded, "/")
	if decoded == "" {
		return "", nil
	}
	if strings.Contains(decoded, `\`) || strings.ContainsAny(decoded, "\r\n") {
		return "", fmt.Errorf("invalid path: path traversal not allowed")
	}
	clean := path.Clean(decoded)
	if clean == "." {
		return "", nil
	}
	if strings.HasPrefix(clean, "../") || clean == ".." || strings.HasPrefix(clean, "/") {
		return "", fmt.Errorf("invalid path: path traversal not allowed")
	}
	return clean, nil
}

// @intent classify refs for clients that want to render namespace, path, and symbol scopes differently.
func scopeFor(refPath, symbol string) string {
	if symbol != "" {
		return "symbol"
	}
	if refPath != "" {
		return "path"
	}
	return "namespace"
}
