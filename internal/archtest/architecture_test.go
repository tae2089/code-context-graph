package archtest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
)

const modulePath = "github.com/tae2089/code-context-graph"

type packageInfo struct {
	ImportPath string
	Imports    []string
	Deps       []string
}

func TestImportsPackageTree(t *testing.T) {
	tests := []struct {
		name     string
		imports  []string
		root     string
		expected bool
	}{
		{name: "root package", imports: []string{"gorm.io/gorm"}, root: "gorm.io/gorm", expected: true},
		{name: "subpackage", imports: []string{"gorm.io/gorm/clause"}, root: "gorm.io/gorm", expected: true},
		{name: "similar prefix", imports: []string{"gorm.io/gormish"}, root: "gorm.io/gorm", expected: false},
		{name: "unrelated", imports: []string{"example.com/gorm"}, root: "gorm.io/gorm", expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := importsPackageTree(tt.imports, tt.root); got != tt.expected {
				t.Fatalf("importsPackageTree(%v, %q) = %v, want %v", tt.imports, tt.root, got, tt.expected)
			}
		})
	}
}

func TestLegacyGORMImportersMatchBaseline(t *testing.T) {
	packages := loadPackages(t)
	allowedAdapters := map[string]bool{
		modulePath + "/internal/db":                          true,
		modulePath + "/internal/db/migration":                true,
		modulePath + "/internal/adapters/outbound/graphgorm": true,
		modulePath + "/internal/adapters/outbound/searchsql": true,
		modulePath + "/internal/runtime":                     true,
	}

	var actual []string
	for _, pkg := range packages {
		if allowedAdapters[pkg.ImportPath] || !importsPackageTree(pkg.Imports, "gorm.io/gorm") {
			continue
		}
		actual = append(actual, strings.TrimPrefix(pkg.ImportPath, modulePath+"/"))
	}
	sort.Strings(actual)

	expected := loadBaseline(t, "testdata/legacy_gorm_importers.txt")
	if !slices.Equal(actual, expected) {
		t.Fatalf("legacy GORM importers changed\nactual:   %v\nexpected: %v\nadd new violations only with an explicit refactor decision; remove stale exceptions immediately", actual, expected)
	}
}

func importsPackageTree(imports []string, root string) bool {
	for _, imported := range imports {
		if imported == root || strings.HasPrefix(imported, root+"/") {
			return true
		}
	}
	return false
}

func TestLocalCLIDoesNotLinkRemoteHostPackages(t *testing.T) {
	packages := loadPackages(t)
	localCLI, ok := packages[modulePath+"/cmd/ccg"]
	if !ok {
		t.Fatal("cmd/ccg package missing from go list output")
	}

	forbidden := []string{
		modulePath + "/internal/adapters/inbound/http",
		modulePath + "/internal/adapters/inbound/webhook",
		modulePath + "/internal/adapters/inbound/wikihttp",
	}
	for _, dependency := range forbidden {
		if slices.Contains(localCLI.Deps, dependency) {
			t.Errorf("cmd/ccg production dependency closure contains remote host package %s", dependency)
		}
	}
}

func TestDomainCandidatesDoNotImportOuterPackages(t *testing.T) {
	packages := loadPackages(t)
	domainPackages := map[string]bool{
		modulePath + "/internal/domain/graph":      true,
		modulePath + "/internal/domain/annotation": true,
		modulePath + "/internal/domain/reference":  true,
	}

	for packagePath := range domainPackages {
		pkg, ok := packages[packagePath]
		if !ok {
			t.Errorf("domain package %s missing from go list output", packagePath)
			continue
		}
		for _, imported := range pkg.Imports {
			if strings.HasPrefix(imported, modulePath+"/internal/") && !domainPackages[imported] {
				t.Errorf("domain package %s imports outer package %s", packagePath, imported)
			}
		}
	}
}

func TestApplicationPackagesDoNotImportInfrastructureOrTransports(t *testing.T) {
	packages := loadPackages(t)
	appPrefix := modulePath + "/internal/app/"
	forbiddenDirectRoots := []string{
		"github.com/mark3labs/mcp-go",
		"github.com/go-git/go-git",
		"go.opentelemetry.io/otel",
		"github.com/smacker/go-tree-sitter",
		"github.com/spf13/cobra",
		"database/sql",
		"gorm.io/gorm",
		"net/http",
	}
	forbiddenInternalRoots := []string{
		modulePath + "/internal/adapters",
		modulePath + "/internal/cli",
		modulePath + "/internal/core",
		modulePath + "/internal/db",
		modulePath + "/internal/mcp",
		modulePath + "/internal/mcpruntime",
		modulePath + "/internal/parse/treesitter",
		modulePath + "/internal/runtime",
		modulePath + "/internal/server",
		modulePath + "/internal/service",
		modulePath + "/internal/store",
		modulePath + "/internal/webhook",
		modulePath + "/internal/wikiserver",
		modulePath + "/internal/webhook",
	}

	foundApplicationPackage := false
	for packagePath, pkg := range packages {
		if !strings.HasPrefix(packagePath, appPrefix) {
			continue
		}
		foundApplicationPackage = true
		for _, forbidden := range forbiddenDirectRoots {
			if importsPackageTree(pkg.Imports, forbidden) {
				t.Errorf("application package %s directly imports forbidden infrastructure or transport package %s", packagePath, forbidden)
			}
		}
		for _, forbidden := range forbiddenInternalRoots {
			if importsPackageTree(pkg.Deps, forbidden) {
				t.Errorf("application package %s depends on forbidden infrastructure or transport package %s", packagePath, forbidden)
			}
		}
	}
	if !foundApplicationPackage {
		t.Fatal("no internal/app package found; AR-02 ingest ports are missing")
	}
}

