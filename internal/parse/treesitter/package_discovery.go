package treesitter

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// PythonPackageDiscovery discovers repo-local Python packages from directory layout.
// @intent model Python import targets as directory-based package nodes so package contains/import edges can be resolved.
type PythonPackageDiscovery struct{}

// TypeScriptPackageDiscovery discovers package nodes for TypeScript source trees.
// @intent map TypeScript source directories to package.json- and tsconfig-based import paths.
type TypeScriptPackageDiscovery struct{}

// JavaScriptPackageDiscovery discovers package nodes for JavaScript source trees.
// @intent map JavaScript source directories to package.json-based import paths.
type JavaScriptPackageDiscovery struct{}

// JavaPackageDiscovery discovers Java packages from package declarations.
// @intent map JVM package declarations to package nodes so Java imports can bind to semantic package targets.
type JavaPackageDiscovery struct{}

// KotlinPackageDiscovery discovers Kotlin packages from package headers.
// @intent map Kotlin package headers to package nodes so imports and package containment use declared package names.
type KotlinPackageDiscovery struct{}

// DiscoverPackages returns Python package metadata keyed by dotted import path.
// @intent group Python source files by containing directory and support both __init__.py packages and implicit namespace packages.
func (PythonPackageDiscovery) DiscoverPackages(ctx context.Context, opts PackageDiscoveryOptions) (map[string]PackageInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.WalkFiles == nil {
		return nil, nil
	}
	packages := make(map[string]PackageInfo)
	ambiguous := make(map[string]struct{})
	err := opts.WalkFiles(func(path, relPath string) error {
		if strings.ToLower(filepath.Ext(path)) != ".py" {
			return nil
		}
		if opts.HasParser != nil && !opts.HasParser(".py") {
			return nil
		}
		dir := filepath.ToSlash(filepath.Dir(relPath))
		if dir == "." {
			return nil
		}
		importPath := pythonDirToImportPath(dir)
		if importPath == "" {
			return nil
		}
		rememberPackage(packages, ambiguous, PackageInfo{
			ImportPath: importPath,
			Name:       pathBaseName(importPath, "."),
			Dir:        dir,
			Language:   "python",
			Files:      []string{filepath.ToSlash(relPath)},
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	for importPath, pkg := range packages {
		slices.Sort(pkg.Files)
		packages[importPath] = pkg
	}
	return packages, nil
}

// DiscoverPackages returns TypeScript package metadata keyed by import path.
// @intent create package nodes for package.json paths and tsconfig alias paths that imports can target.
func (TypeScriptPackageDiscovery) DiscoverPackages(ctx context.Context, opts PackageDiscoveryOptions) (map[string]PackageInfo, error) {
	return discoverNodePackages(ctx, opts, nodePackageDiscoveryConfig{
		language:     "typescript",
		extensions:    []string{".ts", ".tsx"},
		packageJSON:   true,
		tsconfigAlias: true,
	})
}

// DiscoverPackages returns JavaScript package metadata keyed by import path.
// @intent create package nodes for JavaScript directories using package.json-derived import paths.
func (JavaScriptPackageDiscovery) DiscoverPackages(ctx context.Context, opts PackageDiscoveryOptions) (map[string]PackageInfo, error) {
	return discoverNodePackages(ctx, opts, nodePackageDiscoveryConfig{
		language:   "javascript",
		extensions:  []string{".js", ".jsx", ".mjs", ".cjs"},
		packageJSON: true,
	})
}

// DiscoverPackages returns Java package metadata keyed by package declaration.
// @intent group Java source files by declared package so package nodes reflect actual import targets rather than directory guesses.
func (JavaPackageDiscovery) DiscoverPackages(ctx context.Context, opts PackageDiscoveryOptions) (map[string]PackageInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.WalkFiles == nil {
		return nil, nil
	}
	packages := make(map[string]PackageInfo)
	ambiguous := make(map[string]struct{})
	err := opts.WalkFiles(func(path, relPath string) error {
		if strings.ToLower(filepath.Ext(path)) != ".java" {
			return nil
		}
		if opts.HasParser != nil && !opts.HasParser(".java") {
			return nil
		}
		packageName, err := readJavaPackageDeclaration(path)
		if err != nil || packageName == "" {
			return err
		}
		dir := filepath.ToSlash(filepath.Dir(relPath))
		rememberSplitPackage(packages, ambiguous, PackageInfo{
			ImportPath: packageName,
			Name:       pathBaseName(packageName, "."),
			Dir:        dir,
			Language:   "java",
			Files:      []string{filepath.ToSlash(relPath)},
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	for importPath, pkg := range packages {
		slices.Sort(pkg.Files)
		packages[importPath] = pkg
	}
	return packages, nil
}

// DiscoverPackages returns Kotlin package metadata keyed by package header.
// @intent group Kotlin source files by declared package so package nodes reflect Kotlin import targets.
func (KotlinPackageDiscovery) DiscoverPackages(ctx context.Context, opts PackageDiscoveryOptions) (map[string]PackageInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.WalkFiles == nil {
		return nil, nil
	}
	packages := make(map[string]PackageInfo)
	ambiguous := make(map[string]struct{})
	err := opts.WalkFiles(func(path, relPath string) error {
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".kt" && ext != ".kts" {
			return nil
		}
		if opts.HasParser != nil && !opts.HasParser(ext) {
			return nil
		}
		packageName, err := readKotlinPackageHeader(path)
		if err != nil || packageName == "" {
			return err
		}
		dir := filepath.ToSlash(filepath.Dir(relPath))
		rememberSplitPackage(packages, ambiguous, PackageInfo{
			ImportPath: packageName,
			Name:       pathBaseName(packageName, "."),
			Dir:        dir,
			Language:   "kotlin",
			Files:      []string{filepath.ToSlash(relPath)},
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	for importPath, pkg := range packages {
		slices.Sort(pkg.Files)
		packages[importPath] = pkg
	}
	return packages, nil
}

// GoPackageDiscovery discovers repo-local Go packages from go.mod and package clauses.
// @intent model a Go import path as one package node that contains every non-test file in that package.
// @index Go package discovery implementation using go.mod and package statements.
type GoPackageDiscovery struct{}

// DiscoverPackages returns package metadata keyed by import path.
// @intent walk the repository to identify Go packages and their source files.
func (GoPackageDiscovery) DiscoverPackages(ctx context.Context, opts PackageDiscoveryOptions) (map[string]PackageInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	modulePath, err := readGoModulePath(filepath.Join(opts.RootDir, "go.mod"))
	if err != nil || modulePath == "" {
		return nil, err
	}
	if opts.WalkFiles == nil {
		return nil, nil
	}
	packages := make(map[string]PackageInfo)
	ambiguous := make(map[string]struct{})
	err = opts.WalkFiles(func(path, relPath string) error {
		if strings.ToLower(filepath.Ext(path)) != ".go" {
			return nil
		}
		if strings.HasSuffix(relPath, "_test.go") {
			return nil
		}
		if opts.HasParser != nil && !opts.HasParser(".go") {
			return nil
		}
		pkgName, err := readGoPackageClause(path)
		if err != nil || pkgName == "" {
			return err
		}
		if strings.HasSuffix(pkgName, "_test") {
			return nil
		}
		dir := filepath.ToSlash(filepath.Dir(relPath))
		importPath := modulePath
		if dir != "." {
			importPath = modulePath + "/" + dir
		}
		rememberPackage(packages, ambiguous, PackageInfo{
			ImportPath: importPath,
			Name:       pkgName,
			Dir:        dir,
			Language:   "go",
			Files:      []string{relPath},
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	for importPath, pkg := range packages {
		slices.Sort(pkg.Files)
		packages[importPath] = pkg
	}
	return packages, nil
}

// rememberPackage caches a discovered package and merges its files, or marks it as ambiguous on conflict.
// @intent handle multiple declarations of the same import path by merging files or detecting inconsistencies.
func rememberPackage(packages map[string]PackageInfo, ambiguous map[string]struct{}, pkg PackageInfo) {
	if _, blocked := ambiguous[pkg.ImportPath]; blocked {
		return
	}
	if existing, ok := packages[pkg.ImportPath]; ok {
		if existing.Name != pkg.Name || existing.Language != pkg.Language || existing.Dir != pkg.Dir {
			delete(packages, pkg.ImportPath)
			ambiguous[pkg.ImportPath] = struct{}{}
			return
		}
		existing.Files = appendUniquePackageFile(existing.Files, pkg.Files...)
		packages[pkg.ImportPath] = existing
		return
	}
	pkg.Files = appendUniquePackageFile(nil, pkg.Files...)
	packages[pkg.ImportPath] = pkg
}

// rememberSplitPackage merges files for one logical package even when they come from multiple source roots.
// @intent support JVM source-set layouts where one package is intentionally spread across main/test directories.
func rememberSplitPackage(packages map[string]PackageInfo, ambiguous map[string]struct{}, pkg PackageInfo) {
	if _, blocked := ambiguous[pkg.ImportPath]; blocked {
		return
	}
	if existing, ok := packages[pkg.ImportPath]; ok {
		if existing.Name != pkg.Name || existing.Language != pkg.Language {
			delete(packages, pkg.ImportPath)
			ambiguous[pkg.ImportPath] = struct{}{}
			return
		}
		existing.Files = appendUniquePackageFile(existing.Files, pkg.Files...)
		existing.Dir = mergeSplitPackageDir(existing.Dir, pkg.Dir, pkg.ImportPath)
		packages[pkg.ImportPath] = existing
		return
	}
	pkg.Files = appendUniquePackageFile(nil, pkg.Files...)
	pkg.Dir = mergeSplitPackageDir("", pkg.Dir, pkg.ImportPath)
	packages[pkg.ImportPath] = pkg
}

// mergeSplitPackageDir returns a stable representative directory for a split package.
// @intent keep package nodes deterministic even when files come from multiple source roots.
func mergeSplitPackageDir(a, b, importPath string) string {
	a = filepath.ToSlash(strings.TrimSpace(a))
	b = filepath.ToSlash(strings.TrimSpace(b))
	suffix := strings.ReplaceAll(strings.TrimSpace(importPath), ".", "/")
	if suffix != "" {
		return suffix
	}
	if a == "" {
		return b
	}
	if b == "" || a == b {
		return a
	}
	if a < b {
		return a
	}
	return b
}

// appendUniquePackageFile adds new file paths to a slice if they are not already present.
// @intent ensure the file list for a package remains unique without duplicates.
func appendUniquePackageFile(values []string, add ...string) []string {
	for _, value := range add {
		seen := false
		for _, existing := range values {
			if existing == value {
				seen = true
				break
			}
		}
		if !seen {
			values = append(values, value)
		}
	}
	return values
}

// pythonDirToImportPath converts a repository-relative directory into a Python dotted import path.
// @intent normalize filesystem package directories into the import-path form used by package nodes.
func pythonDirToImportPath(dir string) string {
	dir = filepath.ToSlash(strings.TrimSpace(dir))
	if dir == "" || dir == "." {
		return ""
	}
	return strings.ReplaceAll(dir, "/", ".")
}

// pathBaseName returns the last segment of a path-like value separated by sep.
// @intent derive the short package name from an import path without introducing language-specific branches elsewhere.
func pathBaseName(value, sep string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parts := strings.Split(value, sep)
	return parts[len(parts)-1]
}

// nodePackageDiscoveryConfig describes one Node.js-family package discovery mode.
// @intent share package.json and tsconfig-based directory discovery across TypeScript and JavaScript.
type nodePackageDiscoveryConfig struct {
	language     string
	extensions    []string
	packageJSON   bool
	tsconfigAlias bool
}

// nodePackageJSON is the subset of package.json used for import-path discovery.
// @intent keep package metadata parsing minimal while deriving package-node qualified names.
type nodePackageJSON struct {
	Name      string          `json:"name"`
	Workspaces json.RawMessage `json:"workspaces"`
}

// nodeTSConfig is the subset of tsconfig.json used for path alias discovery.
// @intent derive additional package-node import paths from compiler aliases.
type nodeTSConfig struct {
	Extends         string `json:"extends"`
	CompilerOptions struct {
		BaseURL string              `json:"baseUrl"`
		Paths   map[string][]string `json:"paths"`
	} `json:"compilerOptions"`
}

// nodePackageScope stores the root directory and base import path for one Node package scope.
// @intent map repository files back to the package node that should own their imports.
type nodePackageScope struct {
	rootDir    string
	importPath string
}

// nodeAliasScope stores one tsconfig alias scope and its import-path prefixes.
// @intent resolve aliased Node imports against the package node scope they belong to.
type nodeAliasScope struct {
	scopeDir     string
	targetPrefix string
	aliasPrefix  string
}

// discoverNodePackages discovers JS/TS package nodes from directory layout, package.json, and optional tsconfig aliases.
// @intent unify Node ecosystem package discovery so import edges can bind to package nodes consistently.
func discoverNodePackages(ctx context.Context, opts PackageDiscoveryOptions, cfg nodePackageDiscoveryConfig) (map[string]PackageInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.WalkFiles == nil {
		return nil, nil
	}
	rootManifest, err := readNodePackageManifest(filepath.Join(opts.RootDir, "package.json"))
	if err != nil {
		return nil, err
	}
	packageScopes, err := discoverNodePackageScopes(opts.RootDir, rootManifest)
	if err != nil {
		return nil, err
	}
	aliasScopes, err := discoverTSConfigAliasScopes(opts.RootDir)
	if err != nil {
		return nil, err
	}
	packages := make(map[string]PackageInfo)
	ambiguous := make(map[string]struct{})
	err = opts.WalkFiles(func(path, relPath string) error {
		ext := strings.ToLower(filepath.Ext(path))
		if !containsString(cfg.extensions, ext) {
			return nil
		}
		if opts.HasParser != nil && !opts.HasParser(ext) {
			return nil
		}
		dir := filepath.ToSlash(filepath.Dir(relPath))
		if dir == "." {
			return nil
		}
		relPath = filepath.ToSlash(relPath)
		scope := bestNodePackageScope(relPath, packageScopes)
		packageRelPath := relPath
		baseImportPath := rootManifest.Name
		if scope.importPath != "" {
			baseImportPath = scope.importPath
		}
		if scope.rootDir != "" && pathMatchesPrefix(relPath, scope.rootDir) {
			packageRelPath = strings.TrimPrefix(relPath, strings.Trim(scope.rootDir, "/")+"/")
		}
		for _, importPath := range nodeImportPathsForPath(relPath, packageRelPath, baseImportPath, aliasScopes, cfg.tsconfigAlias) {
			rememberPackage(packages, ambiguous, PackageInfo{
				ImportPath: importPath,
				Name:       pathBaseName(dir, "/"),
				Dir:        dir,
				Language:   cfg.language,
				Files:      []string{filepath.ToSlash(relPath)},
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for importPath, pkg := range packages {
		slices.Sort(pkg.Files)
		packages[importPath] = pkg
	}
	return packages, nil
}

// readNodePackageManifest extracts the package.json name and workspace globs when present.
// @intent use repository and workspace manifest metadata to build Node-family import paths.
func readNodePackageManifest(path string) (nodePackageJSON, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nodePackageJSON{}, nil
		}
		return nodePackageJSON{}, err
	}
	var pkg nodePackageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nodePackageJSON{}, err
	}
	pkg.Name = strings.TrimSpace(pkg.Name)
	return pkg, nil
}

// readTSConfigAliasPrefixes parses tsconfig path aliases into path-prefix mappings.
// @intent derive alternate package-node import paths for aliased TypeScript imports.
func readTSConfigAliasPrefixes(rootDir, path string) (map[string]string, error) {
	return readTSConfigAliasPrefixesSeen(rootDir, path, map[string]struct{}{})
}

// readTSConfigAliasPrefixesSeen resolves tsconfig path aliases while guarding against extends cycles.
// @intent merge inherited alias prefixes from nested tsconfig chains into one import-path map.
func readTSConfigAliasPrefixesSeen(rootDir, path string, seen map[string]struct{}) (map[string]string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if _, ok := seen[absPath]; ok {
		return nil, nil
	}
	seen[absPath] = struct{}{}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	data = stripJSONComments(data)
	var cfg nodeTSConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	aliases := make(map[string]string)
	if cfg.Extends != "" {
		parentPath := resolveTSConfigExtends(path, cfg.Extends)
		if parentPath != "" {
			parentAliases, err := readTSConfigAliasPrefixesSeen(rootDir, parentPath, seen)
			if err != nil {
				return nil, err
			}
			for k, v := range parentAliases {
				aliases[k] = v
			}
		}
	}
	configDir := filepath.Dir(path)
	configRelDir, err := filepath.Rel(rootDir, configDir)
	if err != nil {
		return nil, err
	}
	configRelDir = filepath.ToSlash(configRelDir)
	if configRelDir == "." {
		configRelDir = ""
	}
	baseURL := trimNodeWildcard(cfg.CompilerOptions.BaseURL)
	if baseURL == "." {
		baseURL = ""
	}
	basePrefix := joinNodeImportPath(configRelDir, baseURL)
	for alias, targets := range cfg.CompilerOptions.Paths {
		aliasPrefix := trimNodeWildcard(alias)
		if aliasPrefix == "" || len(targets) == 0 {
			continue
		}
		targetPrefix := trimNodeWildcard(targets[0])
		if basePrefix != "" {
			targetPrefix = joinNodeImportPath(basePrefix, targetPrefix)
		}
		if targetPrefix == "" {
			continue
		}
		aliases[filepath.ToSlash(targetPrefix)] = aliasPrefix
	}
	if len(aliases) == 0 {
		return nil, nil
	}
	return aliases, nil
}

// nodeImportPathsForPath returns all import paths that should refer to one JS/TS source path.
// @intent register both directory package nodes and file-level alias nodes for Node ecosystem imports.
func nodeImportPathsForPath(relPath, packageRelPath, baseImportPath string, aliasScopes []nodeAliasScope, includeAliases bool) []string {
	relPath = strings.Trim(filepath.ToSlash(relPath), "/")
	packageRelPath = strings.Trim(filepath.ToSlash(packageRelPath), "/")
	dir := filepath.ToSlash(filepath.Dir(packageRelPath))
	rootDir := filepath.ToSlash(filepath.Dir(relPath))
	var paths []string
	if baseImportPath != "" {
		paths = append(paths, joinNodeImportPath(baseImportPath, dir))
	}
	if includeAliases {
		for _, scope := range aliasScopes {
			if scope.scopeDir != "" && !pathMatchesPrefix(relPath, scope.scopeDir) {
				continue
			}
			targetPrefix := scope.targetPrefix
			aliasPrefix := scope.aliasPrefix
			if dirMatchesPrefix(rootDir, targetPrefix) {
				dirRemainder := strings.TrimPrefix(rootDir, targetPrefix)
				dirRemainder = strings.TrimPrefix(dirRemainder, "/")
				paths = append(paths, joinNodeImportPath(aliasPrefix, dirRemainder))
			}
			if !pathMatchesPrefix(relPath, targetPrefix) {
				continue
			}
			remainder := strings.TrimPrefix(relPath, targetPrefix)
			remainder = strings.TrimPrefix(remainder, "/")
			remainder = strings.TrimSuffix(remainder, filepath.Ext(remainder))
			if remainder != "" {
				paths = append(paths, joinNodeImportPath(aliasPrefix, remainder))
			}
			aliasDir := filepath.ToSlash(filepath.Dir(remainder))
			if aliasDir != "." && aliasDir != "" {
				paths = append(paths, joinNodeImportPath(aliasPrefix, aliasDir))
			}
		}
	}
	return appendUniquePackageFile(nil, paths...)
}

// discoverNodePackageScopes collects root and workspace package scopes used for JS/TS import path generation.
// @intent pick the nearest package.json name for monorepo files instead of assuming the repository root package applies everywhere.
func discoverNodePackageScopes(rootDir string, rootManifest nodePackageJSON) ([]nodePackageScope, error) {
	scopes := []nodePackageScope{{importPath: rootManifest.Name}}
	workspaceGlobs := appendUniquePackageFile(parseNodeWorkspaces(rootManifest.Workspaces), readPNPMWorkspacePatterns(filepath.Join(rootDir, "pnpm-workspace.yaml"))...)
	for _, relDir := range workspacePackageRoots(rootDir, workspaceGlobs) {
		manifest, err := readNodePackageManifest(filepath.Join(rootDir, relDir, "package.json"))
		if err != nil {
			return nil, err
		}
		if manifest.Name == "" {
			continue
		}
		scopes = append(scopes, nodePackageScope{rootDir: filepath.ToSlash(relDir), importPath: manifest.Name})
	}
	slices.SortFunc(scopes, func(a, b nodePackageScope) int {
		return len(strings.Trim(b.rootDir, "/")) - len(strings.Trim(a.rootDir, "/"))
	})
	return scopes, nil
}

// discoverTSConfigAliasScopes collects alias mappings from every tsconfig.json under the repository.
// @intent let nested packages contribute their own alias prefixes for monorepo-local imports.
func discoverTSConfigAliasScopes(rootDir string) ([]nodeAliasScope, error) {
	var scopes []nodeAliasScope
	err := filepath.WalkDir(rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == "node_modules" || name == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "tsconfig.json" {
			return nil
		}
		aliases, err := readTSConfigAliasPrefixes(rootDir, path)
		if err != nil {
			return err
		}
		configDir, err := filepath.Rel(rootDir, filepath.Dir(path))
		if err != nil {
			return err
		}
		configDir = filepath.ToSlash(configDir)
		if configDir == "." {
			configDir = ""
		}
		for targetPrefix, aliasPrefix := range aliases {
			scopes = append(scopes, nodeAliasScope{
				scopeDir:     configDir,
				targetPrefix: targetPrefix,
				aliasPrefix:  aliasPrefix,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(scopes, func(a, b nodeAliasScope) int {
		return len(strings.Trim(b.scopeDir, "/")) - len(strings.Trim(a.scopeDir, "/"))
	})
	return scopes, nil
}

// bestNodePackageScope returns the nearest package scope containing relPath.
// @intent prefer workspace package names over the root package when files live under nested package roots.
func bestNodePackageScope(relPath string, scopes []nodePackageScope) nodePackageScope {
	relPath = strings.Trim(filepath.ToSlash(relPath), "/")
	for _, scope := range scopes {
		if scope.rootDir == "" || pathMatchesPrefix(relPath, scope.rootDir) {
			return scope
		}
	}
	return nodePackageScope{}
}

// parseNodeWorkspaces extracts workspace glob patterns from package.json workspaces shapes.
// @intent support both array and object forms used by npm/Yarn/Bun workspace configs.
func parseNodeWorkspaces(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var direct []string
	if err := json.Unmarshal(raw, &direct); err == nil {
		return appendUniquePackageFile(nil, direct...)
	}
	var wrapped struct {
		Packages []string `json:"packages"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil {
		return appendUniquePackageFile(nil, wrapped.Packages...)
	}
	return nil
}

// readPNPMWorkspacePatterns parses pnpm-workspace.yaml package globs.
// @intent include pnpm-managed workspace package roots in Node-family package discovery.
func readPNPMWorkspacePatterns(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var patterns []string
	inPackages := false
	for _, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line == "packages:" {
			inPackages = true
			continue
		}
		if !inPackages {
			continue
		}
		if !strings.HasPrefix(line, "-") {
			if strings.HasSuffix(line, ":") {
				break
			}
			continue
		}
		pattern := strings.TrimSpace(strings.TrimPrefix(line, "-"))
		pattern = strings.Trim(pattern, "\"'")
		if pattern != "" {
			patterns = append(patterns, pattern)
		}
	}
	return appendUniquePackageFile(nil, patterns...)
}

// workspacePackageRoots resolves workspace glob patterns to package roots that contain a package.json.
// @intent map workspace manifests to concrete package directories without parsing unrelated nested packages.
func workspacePackageRoots(rootDir string, globs []string) []string {
	includePatterns, excludePatterns := splitWorkspacePatterns(globs)
	if len(includePatterns) == 0 {
		return nil
	}
	var roots []string
	err := filepath.WalkDir(rootDir, func(current string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		name := d.Name()
		if name == ".git" || name == "node_modules" {
			return filepath.SkipDir
		}
		if current == rootDir {
			return nil
		}
		if _, err := os.Stat(filepath.Join(current, "package.json")); err != nil {
			return nil
		}
		rel, err := filepath.Rel(rootDir, current)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if !matchesWorkspacePatterns(rel, includePatterns, excludePatterns) {
			return nil
		}
		roots = append(roots, rel)
		return nil
	})
	if err != nil {
		return appendUniquePackageFile(nil, roots...)
	}
	return appendUniquePackageFile(nil, roots...)
}

// splitWorkspacePatterns separates workspace globs into include and exclude lists.
// @intent normalize npm/pnpm workspace pattern lists before matching concrete package roots.
func splitWorkspacePatterns(globs []string) (includes []string, excludes []string) {
	for _, pattern := range globs {
		pattern = strings.TrimSpace(filepath.ToSlash(pattern))
		pattern = strings.Trim(pattern, "/")
		if pattern == "" {
			continue
		}
		if strings.HasPrefix(pattern, "!") {
			exclude := strings.TrimSpace(strings.TrimPrefix(pattern, "!"))
			exclude = strings.Trim(filepath.ToSlash(exclude), "/")
			if exclude != "" {
				excludes = append(excludes, exclude)
			}
			continue
		}
		includes = append(includes, pattern)
	}
	return appendUniquePackageFile(nil, includes...), appendUniquePackageFile(nil, excludes...)
}

// matchesWorkspacePatterns reports whether a relative path is included by workspace rules.
// @intent apply include-first and negate-after semantics consistently across workspace root discovery.
func matchesWorkspacePatterns(rel string, includes []string, excludes []string) bool {
	rel = strings.Trim(filepath.ToSlash(rel), "/")
	matched := false
	for _, pattern := range includes {
		if workspacePatternMatch(pattern, rel) {
			matched = true
			break
		}
	}
	if !matched {
		return false
	}
	for _, pattern := range excludes {
		if workspacePatternMatch(pattern, rel) {
			return false
		}
	}
	return true
}

// workspacePatternMatch matches one normalized workspace glob against a relative path.
// @intent keep workspace package discovery independent from shell-specific glob expansion.
func workspacePatternMatch(pattern, rel string) bool {
	pattern = strings.Trim(filepath.ToSlash(pattern), "/")
	rel = strings.Trim(filepath.ToSlash(rel), "/")
	if pattern == "" {
		return rel == ""
	}
	return workspacePatternMatchParts(strings.Split(pattern, "/"), strings.Split(rel, "/"))
}

// workspacePatternMatchParts recursively matches split workspace path segments.
// @intent implement **-aware workspace glob semantics for package root discovery.
func workspacePatternMatchParts(patternParts []string, relParts []string) bool {
	if len(patternParts) == 0 {
		return len(relParts) == 0
	}
	if patternParts[0] == "**" {
		if workspacePatternMatchParts(patternParts[1:], relParts) {
			return true
		}
		for i := 0; i < len(relParts); i++ {
			if workspacePatternMatchParts(patternParts[1:], relParts[i+1:]) {
				return true
			}
		}
		return false
	}
	if len(relParts) == 0 {
		return false
	}
	matched, err := path.Match(patternParts[0], relParts[0])
	if err != nil || !matched {
		return false
	}
	return workspacePatternMatchParts(patternParts[1:], relParts[1:])
}

// resolveTSConfigExtends resolves a tsconfig extends entry to a local file path when possible.
// @intent let nested tsconfig files inherit baseUrl/paths from local parent configs.
func resolveTSConfigExtends(configPath, extends string) string {
	extends = strings.TrimSpace(extends)
	if extends == "" || (!strings.HasPrefix(extends, ".") && !strings.HasPrefix(extends, "/")) {
		return ""
	}
	if filepath.Ext(extends) == "" {
		extends += ".json"
	}
	if filepath.IsAbs(extends) {
		return extends
	}
	return filepath.Join(filepath.Dir(configPath), extends)
}

// joinNodeImportPath joins an import prefix and a relative directory using slash separators.
// @intent keep package-node qualified names aligned with JS/TS import strings.
func joinNodeImportPath(prefix, relDir string) string {
	prefix = strings.TrimSpace(strings.TrimSuffix(filepath.ToSlash(prefix), "/"))
	relDir = strings.Trim(filepath.ToSlash(relDir), "/")
	if prefix == "" {
		return relDir
	}
	if relDir == "" {
		return prefix
	}
	return prefix + "/" + relDir
}

// trimNodeWildcard removes one trailing /* wildcard suffix from a tsconfig path or alias.
// @intent normalize alias rules before matching them against source directories.
func trimNodeWildcard(value string) string {
	value = filepath.ToSlash(strings.TrimSpace(value))
	value = strings.TrimSuffix(value, "/*")
	value = strings.TrimSuffix(value, "*")
	value = strings.TrimSuffix(value, "/")
	return value
}

// dirMatchesPrefix reports whether dir is equal to or nested under prefix.
// @intent match source directories against tsconfig path targets without partial-segment false positives.
func dirMatchesPrefix(dir, prefix string) bool {
	dir = strings.Trim(filepath.ToSlash(dir), "/")
	prefix = strings.Trim(filepath.ToSlash(prefix), "/")
	if dir == prefix {
		return true
	}
	return strings.HasPrefix(dir, prefix+"/")
}

// pathMatchesPrefix reports whether relPath is equal to or nested under prefix.
// @intent match concrete source file paths against tsconfig alias target roots.
func pathMatchesPrefix(relPath, prefix string) bool {
	relPath = strings.Trim(filepath.ToSlash(relPath), "/")
	prefix = strings.Trim(filepath.ToSlash(prefix), "/")
	if relPath == prefix {
		return true
	}
	return strings.HasPrefix(relPath, prefix+"/")
}

// stripJSONComments removes line and block comments from JSONC input.
// @intent let tsconfig discovery accept common commented config files without introducing a separate parser dependency.
func stripJSONComments(data []byte) []byte {
	var out []byte
	inString := false
	escaped := false
	for i := 0; i < len(data); i++ {
		ch := data[i]
		if inString {
			out = append(out, ch)
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		if ch == '"' {
			inString = true
			out = append(out, ch)
			continue
		}
		if ch == '/' && i+1 < len(data) {
			next := data[i+1]
			if next == '/' {
				for i < len(data) && data[i] != '\n' {
					i++
				}
				if i < len(data) {
					out = append(out, data[i])
				}
				continue
			}
			if next == '*' {
				i += 2
				for i < len(data)-1 {
					if data[i] == '*' && data[i+1] == '/' {
						i++
						break
					}
					i++
				}
				continue
			}
		}
		out = append(out, ch)
	}
	return out
}

// containsString reports whether values contains target.
// @intent keep extension checks simple without importing extra helpers.
func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// readGoModulePath extracts the module name from a go.mod file.
// @intent identify the repository's root import path for Go package normalization.
func readGoModulePath(goModPath string) (string, error) {
	file, err := os.Open(goModPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "module ") {
			continue
		}
		return strings.TrimSpace(strings.TrimPrefix(line, "module ")), nil
	}
	return "", scanner.Err()
}

// readGoPackageClause finds the package name declared at the top of a Go source file.
// @intent determine the local package name to assist in constructing qualified names.
func readGoPackageClause(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if !strings.HasPrefix(line, "package ") {
			return "", nil
		}
		name := strings.TrimSpace(strings.TrimPrefix(line, "package "))
		if fields := strings.Fields(name); len(fields) > 0 {
			return fields[0], nil
		}
		return "", nil
	}
	return "", scanner.Err()
}

// readJavaPackageDeclaration extracts the declared package from a Java source file.
// @intent use the language-declared package as the authoritative import path for Java package nodes.
func readJavaPackageDeclaration(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "/*") || strings.HasPrefix(line, "*") {
			continue
		}
		if !strings.HasPrefix(line, "package ") {
			return "", nil
		}
		name := strings.TrimSpace(strings.TrimPrefix(line, "package "))
		name = strings.TrimSuffix(name, ";")
		return strings.TrimSpace(name), nil
	}
	return "", scanner.Err()
}

// readKotlinPackageHeader extracts the declared package from a Kotlin source file.
// @intent use the language-declared package as the authoritative import path for Kotlin package nodes.
func readKotlinPackageHeader(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "/*") || strings.HasPrefix(line, "*") {
			continue
		}
		if !strings.HasPrefix(line, "package ") {
			return "", nil
		}
		name := strings.TrimSpace(strings.TrimPrefix(line, "package "))
		return strings.TrimSpace(name), nil
	}
	return "", scanner.Err()
}
