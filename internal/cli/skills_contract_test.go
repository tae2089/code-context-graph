package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type skillContract struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Metadata    struct {
		Version  string `yaml:"version"`
		OpenClaw struct {
			Category string `yaml:"category"`
			Domain   string `yaml:"domain"`
		} `yaml:"openclaw"`
		Requires struct {
			Bins   []string `yaml:"bins"`
			Skills []string `yaml:"skills"`
		} `yaml:"requires"`
		CLIHelp string `yaml:"cliHelp"`
	} `yaml:"metadata"`
}

func TestProjectSkillsDeclareRuntimeContract(t *testing.T) {
	type expectation struct {
		domain       string
		requiresCore bool
		cliHelp      string
	}
	want := map[string]expectation{
		"ccg":           {domain: "core", cliHelp: "ccg --help"},
		"ccg-analyze":   {domain: "analysis", requiresCore: true},
		"ccg-annotate":  {domain: "annotation", requiresCore: true},
		"ccg-docs":      {domain: "documentation", requiresCore: true, cliHelp: "ccg docs --help"},
		"ccg-namespace": {domain: "namespace", requiresCore: true, cliHelp: "ccg build --help"},
	}
	skillsRoot := filepath.Join("..", "..", "skills")
	entries, err := os.ReadDir(skillsRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(skillsRoot, entry.Name(), "SKILL.md")); err != nil {
			continue
		}
		if _, ok := want[entry.Name()]; !ok {
			t.Errorf("skill %q has no declared contract expectation", entry.Name())
		}
	}

	for name, expected := range want {
		t.Run(name, func(t *testing.T) {
			skillDir := filepath.Join(skillsRoot, name)
			raw, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
			if err != nil {
				t.Fatalf("read skill: %v", err)
			}
			contract := parseSkillContract(t, raw)
			if contract.Name != name {
				t.Errorf("name = %q, want %q", contract.Name, name)
			}
			if len(contract.Name) > 64 || !regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`).MatchString(contract.Name) {
				t.Errorf("name = %q, want at most 64 characters in hyphen-case", contract.Name)
			}
			if len(contract.Description) > 1024 || strings.ContainsAny(contract.Description, "<>") {
				t.Errorf("description must be at most 1024 characters without angle brackets: %q", contract.Description)
			}
			if !strings.Contains(contract.Description, "Use when") {
				t.Errorf("description must include concrete 'Use when' triggers: %q", contract.Description)
			}
			if !strings.Contains(contract.Description, "Do not use") {
				t.Errorf("description must include a concrete 'Do not use' boundary: %q", contract.Description)
			}
			if matched, _ := regexp.MatchString(`^\d+\.\d+\.\d+$`, contract.Metadata.Version); !matched {
				t.Errorf("metadata.version = %q, want semantic version", contract.Metadata.Version)
			}
			if contract.Metadata.OpenClaw.Category != "code-intelligence" {
				t.Errorf("metadata.openclaw.category = %q", contract.Metadata.OpenClaw.Category)
			}
			if contract.Metadata.OpenClaw.Domain != expected.domain {
				t.Errorf("metadata.openclaw.domain = %q, want %q", contract.Metadata.OpenClaw.Domain, expected.domain)
			}
			if !slices.Contains(contract.Metadata.Requires.Bins, "ccg") {
				t.Errorf("metadata.requires.bins = %v, want ccg", contract.Metadata.Requires.Bins)
			}
			if expected.requiresCore && !slices.Contains(contract.Metadata.Requires.Skills, "ccg") {
				t.Errorf("metadata.requires.skills = %v, want ccg", contract.Metadata.Requires.Skills)
			}
			for _, dependency := range contract.Metadata.Requires.Skills {
				if _, err := os.Stat(filepath.Join("..", "..", "skills", dependency, "SKILL.md")); err != nil {
					t.Errorf("metadata.requires.skills dependency %q: %v", dependency, err)
				}
			}
			if contract.Metadata.CLIHelp != expected.cliHelp {
				t.Errorf("metadata.cliHelp = %q, want %q", contract.Metadata.CLIHelp, expected.cliHelp)
			}
			assertSkillReferenceLinks(t, skillDir, string(raw))
		})
	}
}

func TestProjectSkillsDoNotAdvertiseRemovedCommands(t *testing.T) {
	for _, command := range []string{"ccg index", "ccg languages", "ccg example", "ccg tags"} {
		matches, err := filepath.Glob(filepath.Join("..", "..", "skills", "*", "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range matches {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), command) {
				t.Errorf("%s advertises removed command %q", path, command)
			}
		}
	}
}

func TestProjectSkillsRouteEveryRegisteredMCPTool(t *testing.T) {
	mcpToolPattern := regexp.MustCompile(`mcp\.NewTool\("([^"]+)"`)
	mcpSources, err := filepath.Glob(filepath.Join("..", "mcp", "tools_*.go"))
	if err != nil {
		t.Fatal(err)
	}
	registered := make(map[string]struct{})
	for _, path := range mcpSources {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range mcpToolPattern.FindAllStringSubmatch(string(raw), -1) {
			registered[match[1]] = struct{}{}
		}
	}
	if len(registered) == 0 {
		t.Fatal("no registered MCP tools found")
	}

	var skillText strings.Builder
	skillPaths, err := filepath.Glob(filepath.Join("..", "..", "skills", "*", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range skillPaths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		skillText.Write(raw)
		skillText.WriteByte('\n')
	}

	allSkillText := skillText.String()
	for tool := range registered {
		if !strings.Contains(allSkillText, "`"+tool+"`") {
			t.Errorf("registered MCP tool %q is not routed by any project skill", tool)
		}
	}
}

func TestProjectSkillsAvoidKnownMisleadingContracts(t *testing.T) {
	forbidden := []string{
		"semantic search",
		"it broke at an interface",
		"do not overwrite existing annotations",
		"requires `ccg build .` first",
		"refresh flows, communities, or fts",
	}
	paths, err := filepath.Glob(filepath.Join("..", "..", "skills", "*", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		lower := strings.ToLower(string(raw))
		for _, claim := range forbidden {
			if strings.Contains(lower, claim) {
				t.Errorf("%s contains misleading or contradictory contract %q", path, claim)
			}
		}
	}
}

func parseSkillContract(t *testing.T, raw []byte) skillContract {
	t.Helper()
	text := string(raw)
	if !strings.HasPrefix(text, "---\n") {
		t.Fatal("SKILL.md must start with YAML frontmatter")
	}
	end := strings.Index(text[4:], "\n---\n")
	if end < 0 {
		t.Fatal("SKILL.md frontmatter is not closed")
	}
	frontmatter := []byte(text[4 : 4+end])
	var fields map[string]any
	if err := yaml.Unmarshal(frontmatter, &fields); err != nil {
		t.Fatalf("parse frontmatter fields: %v", err)
	}
	allowed := map[string]struct{}{
		"name": {}, "description": {}, "license": {}, "allowed-tools": {}, "metadata": {},
	}
	for key := range fields {
		if _, ok := allowed[key]; !ok {
			t.Errorf("unexpected frontmatter field %q", key)
		}
	}
	var contract skillContract
	if err := yaml.Unmarshal(frontmatter, &contract); err != nil {
		t.Fatalf("parse frontmatter: %v", err)
	}
	return contract
}

func assertSkillReferenceLinks(t *testing.T, skillDir, body string) {
	t.Helper()
	pattern := regexp.MustCompile(`\]\((references/[^)#]+)\)`)
	for _, match := range pattern.FindAllStringSubmatch(body, -1) {
		if _, err := os.Stat(filepath.Join(skillDir, filepath.FromSlash(match[1]))); err != nil {
			t.Errorf("reference %q: %v", match[1], err)
		}
	}
}
