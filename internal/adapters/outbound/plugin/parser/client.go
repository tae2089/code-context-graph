// @index Subprocess client that drives one out-of-process parser plugin over NDJSON stdin/stdout.
package parser

import (
	"bufio"
	"encoding/json"
	"io"
	"os/exec"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// maxResponseLine bounds one NDJSON result line; large files can emit many nodes/edges.
const maxResponseLine = 8 * 1024 * 1024

// parseRequest is one NDJSON line core writes to a plugin: parse this file.
// @intent name the file (and its language) the plugin should read from disk and parse.
type parseRequest struct {
	FilePath string `json:"file_path"`
	Language string `json:"language"`
}

// parseResponse is one NDJSON line a plugin writes back: the file's nodes and edges, or an error.
// @intent carry per-file parse output as parser-neutral wire values matched to the request by path.
type parseResponse struct {
	FilePath string     `json:"file_path"`
	Nodes    []wireNode `json:"nodes"`
	Edges    []wireEdge `json:"edges"`
	Error    string     `json:"error,omitempty"`
}

// client drives a single long-lived parser plugin subprocess with per-file request/response lines.
// @intent reuse one process across files (no per-file spawn) while exposing per-file parsing to the adapter.
// @mutates writes requests to the child's stdin and advances the stdout scanner on each parse call
type client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner
}

// newClient starts the plugin subprocess and wires its stdin/stdout pipes.
// @intent take a caller-built command so discovery/tests decide how the plugin is launched.
func newClient(cmd *exec.Cmd) (*client, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, trace.Wrap(err, "open parser plugin stdin")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, trace.Wrap(err, "open parser plugin stdout")
	}
	if err := cmd.Start(); err != nil {
		return nil, trace.Wrap(err, "start parser plugin")
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), maxResponseLine)
	return &client{cmd: cmd, stdin: stdin, stdout: scanner}, nil
}

// parse sends one file request and converts the plugin's response into domain nodes and edges.
// @intent bridge the batch NDJSON protocol to the per-file Parser port one request/response at a time.
func (c *client) parse(filePath, language string) ([]graph.Node, []graph.Edge, error) {
	req, err := json.Marshal(parseRequest{FilePath: filePath, Language: language})
	if err != nil {
		return nil, nil, trace.Wrap(err, "encode parse request")
	}
	if _, err := c.stdin.Write(append(req, '\n')); err != nil {
		return nil, nil, trace.Wrap(err, "write parse request")
	}
	if !c.stdout.Scan() {
		if err := c.stdout.Err(); err != nil {
			return nil, nil, trace.Wrap(err, "read parse response")
		}
		return nil, nil, trace.New("parser plugin closed stream before responding")
	}

	var resp parseResponse
	if err := json.Unmarshal(c.stdout.Bytes(), &resp); err != nil {
		return nil, nil, trace.Wrap(err, "decode parse response")
	}
	if resp.Error != "" {
		return nil, nil, trace.New("parser plugin error: " + resp.Error)
	}

	nodes := make([]graph.Node, len(resp.Nodes))
	for i, wn := range resp.Nodes {
		nodes[i] = wn.toNode()
	}
	edges := make([]graph.Edge, 0, len(resp.Edges))
	for _, we := range resp.Edges {
		edge, err := we.toEdge()
		if err != nil {
			return nil, nil, trace.Wrap(err, "convert edge from parser plugin")
		}
		edges = append(edges, edge)
	}
	return nodes, edges, nil
}

// close ends the plugin stream and waits for the subprocess to exit.
// @intent release the child cleanly by closing its stdin (EOF) and reaping it.
func (c *client) close() error {
	_ = c.stdin.Close()
	return c.cmd.Wait()
}
