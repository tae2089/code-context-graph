package benchmark

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

var (
	reMarkerStart = regexp.MustCompile(`^===BENCHMARK_QUERY_START id=([A-Za-z0-9_.-]+)===$`)
	reMarkerEnd   = regexp.MustCompile(`^===BENCHMARK_QUERY_END id=([A-Za-z0-9_.-]+)===$`)
)

// SessionMessage represents one line from a Claude Code session JSONL file.
type SessionMessage struct {
	Type      string          `json:"type"`
	Message   *MessagePayload `json:"message,omitempty"`
	Content   string          `json:"content,omitempty"` // tool_result plain string
	ToolUseID string          `json:"tool_use_id,omitempty"`
}

// MessagePayload is the nested message object inside a SessionMessage.
type MessagePayload struct {
	Role    string          `json:"role"`
	Content []ContentBlock  `json:"content"`
	Usage   *UsageInfo      `json:"usage,omitempty"`
}

// ContentBlock is a single content item (text, tool_use, etc.).
type ContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	Name  string          `json:"name,omitempty"` // tool_use
	ID    string          `json:"id,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

// UsageInfo holds token usage from Claude's response.
type UsageInfo struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// QuerySegment groups the messages belonging to a single benchmark query.
type QuerySegment struct {
	QueryID  string
	Messages []SessionMessage
}

// ParseJSONL reads a Claude Code session JSONL file and returns all parsed lines.
// Lines that are not valid JSON are silently skipped.
func ParseJSONL(path string) ([]SessionMessage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", path, err)
	}
	defer f.Close()

	var msgs []SessionMessage
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 10<<20), 10<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var m SessionMessage
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue // skip invalid lines
		}
		msgs = append(msgs, m)
	}
	return msgs, scanner.Err()
}

const (
	markerStart = "===BENCHMARK_QUERY_START id="
	markerEnd   = "===BENCHMARK_QUERY_END id="
)

// extractMarker checks if a SessionMessage contains a benchmark marker.
// Returns (queryID, isStart, found). Uses strict regex to prevent injection via crafted IDs.
func extractMarker(m SessionMessage) (queryID string, isStart bool, found bool) {
	var text string
	if m.Type == "tool_result" {
		text = m.Content
	} else if m.Message != nil {
		for _, b := range m.Message.Content {
			if b.Type == "text" {
				text = b.Text
				break
			}
		}
	}
	text = strings.TrimSpace(text)
	if sub := reMarkerStart.FindStringSubmatch(text); sub != nil {
		return sub[1], true, true
	}
	if sub := reMarkerEnd.FindStringSubmatch(text); sub != nil {
		return sub[1], false, true
	}
	return "", false, false
}

// ExtractQuerySegments splits a session's messages into per-query segments using markers.
// An unclosed segment (START without END) is included, spanning to the last message.
func ExtractQuerySegments(msgs []SessionMessage) ([]QuerySegment, error) {
	var segs []QuerySegment
	var current *QuerySegment

	for _, m := range msgs {
		id, isStart, found := extractMarker(m)
		if found {
			if isStart {
				current = &QuerySegment{QueryID: id}
			} else if current != nil && current.QueryID == id {
				segs = append(segs, *current)
				current = nil
			}
			continue
		}
		if current != nil {
			current.Messages = append(current.Messages, m)
		}
	}
	// Include unclosed segment
	if current != nil {
		segs = append(segs, *current)
	}
	return segs, nil
}

// ExtractToolCalls collects all tool_use blocks from a segment's messages.
func ExtractToolCalls(seg QuerySegment) []ToolCall {
	var calls []ToolCall
	for _, m := range seg.Messages {
		if m.Message == nil {
			continue
		}
		for _, b := range m.Message.Content {
			if b.Type == "tool_use" {
				var inputStr string
				if len(b.Input) > 0 {
					inputStr = string(b.Input)
				}
				calls = append(calls, ToolCall{Tool: b.Name, Input: inputStr})
			}
		}
	}
	return calls
}

// ExtractFilesRead returns deduplicated file paths from Read tool calls in the segment.
func ExtractFilesRead(seg QuerySegment) []string {
	var files []string
	seen := make(map[string]bool)
	for _, m := range seg.Messages {
		if m.Message == nil {
			continue
		}
		for _, b := range m.Message.Content {
			if b.Type != "tool_use" || b.Name != "Read" {
				continue
			}
			var inp struct {
				FilePath string `json:"file_path"`
			}
			if err := json.Unmarshal(b.Input, &inp); err == nil && inp.FilePath != "" && !seen[inp.FilePath] {
				seen[inp.FilePath] = true
				files = append(files, inp.FilePath)
			}
		}
	}
	return files
}

// ExtractTokens sums input and output token counts across all messages in a segment.
func ExtractTokens(seg QuerySegment) (inputTokens, outputTokens int) {
	for _, m := range seg.Messages {
		if m.Message != nil && m.Message.Usage != nil {
			inputTokens += m.Message.Usage.InputTokens
			outputTokens += m.Message.Usage.OutputTokens
		}
	}
	return
}

// ExtractAnswer returns the text of the last assistant text block in the segment.
func ExtractAnswer(seg QuerySegment) string {
	var last string
	for _, m := range seg.Messages {
		if m.Message == nil || m.Message.Role != "assistant" {
			continue
		}
		for _, b := range m.Message.Content {
			if b.Type == "text" && b.Text != "" {
				last = b.Text
			}
		}
	}
	return last
}

// ExtractRunResult builds a RunResult from a query segment.
func ExtractRunResult(queryID string, seg QuerySegment) RunResult {
	in, out := ExtractTokens(seg)
	return RunResult{
		QueryID:      queryID,
		ToolCalls:    ExtractToolCalls(seg),
		FilesRead:    ExtractFilesRead(seg),
		Answer:       ExtractAnswer(seg),
		InputTokens:  in,
		OutputTokens: out,
	}
}
