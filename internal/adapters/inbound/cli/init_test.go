package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitCmd_ProjectLocal(t *testing.T) {
	dir := t.TempDir()

	deps := &Deps{}
	root := NewRootCmd(deps)
	root.SetArgs([]string{"init", "--project"})
	root.SetOut(&strings.Builder{})

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	if err := root.Execute(); err != nil {
		t.Fatalf("init --project failed: %v", err)
	}

	path := filepath.Join(dir, ".ccg.yaml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatalf("expected %s to exist", path)
	}

	data, _ := os.ReadFile(path)
	if len(data) == 0 {
		t.Fatal("config file is empty")
	}
}

func TestInitCmd_UserGlobal(t *testing.T) {
	dir := t.TempDir()

	deps := &Deps{}
	root := NewRootCmd(deps)
	root.SetOut(&strings.Builder{})

	globalDir := filepath.Join(dir, ".config", "ccg")
	root.SetArgs([]string{"init", "--user", "--config-home", dir})

	if err := root.Execute(); err != nil {
		t.Fatalf("init --user failed: %v", err)
	}

	path := filepath.Join(globalDir, ".ccg.yaml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatalf("expected %s to exist", path)
	}
}

func TestInitCmd_DefaultIsProject(t *testing.T) {
	dir := t.TempDir()

	deps := &Deps{}
	root := NewRootCmd(deps)
	root.SetArgs([]string{"init"})
	root.SetOut(&strings.Builder{})

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	if err := root.Execute(); err != nil {
		t.Fatalf("init (default) failed: %v", err)
	}

	path := filepath.Join(dir, ".ccg.yaml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatalf("expected %s to exist", path)
	}
}

func TestInitCmd_NoClobber(t *testing.T) {
	dir := t.TempDir()

	existing := filepath.Join(dir, ".ccg.yaml")
	os.WriteFile(existing, []byte("custom: true\n"), 0644)

	deps := &Deps{}
	root := NewRootCmd(deps)
	root.SetArgs([]string{"init", "--project"})
	root.SetOut(&strings.Builder{})

	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error when file already exists")
	}

	data, _ := os.ReadFile(existing)
	if string(data) != "custom: true\n" {
		t.Fatal("existing file was modified")
	}
}

func TestInitCmd_BothFlagsError(t *testing.T) {
	deps := &Deps{}
	root := NewRootCmd(deps)
	root.SetArgs([]string{"init", "--project", "--user"})
	root.SetOut(&strings.Builder{})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error when both --project and --user are set")
	}
}