func TestInboundAdaptersDoNotImportPersistenceImplementations(t *testing.T) {
	packages := loadPackages(t)
	inboundPrefix := modulePath + "/internal/adapters/inbound/"
	forbidden := []string{
		"gorm.io/gorm",
		modulePath + "/internal/adapters/outbound/graphgorm",
		modulePath + "/internal/adapters/outbound/searchsql",
	}

	found := false
	for packagePath, pkg := range packages {
		if !strings.HasPrefix(packagePath, inboundPrefix) {
			continue
		}
		found = true
		for _, dependency := range forbidden {
			if importsPackageTree(pkg.Imports, dependency) {
				t.Errorf("inbound adapter %s directly imports persistence implementation %s", packagePath, dependency)
			}
		}
	}
	if !found {
		t.Fatal("no inbound adapter packages found")
	}
}

func TestGlobalPortsPackageDoesNotExist(t *testing.T) {
	packages := loadPackages(t)
	for _, packagePath := range []string{modulePath + "/internal/ports", modulePath + "/internal/store"} {
		if _, exists := packages[packagePath]; exists {
			t.Errorf("global contract package is forbidden; ports belong beside their application consumer: %s", packagePath)
		}
	}
}

func TestContextPackageReplacesCtxns(t *testing.T) {
	packages := loadPackages(t)
	legacy := modulePath + "/internal/ctxns"
	if _, exists := packages[legacy]; exists {
		t.Errorf("legacy context helper package must not remain: %s", legacy)
	}
	current := modulePath + "/internal/ctx"
	if _, exists := packages[current]; !exists {
		t.Errorf("reusable request context package is missing: %s", current)
	}
}

func TestPagingPackageDoesNotExist(t *testing.T) {
	packages := loadPackages(t)
	legacy := modulePath + "/internal/paging"
	if _, exists := packages[legacy]; exists {
		t.Errorf("generic paging package must not remain: %s", legacy)
	}
}

