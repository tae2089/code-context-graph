package changes

import (
	"bufio"
	"context"
	"os/exec"
	"strconv"
	"strings"

	"github.com/tae2089/trace"
)

// ExecGitClient shells out to git for change detection.
// @intent provide GitClient behavior using the local git executable
type ExecGitClient struct{}

// NewExecGitClient creates an exec-backed git client.
// @intent construct a GitClient that reads diffs from the local repository
func NewExecGitClient() *ExecGitClient {
	return &ExecGitClient{}
}

// ChangedFiles lists files changed from the given base ref.
// @intent identify which repository paths changed since a base revision
// @param repoDir repository root where git commands are executed
// @param baseRef git revision used as the diff baseline
// @return changed file paths reported by git diff --name-only
// @sideEffect executes git diff in the target repository
// @requires repoDir points to a valid git working tree
// @ensures returned file paths are trimmed and exclude blank lines
func (g *ExecGitClient) ChangedFiles(ctx context.Context, repoDir, baseRef string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "diff", "--name-only", baseRef)
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return nil, trace.Wrap(err, "git diff --name-only")
	}

	var files []string
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			files = append(files, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, trace.Wrap(err, "scan git output")
	}
	return files, nil
}

// DiffHunks extracts changed line ranges for the given paths.
// @intent map git diff output into file-level hunk ranges for overlap analysis
// @param repoDir repository root where git commands are executed
// @param baseRef git revision used as the diff baseline
// @param paths repository-relative paths to limit hunk extraction
// @return unified diff hunks with inclusive line ranges in new files
// @sideEffect executes git diff with zero-context output
// @requires repoDir points to a valid git working tree
// @ensures each returned hunk has a file path and inclusive start/end lines
func (g *ExecGitClient) DiffHunks(ctx context.Context, repoDir, baseRef string, paths []string) ([]Hunk, error) {
	args := []string{"diff", "-U0", baseRef, "--"}
	args = append(args, paths...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return nil, trace.Wrap(err, "git diff -U0")
	}

	var hunks []Hunk
	var currentFile string
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		if after, ok := strings.CutPrefix(line, "+++ b/"); ok {
			currentFile = after
		} else if strings.HasPrefix(line, "@@ ") {
			start, count := parseHunkHeader(line)
			if start > 0 && currentFile != "" {
				end := max(start+count-1, start)
				hunks = append(hunks, Hunk{
					FilePath:  currentFile,
					StartLine: start,
					EndLine:   end,
				})
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, trace.Wrap(err, "scan git diff output")
	}
	return hunks, nil
}

// parseHunkHeader parses the added-side range from a unified diff header.
// @intent decode git hunk metadata into line numbers usable for overlap checks
// @param line unified diff header line beginning with @@
// @return new-file start line and line count from the hunk header
// @ensures malformed headers return zero values instead of panicking
func parseHunkHeader(line string) (start, count int) {
	// @@ -oldStart,oldCount +newStart,newCount @@
	_, after, ok := strings.Cut(line, "+")
	if !ok {
		return 0, 0
	}
	rest, _, _ := strings.Cut(after, " ")
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
