package treesitter

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
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
	Name string `json:"name"`
}

// nodeTSConfig is the subset of tsconfig.json used for path alias discovery.
// @intent derive additional package-node import paths from compiler aliases.
type nodeTSConfig struct {
	CompilerOptions struct {
		BaseURL string              `json:"baseUrl"`
		Paths   map[string][]string `json:"paths"`
	} `json:"compilerOptions"`
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
	baseImportPath, err := readNodePackageName(filepath.Join(opts.RootDir, "package.json"))
	if err != nil {
		return nil, err
	}
	aliasMap, err := readTSConfigAliasPrefixes(filepath.Join(opts.RootDir, "tsconfig.json"))
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
		for _, importPath := range nodeImportPathsForPath(filepath.ToSlash(relPath), baseImportPath, aliasMap, cfg.tsconfigAlias) {
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

// readNodePackageName extracts the package.json name field when present.
// @intent use the repository package name as the root import prefix for JS/TS package nodes.
func readNodePackageName(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	var pkg nodePackageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return "", err
	}
	return strings.TrimSpace(pkg.Name), nil
}

// readTSConfigAliasPrefixes parses tsconfig path aliases into path-prefix mappings.
// @intent derive alternate package-node import paths for aliased TypeScript imports.
func readTSConfigAliasPrefixes(path string) (map[string]string, error) {
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
	baseURL := trimNodeWildcard(cfg.CompilerOptions.BaseURL)
	if baseURL == "." {
		baseURL = ""
	}
	for alias, targets := range cfg.CompilerOptions.Paths {
		aliasPrefix := trimNodeWildcard(alias)
		if aliasPrefix == "" || len(targets) == 0 {
			continue
		}
		targetPrefix := trimNodeWildcard(targets[0])
		if baseURL != "" {
			targetPrefix = joinNodeImportPath(baseURL, targetPrefix)
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
func nodeImportPathsForPath(relPath, baseImportPath string, aliasPrefixes map[string]string, includeAliases bool) []string {
	relPath = strings.Trim(filepath.ToSlash(relPath), "/")
	dir := filepath.ToSlash(filepath.Dir(relPath))
	var paths []string
	if baseImportPath != "" {
		paths = append(paths, joinNodeImportPath(baseImportPath, dir))
	}
	if includeAliases {
		for targetPrefix, aliasPrefix := range aliasPrefixes {
			if dirMatchesPrefix(dir, targetPrefix) {
				dirRemainder := strings.TrimPrefix(dir, targetPrefix)
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
