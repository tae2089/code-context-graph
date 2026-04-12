package changes

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGitClient_ChangedFiles(t *testing.T) {
	dir := initTestRepo(t)
	writeFile(t, dir, "hello.go", "package main\n")
	gitCommit(t, dir, "initial")
	writeFile(t, dir, "hello.go", "package main\n\nfunc Hello() {}\n")

	git := NewExecGitClient()
	files, err := git.ChangedFiles(context.Background(), dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 changed file, got %d", len(files))
	}
	if files[0] != "hello.go" {
		t.Errorf("expected hello.go, got %s", files[0])
	}
}

func TestGitClient_DiffHunks(t *testing.T) {
	dir := initTestRepo(t)
	writeFile(t, dir, "hello.go", "package main\n\nfunc Old() {}\n")
	gitCommit(t, dir, "initial")
	writeFile(t, dir, "hello.go", "package main\n\nfunc New() {}\n")

	git := NewExecGitClient()
	hunks, err := git.DiffHunks(context.Background(), dir, "HEAD", []string{"hello.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hunks) == 0 {
		t.Fatal("expected at least 1 hunk")
	}
	if hunks[0].FilePath != "hello.go" {
		t.Errorf("expected hello.go, got %s", hunks[0].FilePath)
	}
}

func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "git", "init")
	run(t, dir, "git", "config", "user.email", "test@test.com")
	run(t, dir, "git", "config", "user.name", "Test")
	return dir
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func gitCommit(t *testing.T, dir, msg string) {
	t.Helper()
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", msg)
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}
