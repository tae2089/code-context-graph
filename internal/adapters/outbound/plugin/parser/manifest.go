// @index Parser plugin manifest parsing and API-version compatibility.
package parser

import (
	"encoding/json"
	"strings"

	"github.com/tae2089/trace"
)

// manifest describes a parser plugin's identity, handled extensions, and declared capabilities.
// @intent let core discover which extensions a plugin handles and gate api compatibility before spawning it.
type manifest struct {
	Key          string   `json:"key"`
	APIVersion   string   `json:"api_version"`
	Entry        string   `json:"entry"`
	Extensions   []string `json:"extensions"`
	Languages    []string `json:"languages"`
	Capabilities []string `json:"capabilities"`
}

// parseManifest decodes a manifest.json payload.
// @intent turn a plugin manifest file into a validated struct core can dispatch on.
func parseManifest(data []byte) (manifest, error) {
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return manifest{}, trace.Wrap(err, "parse parser plugin manifest")
	}
	return m, nil
}

// apiMajorCompatible reports whether a plugin api version shares core's major version.
// @intent gate plugins by semver-major only, so additive minor changes stay compatible.
func apiMajorCompatible(pluginVer, coreVer string) bool {
	return apiMajor(pluginVer) == apiMajor(coreVer)
}

// apiMajor extracts the major segment of a "MAJOR.MINOR" api version string.
func apiMajor(version string) string {
	major, _, _ := strings.Cut(version, ".")
	return major
}
