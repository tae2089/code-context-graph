package treesitter

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// GoPackageDiscovery discovers repo-local Go packages from go.mod and package clauses.
// @intent model a Go import path as one package node that contains every non-test file in that package.
type GoPackageDiscovery struct{}

// DiscoverPackages returns package metadata keyed by import path.
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
