package configfiles

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"

	"github.com/tae2089/code-context-graph/internal/app/reposync"
)

var errBuildScopeConfig = errors.New("invalid repository build scope config")

// BuildScope reads repository-local CCG source selection configuration.
// @intent adapt repository include and exclude configuration parsing to the reposync application port.
type BuildScope struct{}

// Load reads include paths and exclude patterns from a repository-local .ccg.yaml file.
// @intent own repository build scope configuration I/O for webhook synchronization.
// @return returns an empty scope when the repository has no .ccg.yaml file.
func (BuildScope) Load(repoDir string) (reposync.BuildScope, error) {
	data, err := os.ReadFile(filepath.Join(repoDir, ".ccg.yaml"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return reposync.BuildScope{}, nil
		}
		return reposync.BuildScope{}, err
	}
	var cfg struct {
		IncludePaths    []string `yaml:"include_paths"`
		ExcludePatterns []string `yaml:"exclude"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return reposync.BuildScope{}, fmt.Errorf("%w: parse .ccg.yaml: %w", errBuildScopeConfig, err)
	}
	return reposync.BuildScope{IncludePaths: cfg.IncludePaths, ExcludePatterns: cfg.ExcludePatterns}, nil
}
