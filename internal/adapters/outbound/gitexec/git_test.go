package gitexec

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestGitClient_RejectsOptionLikeBaseRef(t *testing.T) {
	dir := initTestRepo(t)
	writeFile(t, dir, "hello.go", "package main\n")
	gitCommit(t, dir, "initial")

	git := NewExecGitClient()
	for _, base := range []string{"--output=/tmp/pwned", "-U9999", "--no-index"} {
		if _, err := git.ChangedFiles(context.Background(), dir, base); err == nil {
			t.Errorf("ChangedFiles accepted option-like base %q", base)
		}
		if _, err := git.DiffHunks(context.Background(), dir, base, []string{"hello.go"}); err == nil {
			t.Errorf("DiffHunks accepted option-like base %q", base)
		}
	}
}

func TestGitClient_ChangedFiles_NonASCIIPath(t *testing.T) {
	dir := initTestRepo(t)
	writeFile(t, dir, "한글.go", "package main\n")
	gitCommit(t, dir, "initial")
	writeFile(t, dir, "한글.go", "package main\n\nfunc Hello() {}\n")

	git := NewExecGitClient()
	files, err := git.ChangedFiles(context.Background(), dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "한글.go" {
		t.Fatalf("expected unquoted 한글.go, got %v", files)
	}

	hunks, err := git.DiffHunks(context.Background(), dir, "HEAD", []string{"한글.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hunks) == 0 || hunks[0].FilePath != "한글.go" {
		t.Fatalf("expected hunk for unquoted 한글.go, got %+v", hunks)
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

func TestRunGitLimitedWithMaxRejectsLargeOutput(t *testing.T) {
	dir := initTestRepo(t)
	_, err := runGitLimitedWithMax(context.Background(), dir, []string{"--version"}, 1)
	if err == nil {
		t.Fatal("expected output cap error")
	}
	if !strings.Contains(err.Error(), "git output exceeds") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunGitLimitedHonorsContextTimeout(t *testing.T) {
	dir := initTestRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)

	_, err := runGitLimited(ctx, dir, []string{"status"})
	if err == nil {
		t.Fatal("expected context timeout error")
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
