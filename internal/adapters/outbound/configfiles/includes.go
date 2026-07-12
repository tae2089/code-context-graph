package configfiles

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

var errIncludePathsConfig = errors.New("invalid include_paths config")

// IncludePaths reads the repository-local CCG include path configuration.
// @intent adapt established repository config parsing to the reposync application port.
type IncludePaths struct{}

// Load reads include paths from a repository-local .ccg.yaml file.
// @intent own repository configuration file I/O for the reposync adapter while preserving absent-file and parse-error behavior.
func (IncludePaths) Load(repoDir string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(repoDir, ".ccg.yaml"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var cfg struct {
		IncludePaths []string `yaml:"include_paths"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("%w: parse .ccg.yaml: %w", errIncludePathsConfig, err)
	}
	if len(cfg.IncludePaths) == 0 {
		return nil, nil
	}
	return cfg.IncludePaths, nil
}
