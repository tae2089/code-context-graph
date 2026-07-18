package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestDockerfilePackagesPrebuiltReleaseArtifacts(t *testing.T) {
	dockerfile := readRepositoryFile(t, "Dockerfile")

	for _, contract := range []string{
		"FROM ubuntu:24.04",
		"ARG TARGETARCH=amd64",
		"ca-certificates git wget",
		"COPY container-artifacts/${TARGETARCH}/ccg /usr/local/bin/ccg",
		"COPY container-artifacts/${TARGETARCH}/ccg-server /usr/local/bin/ccg-server",
		"COPY container-artifacts/wiki /usr/share/ccg/wiki",
	} {
		if !strings.Contains(dockerfile, contract) {
			t.Errorf("Dockerfile missing artifact packaging contract %q", contract)
		}
	}

	for _, forbidden := range []string{
		"FROM node:",
		"FROM golang:",
		"go build",
		"npm ci",
		"npm run build",
	} {
		if strings.Contains(dockerfile, forbidden) {
			t.Errorf("Dockerfile must package prebuilt artifacts, found %q", forbidden)
		}
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
		"os: ubuntu-24.04",
		"name: ccg-linux-amd64",
		"path: artifacts/ccg-linux-amd64",
		"name: ccg-linux-arm64",
		"path: artifacts/ccg-linux-arm64",
		"name: ccg-wiki-dist",
		"path: artifacts/ccg-wiki-dist",
		"tar xzf artifacts/ccg-linux-amd64/ccg-linux-amd64.tar.gz -C container-artifacts/amd64",
		"tar xzf artifacts/ccg-linux-arm64/ccg-linux-arm64.tar.gz -C container-artifacts/arm64",
		"tar xzf artifacts/ccg-wiki-dist/ccg-wiki-dist.tar.gz -C container-artifacts/wiki",
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
	} {
		if !strings.Contains(workflow, contract) {
			t.Errorf("release workflow missing container publication contract %q", contract)
		}
	}
}

func TestCIWorkflowBuildsProductionImageWithoutPublishing(t *testing.T) {
	workflow := readRepositoryFile(t, ".github", "workflows", "ci.yml")

	for _, contract := range []string{
		"name: Prepare container artifacts",
		"make container-artifacts",
		"name: Build production image",
		"docker build",
		"--build-arg TARGETARCH=amd64",
	} {
		if !strings.Contains(workflow, contract) {
			t.Errorf("CI workflow missing build-only image contract %q", contract)
		}
	}
	if strings.Contains(workflow, "docker push") {
		t.Error("CI workflow must not publish the production image")
	}
}

func TestContainerArtifactsTargetPreparesBuiltBinariesAndWiki(t *testing.T) {
	makefile := readRepositoryFile(t, "Makefile")

	for _, contract := range []string{
		"HOST_GOOS",
		"container-artifacts: wiki-build",
		"if [ \"$(HOST_GOOS)\" = \"linux\" ]; then",
		"$(MAKE) release",
		"docker run --rm --platform linux/$(CONTAINER_ARCH)",
		"container-artifacts/$(CONTAINER_ARCH)",
		"container-artifacts/wiki",
		"cp ccg container-artifacts/$(CONTAINER_ARCH)/ccg",
		"cp ccg-server container-artifacts/$(CONTAINER_ARCH)/ccg-server",
		"cp -R web/wiki/dist/. container-artifacts/wiki/",
	} {
		if !strings.Contains(makefile, contract) {
			t.Errorf("Makefile missing container artifact preparation contract %q", contract)
		}
	}
}

func TestIntegrationStackPreparesArtifactsForItsTargetArchitecture(t *testing.T) {
	compose := readRepositoryFile(t, "docker-compose.integration.yml")
	for _, contract := range []string{
		"TARGETARCH: ${CONTAINER_ARCH:-amd64}",
	} {
		if !strings.Contains(compose, contract) {
			t.Errorf("integration compose file missing artifact packaging contract %q", contract)
		}
	}

	script := readRepositoryFile(t, "scripts", "integration-test.sh")
	for _, contract := range []string{
		"CONTAINER_ARCH=${CONTAINER_ARCH:-$(go env GOARCH)}",
		"make container-artifacts",
		"compose build ccg",
	} {
		if !strings.Contains(script, contract) {
			t.Errorf("integration script missing artifact preparation contract %q", contract)
		}
	}
	if strings.Index(script, "make container-artifacts") > strings.Index(script, "compose build ccg") {
		t.Error("integration script must prepare artifacts before building the ccg image")
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
