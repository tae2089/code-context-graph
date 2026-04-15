package webhook

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func initBareRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bareDir := filepath.Join(dir, "bare.git")

	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}

	run("init", "--bare", bareDir)

	workDir := filepath.Join(dir, "work")
	run("clone", bareDir, workDir)

	if err := os.WriteFile(filepath.Join(workDir, "hello.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	runIn := func(d string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = d
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v in %s failed: %v\n%s", args, d, err, out)
		}
	}

	runIn(workDir, "add", ".")
	runIn(workDir, "commit", "-m", "initial")
	runIn(workDir, "push")

	return bareDir
}

func addFileToBareRepo(t *testing.T, bareDir, fileName, content string) {
	t.Helper()
	dir := t.TempDir()
	workDir := filepath.Join(dir, "work")

	runIn := func(d string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = d
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v in %s failed: %v\n%s", args, d, err, out)
		}
	}

	cmd := exec.Command("git", "clone", bareDir, workDir)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone failed: %v\n%s", err, out)
	}

	if err := os.WriteFile(filepath.Join(workDir, fileName), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	runIn(workDir, "add", ".")
	runIn(workDir, "commit", "-m", "add "+fileName)
	runIn(workDir, "push")
}

func TestGitOps_RepoDir(t *testing.T) {
	got := RepoDir("/var/repos", "acme/pay-svc")
	want := filepath.Join("/var/repos", "acme/pay-svc")
	if got != want {
		t.Errorf("RepoDir = %q, want %q", got, want)
	}
}

func TestGitOps_CloneRepo(t *testing.T) {
	bareDir := initBareRepo(t)
	destRoot := t.TempDir()
	ns := "test-ns"

	err := CloneOrPull(context.Background(), bareDir, destRoot, ns, nil)
	if err != nil {
		t.Fatalf("CloneOrPull (clone) failed: %v", err)
	}

	dest := RepoDir(destRoot, ns)
	if _, err := os.Stat(filepath.Join(dest, "hello.txt")); err != nil {
		t.Errorf("expected hello.txt to exist after clone: %v", err)
	}
}

func TestGitOps_PullUpdates(t *testing.T) {
	bareDir := initBareRepo(t)
	destRoot := t.TempDir()
	ns := "test-ns"

	err := CloneOrPull(context.Background(), bareDir, destRoot, ns, nil)
	if err != nil {
		t.Fatalf("initial clone failed: %v", err)
	}

	addFileToBareRepo(t, bareDir, "new.txt", "new content")

	err = CloneOrPull(context.Background(), bareDir, destRoot, ns, nil)
	if err != nil {
		t.Fatalf("CloneOrPull (pull) failed: %v", err)
	}

	dest := RepoDir(destRoot, ns)
	data, err := os.ReadFile(filepath.Join(dest, "new.txt"))
	if err != nil {
		t.Fatalf("expected new.txt to exist after pull: %v", err)
	}
	if string(data) != "new content" {
		t.Errorf("new.txt content = %q, want %q", string(data), "new content")
	}
}
