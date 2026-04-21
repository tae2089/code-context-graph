package treesitter

import (
	"context"
	"strings"
	"testing"

	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/parse"
)

func TestPythonDocstring_PrefixBinding(t *testing.T) {
	content := readFixture(t, "python", "docstring_prefix.py")

	w := NewWalker(PythonSpec)
	nodes, _, walkerComments, err := w.ParseWithComments(context.Background(), "docstring_prefix.py", content)
	if err != nil {
		t.Fatalf("파싱 실패: %v", err)
	}

	b := parse.NewBinder()
	bindings := b.Bind(
		binderFromWalkerComments(walkerComments),
		nodes,
		"python",
		strings.Split(string(content), "\n"),
	)

	for _, name := range []string{"foo", "bar", "baz", "qux", "quux", "corge"} {
		if !hasIntentBinding(bindings, name, model.NodeKindFunction) {
			t.Fatalf("%s 함수에 @intent 바인딩이 없음", name)
		}
	}
}

func hasIntentBinding(bindings []parse.Binding, name string, kind model.NodeKind) bool {
	for _, binding := range bindings {
		if binding.Node.Name != name || binding.Node.Kind != kind {
			continue
		}
		for _, tag := range binding.Annotation.Tags {
			if tag.Kind == model.TagIntent {
				return true
			}
		}
	}
	return false
}
