package changes

import (
	"bufio"
	"context"
	"os/exec"
	"strconv"
	"strings"

	"github.com/tae2089/trace"
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
