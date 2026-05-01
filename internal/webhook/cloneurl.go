package webhook

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

func ResolveCloneURL(repoFullName, payloadCloneURL, baseURL string, allowPayload bool) (string, error) {
	repoPath, err := normalizeRepoPath(repoFullName)
	if err != nil {
		return "", err
	}

	if strings.TrimSpace(baseURL) != "" {
		return buildCloneURL(baseURL, repoPath)
	}
	if allowPayload && strings.TrimSpace(payloadCloneURL) != "" {
		return payloadCloneURL, nil
	}
	return "", fmt.Errorf("clone URL is not configured for repo %q", repoFullName)
}

func normalizeRepoPath(repoFullName string) (string, error) {
	parts := strings.Split(repoFullName, "/")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid repo name %q", repoFullName)
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("invalid repo name %q", repoFullName)
		}
	}
	cleaned := path.Clean(repoFullName)
	if cleaned != repoFullName || strings.HasPrefix(cleaned, "/") {
		return "", fmt.Errorf("invalid repo name %q", repoFullName)
	}
	return cleaned, nil
}

func buildCloneURL(baseURL, repoPath string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse repo clone base URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("repo clone base URL must include scheme and host")
	}
	cloneURL := *parsed
	cloneURL.Path = path.Join(parsed.Path, repoPath) + ".git"
	return cloneURL.String(), nil
}
