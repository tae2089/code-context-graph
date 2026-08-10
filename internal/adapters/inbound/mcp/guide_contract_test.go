package mcp

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	searchwire "github.com/tae2089/code-context-graph/internal/app/search/wire"
)

// guideFiles are the two documents that state the MCP contract in prose. They
// are listed together because the failure this file exists to catch is one of
// them being fixed and the other left behind: a reader who follows the Korean
// guide and a reader who follows the English one must be told the same rules.
var guideFiles = []string{
	filepath.Join("guide", "mcp-tools.md"),
	filepath.Join("guide", "ko", "mcp-tools.md"),
}

// guideDocumentedResponses are the MCP payloads whose own field names the guides
// state as rules rather than merely describe.
//
// The guides close by pointing at the live MCP schema for parameters and
// response shapes, so they do not claim to spell out every tool's payload — and
// a check that demanded they did would freeze design rather than guard a
// contract. These two are the ones the guides do spell out, because both carry a
// rule a caller has to act on: search says an answer is complete only when
// `truncated` and `pool_truncated` are both false, and describe says a target
// the graph does not hold comes back under `scope` rather than as an error. A
// field added to either payload changes what those rules are worth, and a guide
// that never names it leaves an agent acting on the old contract.
//
// @intent tie the documented response contract to the structs that produce it, so a new field cannot arrive undocumented.
var guideDocumentedResponses = map[string]any{
	"search":   searchwire.Response{},
	"describe": describeResponse{},
}

func TestGuidesNameEveryTopLevelResponseField(t *testing.T) {
	for _, guide := range guideFiles {
		text := readGuide(t, guide)
		for tool, response := range guideDocumentedResponses {
			for _, field := range topLevelJSONFields(t, response) {
				if !strings.Contains(text, "`"+field+"`") {
					t.Errorf("%s never names %q, a top-level field of the %s response", guide, field, tool)
				}
			}
		}
	}
}

func TestGuidesListEveryRegisteredTool(t *testing.T) {
	registered := registeredToolNames(t)

	for _, guide := range guideFiles {
		text := readGuide(t, guide)
		listed := guideToolRows(t, guide, text)

		for _, name := range registered {
			if !slices.Contains(listed, name) {
				t.Errorf("%s does not list registered tool %q", guide, name)
			}
		}
		for _, name := range listed {
			if !slices.Contains(registered, name) {
				t.Errorf("%s lists %q, which no MCP tool registers", guide, name)
			}
		}
	}
}

func TestGuidesCountToolsTheRegistryActuallyHas(t *testing.T) {
	want := len(registeredToolNames(t))
	// Only a digit written right in front of the word it counts is read. Spelling
	// the number out, or moving it into a different sentence, is a rewrite rather
	// than a contract change, and this check stays quiet for it.
	counted := regexp.MustCompile(`(\d+)[^.\n]{0,12}?(?:MCP tools|MCP 도구|tools|개 도구)`)

	for _, guide := range guideFiles {
		text := readGuide(t, guide)
		for _, match := range counted.FindAllStringSubmatch(text, -1) {
			got, err := strconv.Atoi(match[1])
			if err != nil {
				continue
			}
			if got != want {
				t.Errorf("%s says %q, but %d tools are registered", guide, strings.TrimSpace(match[0]), want)
			}
		}
	}
}

// readGuide loads one guide and refuses to hand back nothing.
//
// Every check in this file is "does this name appear in the text", which an
// empty string passes for the wrong reason. A guide that moved or was deleted
// has to stop the suite, not quietly excuse it.
func readGuide(t *testing.T, relative string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve this test file's own path")
	}
	// internal/adapters/inbound/mcp -> repository root.
	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", "..", ".."))
	path := filepath.Join(root, relative)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		t.Fatalf("%s is empty, so this check would pass without reading anything", path)
	}
	return string(raw)
}

// topLevelJSONFields reads the wire names a response serializes, from the struct
// itself rather than from a second list somebody has to remember to update.
func topLevelJSONFields(t *testing.T, response any) []string {
	t.Helper()
	typ := reflect.TypeOf(response)
	if typ.Kind() != reflect.Struct {
		t.Fatalf("%T is not a struct", response)
	}
	var fields []string
	for i := range typ.NumField() {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			t.Fatalf("%s.%s has no json tag, so its wire name cannot be checked", typ.Name(), field.Name)
		}
		fields = append(fields, name)
	}
	if len(fields) == 0 {
		t.Fatalf("%s serializes no fields, so this check would pass without checking anything", typ.Name())
	}
	slices.Sort(fields)
	return fields
}

// registeredToolNames asks a real server which tools it registered.
//
// Asking the server is the point: a list written out here would be the second
// copy of the tool set, and the guide drifting from a second copy is the bug
// rather than the check.
func registeredToolNames(t *testing.T) []string {
	t.Helper()
	tools := NewServer(&Deps{}).ListTools()
	if len(tools) == 0 {
		t.Fatal("the server registered no tools, so this lookup is broken rather than the guide")
	}
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// guideToolRows reads the tool names out of the guide's own tables.
//
// Only the first cell of a table row counts, so prose keeps its freedom to name
// a tool that no longer exists — both guides say `find_by_intent` was removed,
// and saying so must not be mistaken for claiming it back.
func guideToolRows(t *testing.T, guide, text string) []string {
	t.Helper()
	row := regexp.MustCompile("(?m)^\\|\\s*`([a-z0-9_]+)`\\s*\\|")
	var names []string
	for _, match := range row.FindAllStringSubmatch(text, -1) {
		names = append(names, match[1])
	}
	if len(names) == 0 {
		t.Fatalf("%s lists no tools in a table, so this check would pass without reading any", guide)
	}
	slices.Sort(names)
	return names
}
