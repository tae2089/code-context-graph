// @index Outbound adapter that satisfies the ingest.Parser port via an out-of-process parser plugin.
package parser

import (
	"context"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// Adapter parses source files by delegating to a parser plugin subprocess.
// @intent let build/update workflows use an external parser through the same Parser port as the built-in one.
type Adapter struct {
	client   *client
	language string
}

// New builds an adapter over an already-started plugin client for one language.
// @intent bind a running plugin process to the language it parses behind the Parser port.
func New(c *client, language string) *Adapter {
	return &Adapter{client: c, language: language}
}

// Parse parses one file. content is ignored: the plugin reads the file from disk (protocol §1).
// @intent satisfy the context-free Parser entry point by delegating to the plugin.
func (a *Adapter) Parse(filePath string, _ []byte) ([]graph.Node, []graph.Edge, error) {
	return a.client.parse(filePath, a.language)
}

// ParseWithContext parses one file. content is ignored: the plugin reads from disk (protocol §1).
// @intent satisfy the context-aware Parser entry point by delegating to the plugin.
func (a *Adapter) ParseWithContext(_ context.Context, filePath string, _ []byte) ([]graph.Node, []graph.Edge, error) {
	return a.client.parse(filePath, a.language)
}

// Close stops the underlying plugin subprocess.
// @intent release the plugin process when the workflow finishes with this parser.
func (a *Adapter) Close() error {
	return a.client.close()
}
