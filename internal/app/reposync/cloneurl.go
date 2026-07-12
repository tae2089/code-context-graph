// @index Clone URL resolution for webhook-triggered repository sync.
package reposync

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

// ResolveCloneURL selects a safe clone target from configured base URLs or the payload.
// @intent keep repository sync deterministic and prevent untrusted webhook payloads from choosing arbitrary clone endpoints unless explicitly allowed.
func ResolveCloneURL(repoFullName, payloadCloneURL string, baseURLs []string, allowPayload bool) (string, error) {
	repoPath, err := normalizeRepoPath(repoFullName)
	if err != nil {
		return "", err
	}

	var firstBaseURL string
	for _, baseURL := range baseURLs {
		if strings.TrimSpace(baseURL) == "" {
			continue
		}
		if _, err := parseCloneBaseURL(baseURL); err != nil {
			return "", err
		}
		if firstBaseURL == "" {
			firstBaseURL = baseURL
		}
	}
	if firstBaseURL != "" {
		return buildCloneURL(firstBaseURL, repoPath)
	}
	if allowPayload && strings.TrimSpace(payloadCloneURL) != "" {
		return payloadCloneURL, nil
	}
	return "", fmt.Errorf("clone URL is not configured for repo %q", repoFullName)
}

// normalizeRepoPath validates that a repo full name is a clean two-or-more
// segment path with no empty, ".", or ".." components and no leading slash.
//
// @intent reject repo identifiers that could traverse outside the intended path when joined onto a base URL.
// @domainRule each path segment must be non-empty and not equal to "." or "..".
// @domainRule path.Clean(repoFullName) must equal repoFullName and must not start with "/".
// @ensures returned path equals the input when err == nil.
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

// buildCloneURL joins repoPath onto a validated base URL and appends ".git".
//
// @intent derive a clone URL from a trusted base URL plus a normalized repo path so the host/scheme are not taken from webhook payload data.
// @domainRule resulting path is path.Join(base.Path, repoPath) + ".git".
// @ensures returned URL preserves the base URL's scheme and host when err == nil.
func buildCloneURL(baseURL, repoPath string) (string, error) {
	parsed, err := parseCloneBaseURL(baseURL)
	if err != nil {
		return "", err
	}
	cloneURL := *parsed
	cloneURL.Path = path.Join(parsed.Path, repoPath) + ".git"
	return cloneURL.String(), nil
}

// parseCloneBaseURL parses and validates a configured clone base URL.
//
// @intent ensure each configured base URL is an absolute URL with scheme and host before it is used to construct clone targets.
// @domainRule baseURL must parse via url.ParseRequestURI and url.Parse without error.
// @domainRule parsed scheme and host must be non-empty and Opaque must be empty.
func parseCloneBaseURL(baseURL string) (*url.URL, error) {
	if _, err := url.ParseRequestURI(baseURL); err != nil {
		return nil, fmt.Errorf("parse repo clone base URL: %w", err)
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse repo clone base URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" || parsed.Opaque != "" {
		return nil, fmt.Errorf("repo clone base URL must include scheme and host")
	}
	return parsed, nil
}
