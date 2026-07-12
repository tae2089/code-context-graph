package treesitter

import (
	"context"
	"strings"
	"testing"

	"github.com/tae2089/code-context-graph/internal/app/ingest/binding"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

func TestPythonDocstring_PrefixBinding(t *testing.T) {
	content := readFixture(t, "python", "docstring_prefix.py")

	w := NewWalker(PythonSpec)
	nodes, _, walkerComments, err := w.ParseWithComments(context.Background(), "docstring_prefix.py", content)
	if err != nil {
		t.Fatalf("파싱 실패: %v", err)
	}

	b := binding.NewBinder()
	bindings := b.Bind(
		binderFromWalkerComments(walkerComments),
		nodes,
		"python",
		strings.Split(string(content), "\n"),
	)

	for _, name := range []string{"foo", "corge"} {
		if !hasIntentBinding(bindings, name, graph.NodeKindFunction) {
			t.Fatalf("%s 함수에 @intent 바인딩이 없음", name)
		}
	}

	for _, name := range []string{"bar", "baz", "qux", "quux"} {
		if hasIntentBinding(bindings, name, graph.NodeKindFunction) {
			t.Fatalf("%s 함수는 Python docstring prefix 규칙상 바인딩되면 안 됨", name)
		}
	}
}

func hasIntentBinding(bindings []binding.Binding, name string, kind graph.NodeKind) bool {
	for _, binding := range bindings {
		if binding.Node.Name != name || binding.Node.Kind != kind {
			continue
		}
		for _, tag := range binding.Annotation.Tags {
			if tag.Kind == graph.TagIntent {
				return true
			}
		}
	}
	return false
}
