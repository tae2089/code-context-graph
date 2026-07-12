// walker.go `nameIndex` dedup이 동명 심볼을 오병합하는지 실측하는 테스트.
//
// 배경: walker.go:343-356의 nameIndex는 키가 `name` 단독이다. 같은 이름 메서드가
// 서로 다른 클래스 본문에 존재하면 두 번째 매칭이 첫 번째를 갱신할 수 있다.
// 이 테스트는 Python/TypeScript 각각 동명 메서드 fixture를 파싱해
// (1) 노드가 둘 다 수집되는지, (2) StartLine이 겹치지 않는지 확인한다.
package treesitter

import (
	"context"
	"testing"
)

func TestWalker_NameIndexDedup_Python_DupMethods(t *testing.T) {
	content := readFixture(t, "python", "dup_methods.py")
	w := NewWalker(PythonSpec)
	nodes, _, _, err := w.ParseWithComments(context.Background(), "dup_methods.py", content)
	if err != nil {
		t.Fatalf("파싱 실패: %v", err)
	}

	t.Log("--- Python dup_methods 파싱된 노드 ---")
	logNodeInfo(t, nodes)

	var saveNodes []int
	for i, n := range nodes {
		if n.Name == "save" {
			saveNodes = append(saveNodes, i)
		}
	}

	t.Logf("`save` 이름 노드 개수: %d (기대: 2 — Alpha.save, Beta.save)", len(saveNodes))
	if len(saveNodes) < 2 {
		t.Errorf("[nameIndex dedup 오병합 의심] save 메서드가 하나만 수집됨. nodes=%d", len(nodes))
		for _, i := range saveNodes {
			t.Logf("  유일한 save: QN=%s StartLine=%d EndLine=%d", nodes[i].QualifiedName, nodes[i].StartLine, nodes[i].EndLine)
		}
		return
	}

	startLines := make(map[int]bool)
	for _, i := range saveNodes {
		if startLines[nodes[i].StartLine] {
			t.Errorf("[nameIndex dedup 오병합] save 노드 둘 다 StartLine=%d로 중복", nodes[i].StartLine)
		}
		startLines[nodes[i].StartLine] = true
	}
}

func TestWalker_NameIndexDedup_TypeScript_DupMethods(t *testing.T) {
	content := readFixture(t, "typescript", "dup_methods.ts")
	w := NewWalker(TypeScriptSpec)
	nodes, _, _, err := w.ParseWithComments(context.Background(), "dup_methods.ts", content)
	if err != nil {
		t.Fatalf("파싱 실패: %v", err)
	}

	t.Log("--- TypeScript dup_methods 파싱된 노드 ---")
	logNodeInfo(t, nodes)

	var renderNodes []int
	for i, n := range nodes {
		if n.Name == "render" {
			renderNodes = append(renderNodes, i)
		}
	}

	t.Logf("`render` 이름 노드 개수: %d (기대: 2 — Alpha.render, Beta.render)", len(renderNodes))
	if len(renderNodes) < 2 {
		t.Errorf("[nameIndex dedup 오병합 의심] render 메서드가 하나만 수집됨. nodes=%d", len(nodes))
		for _, i := range renderNodes {
			t.Logf("  유일한 render: QN=%s StartLine=%d EndLine=%d", nodes[i].QualifiedName, nodes[i].StartLine, nodes[i].EndLine)
		}
		return
	}

	startLines := make(map[int]bool)
	for _, i := range renderNodes {
		if startLines[nodes[i].StartLine] {
			t.Errorf("[nameIndex dedup 오병합] render 노드 둘 다 StartLine=%d로 중복", nodes[i].StartLine)
		}
		startLines[nodes[i].StartLine] = true
	}
}
