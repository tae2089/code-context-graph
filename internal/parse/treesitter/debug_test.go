package treesitter

import (
	"testing"
	"fmt"
)

func TestDebug(t *testing.T) {
	src := `package main

type User struct {
	Name string
}
`
	w := NewWalker(GoSpec)
	nodes, edges, err := w.Parse("main.go", []byte(src))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i, n := range nodes {
		fmt.Printf("Node %d: %#v\n", i, n)
	}
	for i, e := range edges {
		fmt.Printf("Edge %d: %#v\n", i, e)
	}
}