func TestMigrationModuleOwnsEmbeddedAssets(t *testing.T) {
	packages := loadPackages(t)
	if _, exists := packages[modulePath+"/internal/migrationfs"]; exists {
		t.Error("internal/migrationfs is a shallow package; embedded migration assets belong to internal/db/migration")
	}

	root := repositoryRoot(t)
	for _, driver := range []string{"sqlite", "postgres"} {
		assetDir := filepath.Join(root, "internal", "db", "migration", driver)
		info, err := os.Stat(assetDir)
		if err != nil {
			t.Errorf("stat embedded %s migration assets: %v", driver, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("embedded %s migration assets path is not a directory: %s", driver, assetDir)
		}
	}
}

func TestSearchCapabilityOwnsIdentifierTokenization(t *testing.T) {
	packages := loadPackages(t)
	legacy := modulePath + "/internal/identtoken"
	if _, exists := packages[legacy]; exists {
		t.Errorf("search-only identifier tokenization must not remain a top-level primitive: %s", legacy)
	}
	current := modulePath + "/internal/app/search/identtoken"
	if _, exists := packages[current]; !exists {
		t.Errorf("search identifier tokenization package is missing: %s", current)
	}
}

func TestPathspecOwnsPurePathSpecificationMatching(t *testing.T) {
	packages := loadPackages(t)
	legacy := modulePath + "/internal/pathutil"
	if _, exists := packages[legacy]; exists {
		t.Errorf("generic path utility package must not remain: %s", legacy)
	}
	pathspecPath := modulePath + "/internal/pathspec"
	pkg, exists := packages[pathspecPath]
	if !exists {
		t.Fatalf("path specification package is missing: %s", pathspecPath)
	}
	for _, forbidden := range []string{"os", "go.yaml.in/yaml/v3"} {
		if importsPackageTree(pkg.Imports, forbidden) {
			t.Errorf("pathspec must remain pure lexical matching; configuration I/O belongs to configfiles: %s", forbidden)
		}
	}
}

func TestRuntimeIsTheOnlyCompositionBoundary(t *testing.T) {
	packages := loadPackages(t)
	runtimePath := modulePath + "/internal/runtime"
	if _, exists := packages[runtimePath]; !exists {
		t.Error("internal/runtime composition package is missing")
	}
	for _, legacy := range []string{modulePath + "/internal/core", modulePath + "/internal/mcpruntime"} {
		if _, exists := packages[legacy]; exists {
			t.Errorf("legacy runtime package still exists: %s", legacy)
		}
	}
	for packagePath, pkg := range packages {
		isAdapter := strings.HasPrefix(packagePath, modulePath+"/internal/adapters/")
		isApplication := strings.HasPrefix(packagePath, modulePath+"/internal/app/")
		if !isAdapter && !isApplication {
			continue
		}
		for _, imported := range pkg.Imports {
			if imported == runtimePath || strings.HasPrefix(imported, runtimePath+"/") {
				t.Errorf("package %s imports runtime composition package %s", packagePath, imported)
			}
		}
	}
}

func TestLegacyIngestPackagesDoNotExist(t *testing.T) {
	packages := loadPackages(t)
	legacyPackages := []string{
		modulePath + "/internal/parse",
		modulePath + "/internal/parse/treesitter",
		modulePath + "/internal/edgeresolve",
		modulePath + "/internal/analysis/incremental",
		modulePath + "/internal/store/gormstore",
	}
	for _, packagePath := range legacyPackages {
		if _, exists := packages[packagePath]; exists {
			t.Errorf("legacy ingest package still exists: %s", packagePath)
		}
	}
}

func TestLegacyPackagesDoNotExist(t *testing.T) {
	packages := loadPackages(t)
	legacyPackages := []string{
		modulePath + "/internal/analysis/changes",
		modulePath + "/internal/analysis/flows",
		modulePath + "/internal/analysis/impact",
		modulePath + "/internal/analysis/query",
		modulePath + "/internal/retrieval",
		modulePath + "/internal/searchrank",
		modulePath + "/internal/service",
		modulePath + "/internal/store/search",
		modulePath + "/internal/ragindex",
		modulePath + "/internal/wikiindex",
		modulePath + "/internal/docs",
		modulePath + "/internal/cli",
		modulePath + "/internal/mcp",
		modulePath + "/internal/server",
		modulePath + "/internal/webhook",
		modulePath + "/internal/wikiserver",
	}
	for _, packagePath := range legacyPackages {
		if _, exists := packages[packagePath]; exists {
			t.Errorf("legacy analysis/search package still exists: %s", packagePath)
		}
	}
}

func TestWebhookPackageOwnsOnlyInboundHTTPProductionSource(t *testing.T) {
	dir := filepath.Join(repositoryRoot(t), "internal", "adapters", "inbound", "webhook")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read webhook directory: %v", err)
	}
	var production []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), "_test.go") {
			production = append(production, entry.Name())
		}
	}
	sort.Strings(production)
	if !slices.Equal(production, []string{"handler.go"}) {
		t.Fatalf("webhook production files=%v, want only handler.go", production)
	}
}

func TestRepoSyncApplicationDoesNotDependOnObservabilityImplementation(t *testing.T) {
	packages := loadPackages(t)
	pkg, ok := packages[modulePath+"/internal/app/reposync"]
	if !ok {
		t.Fatal("app/reposync package missing")
	}
	for _, forbidden := range []string{modulePath + "/internal/obs", "go.opentelemetry.io/otel"} {
		if importsPackageTree(pkg.Deps, forbidden) {
			t.Errorf("app/reposync depends on observability implementation %s", forbidden)
		}
	}
}

func loadPackages(t *testing.T) map[string]packageInfo {
	t.Helper()
	root := repositoryRoot(t)
	cmd := exec.Command("go", "list", "-json", "./...")
	cmd.Dir = root
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go list -json ./...: %v\n%s", err, stderr.String())
	}

	packages := make(map[string]packageInfo)
	decoder := json.NewDecoder(&stdout)
	for decoder.More() {
		var pkg packageInfo
		if err := decoder.Decode(&pkg); err != nil {
			t.Fatalf("decode go list output: %v", err)
		}
		packages[pkg.ImportPath] = pkg
	}
	if len(packages) == 0 {
		t.Fatal("go list returned no packages")
	}
	return packages
}

func loadBaseline(t *testing.T, relativePath string) []string {
	t.Helper()
	path := filepath.Join(repositoryRoot(t), "internal", "archtest", relativePath)
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open architecture baseline %s: %v", relativePath, err)
	}
	defer file.Close()

	var entries []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		entry := strings.TrimSpace(scanner.Text())
		if entry == "" || strings.HasPrefix(entry, "#") {
			continue
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read architecture baseline %s: %v", relativePath, err)
	}
	sort.Strings(entries)
	return entries
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve architecture test file path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
}
