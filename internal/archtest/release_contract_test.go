package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestDockerfileInjectsReleaseMetadataIntoBothBinaries(t *testing.T) {
	dockerfile := readRepositoryFile(t, "Dockerfile")

	for _, contract := range []string{
		"ARG VERSION=dev",
		"ARG COMMIT=unknown",
		"ARG DATE=unknown",
	} {
		if !strings.Contains(dockerfile, contract) {
			t.Errorf("Dockerfile missing release metadata contract %q", contract)
		}
	}

	const linkerFlags = `-X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}`
	if count := strings.Count(dockerfile, linkerFlags); count != 2 {
		t.Errorf("Dockerfile release linker flags count = %d, want 2 (ccg and ccg-server)", count)
	}
}

func TestReleaseWorkflowPublishesVersionedMultiPlatformImage(t *testing.T) {
	workflow := readRepositoryFile(t, ".github", "workflows", "release.yml")
	var parsed struct {
		Jobs map[string]struct {
			Needs       any               `yaml:"needs"`
			Permissions map[string]string `yaml:"permissions"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(workflow), &parsed); err != nil {
		t.Fatalf("parse release workflow: %v", err)
	}
	containerJob, ok := parsed.Jobs["publish-container"]
	if !ok {
		t.Fatal("release workflow missing publish-container job")
	}
	if containerJob.Needs != "release" {
		t.Errorf("publish-container needs = %q, want release", containerJob.Needs)
	}
	if got := containerJob.Permissions["contents"]; got != "read" {
		t.Errorf("publish-container contents permission = %q, want read", got)
	}
	if got := containerJob.Permissions["packages"]; got != "write" {
		t.Errorf("publish-container packages permission = %q, want write", got)
	}

	for _, contract := range []string{
		"docker/login-action@v4",
		"docker/metadata-action@v6",
		"docker/setup-qemu-action@v4",
		"docker/setup-buildx-action@v4",
		"docker/build-push-action@v7",
		"registry: ghcr.io",
		"images: ghcr.io/${{ github.repository }}",
		"flavor: latest=false",
		"type=semver,pattern={{version}}",
		"type=semver,pattern={{major}}.{{minor}}",
		"type=semver,pattern={{major}}",
		"type=raw,value=latest,enable=${{ !contains(github.ref_name, '-') }}",
		"platforms: linux/amd64,linux/arm64",
		"push: true",
		"VERSION=${{ steps.build-meta.outputs.version }}",
		"COMMIT=${{ steps.build-meta.outputs.commit }}",
		"DATE=${{ steps.build-meta.outputs.date }}",
	} {
		if !strings.Contains(workflow, contract) {
			t.Errorf("release workflow missing container publication contract %q", contract)
		}
	}
}

func TestCIWorkflowBuildsProductionImageWithoutPublishing(t *testing.T) {
	workflow := readRepositoryFile(t, ".github", "workflows", "ci.yml")

	for _, contract := range []string{
		"name: Build production image",
		"docker build",
		"--build-arg VERSION=ci",
		"--build-arg COMMIT=${{ github.sha }}",
		"--build-arg DATE=unknown",
	} {
		if !strings.Contains(workflow, contract) {
			t.Errorf("CI workflow missing build-only image contract %q", contract)
		}
	}
	if strings.Contains(workflow, "docker push") {
		t.Error("CI workflow must not publish the production image")
	}
}

func readRepositoryFile(t *testing.T, pathParts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{repositoryRoot(t)}, pathParts...)...)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read repository file %s: %v", path, err)
	}
	return string(content)
}
