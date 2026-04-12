package changes

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type ExecGitClient struct{}

func NewExecGitClient() *ExecGitClient {
	return &ExecGitClient{}
}

func (g *ExecGitClient) ChangedFiles(ctx context.Context, repoDir, baseRef string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "diff", "--name-only", baseRef)
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff --name-only: %w", err)
	}

	var files []string
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

func (g *ExecGitClient) DiffHunks(ctx context.Context, repoDir, baseRef string, paths []string) ([]Hunk, error) {
	args := []string{"diff", "-U0", baseRef, "--"}
	args = append(args, paths...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff -U0: %w", err)
	}

	var hunks []Hunk
	var currentFile string
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "+++ b/") {
			currentFile = strings.TrimPrefix(line, "+++ b/")
		} else if strings.HasPrefix(line, "@@ ") {
			start, count := parseHunkHeader(line)
			if start > 0 && currentFile != "" {
				end := start + count - 1
				if end < start {
					end = start
				}
				hunks = append(hunks, Hunk{
					FilePath:  currentFile,
					StartLine: start,
					EndLine:   end,
				})
			}
		}
	}
	return hunks, nil
}

func parseHunkHeader(line string) (start, count int) {
	// @@ -oldStart,oldCount +newStart,newCount @@
	plusIdx := strings.Index(line, "+")
	if plusIdx < 0 {
		return 0, 0
	}
	rest := line[plusIdx+1:]
	spaceIdx := strings.Index(rest, " ")
	if spaceIdx > 0 {
		rest = rest[:spaceIdx]
	}
	parts := strings.SplitN(rest, ",", 2)
	s, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0
	}
	c := 1
	if len(parts) == 2 {
		c, err = strconv.Atoi(parts[1])
		if err != nil {
			return s, 1
		}
	}
	return s, c
}

var _ GitClient = (*ExecGitClient)(nil)
