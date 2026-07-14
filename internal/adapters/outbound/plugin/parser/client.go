// @index Subprocess client that drives one out-of-process parser plugin over NDJSON stdin/stdout.
package parser

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// maxResponseLine bounds one NDJSON result line; large files can emit many nodes/edges.
const maxResponseLine = 8 * 1024 * 1024

// maxStderrTail bounds retained plugin stderr so a chatty plugin cannot grow memory unbounded.
const maxStderrTail = 16 * 1024

// defaultCloseTimeout bounds how long close waits for a plugin to exit before killing it.
const defaultCloseTimeout = 5 * time.Second

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

// syncBuffer is a concurrency-safe bounded sink for the plugin's stderr.
// @intent retain recent plugin diagnostics for error messages without unbounded growth or data races.
type syncBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > maxStderrTail {
		b.buf = b.buf[len(b.buf)-maxStderrTail:]
	}
	return len(p), nil
}

func (b *syncBuffer) tail() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// client drives a single long-lived parser plugin subprocess with per-file request/response lines.
// @intent reuse one process across files (no per-file spawn) while exposing per-file parsing to the adapter.
// @mutates writes requests to the child's stdin, advances the stdout scanner, and latches broken on any transport failure
type client struct {
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	stdout       *bufio.Scanner
	stderr       *syncBuffer
	closeTimeout time.Duration
	broken       error // latched on any transport/framing failure; all later calls fail fast
}

// newClient starts the plugin subprocess and wires its stdin/stdout/stderr pipes.
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
	stderr := &syncBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, trace.Wrap(err, "start parser plugin")
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), maxResponseLine)
	return &client{cmd: cmd, stdin: stdin, stdout: scanner, stderr: stderr, closeTimeout: defaultCloseTimeout}, nil
}

// fail latches the first transport/framing failure, kills the plugin, and returns the latched error.
// @intent make a desynced or misbehaving plugin fail fast on every later call instead of misattributing output.
func (c *client) fail(err error) error {
	if c.broken == nil {
		if tail := c.stderr.tail(); tail != "" {
			err = trace.Wrap(err, "parser plugin stderr: "+tail)
		}
		c.broken = err
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
	}
	return c.broken
}

// parse sends one file request and converts the plugin's response into domain nodes and edges.
// @intent bridge the batch NDJSON protocol to the per-file Parser port with ctx-aware, framing-checked reads.
func (c *client) parse(ctx context.Context, filePath, language string) ([]graph.Node, []graph.Edge, error) {
	if c.broken != nil {
		return nil, nil, c.broken
	}
	req, err := json.Marshal(parseRequest{FilePath: filePath, Language: language})
	if err != nil {
		return nil, nil, trace.Wrap(err, "encode parse request")
	}
	if _, err := c.stdin.Write(append(req, '\n')); err != nil {
		return nil, nil, c.fail(trace.Wrap(err, "write parse request"))
	}

	line, err := c.readLine(ctx)
	if err != nil {
		return nil, nil, err // readLine already latched the failure
	}

	var resp parseResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		// A non-JSON line means the stream is desynced (e.g. a stray stdout log); poison the client.
		return nil, nil, c.fail(trace.Wrap(err, "decode parse response"))
	}
	if resp.FilePath != filePath {
		// Response does not match the request: the stream is misaligned; poison the client.
		return nil, nil, c.fail(trace.New("parser plugin response for " + resp.FilePath + " does not match request " + filePath))
	}
	if resp.Error != "" {
		// A well-framed per-file error: return it but keep the client healthy.
		return nil, nil, trace.New("parser plugin error: " + resp.Error)
	}

	nodes := make([]graph.Node, 0, len(resp.Nodes))
	for _, wn := range resp.Nodes {
		n, err := wn.toNode()
		if err != nil {
			return nil, nil, trace.Wrap(err, "convert node from parser plugin")
		}
		nodes = append(nodes, n)
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

// readLine reads one response line, aborting (and killing the plugin) if ctx is cancelled first.
// @intent honor cancellation/deadline so a hung plugin cannot block the caller indefinitely.
func (c *client) readLine(ctx context.Context) ([]byte, error) {
	type result struct {
		line []byte
		err  error
	}
	ch := make(chan result, 1) // buffered so the reader goroutine never leaks on ctx cancel
	go func() {
		if c.stdout.Scan() {
			b := c.stdout.Bytes()
			ch <- result{line: append([]byte(nil), b...)}
			return
		}
		err := c.stdout.Err()
		if err == nil {
			err = trace.New("parser plugin closed stream before responding")
		}
		ch <- result{err: err}
	}()

	select {
	case <-ctx.Done():
		return nil, c.fail(trace.Wrap(ctx.Err(), "parse cancelled"))
	case r := <-ch:
		if r.err != nil {
			return nil, c.fail(trace.Wrap(r.err, "read parse response"))
		}
		return r.line, nil
	}
}

// close ends the plugin stream and reaps the subprocess, killing it if it does not exit in time.
// @intent release the child cleanly on stdin EOF, but never block shutdown on a plugin that ignores it.
func (c *client) close() error {
	_ = c.stdin.Close()
	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			if tail := c.stderr.tail(); tail != "" {
				return trace.Wrap(err, "parser plugin stderr: "+tail)
			}
		}
		return err
	case <-time.After(c.closeTimeout):
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		<-done // reap the killed process
		return trace.New("parser plugin did not exit within " + c.closeTimeout.String() + "; killed")
	}
}
