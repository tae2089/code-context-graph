package graph

import (
	"encoding/json"
	"strings"
)

const inheritsFingerprintV2Prefix = "inherits:v2:"

// inheritsFingerprintV2 stores the versioned JSON payload for inheritance fingerprints.
// @intent keep child, file, and parent data in one stable payload for edge resolution.
type inheritsFingerprintV2 struct {
	Child  string `json:"child"`
	File   string `json:"file"`
	Parent string `json:"parent"`
}

// BuildInheritsFingerprintV2 encodes inherits edges with a versioned JSON payload.
// @intent provide an unambiguous fingerprint contract for inheritance edges across languages.
func BuildInheritsFingerprintV2(filePath, child, parent string) string {
	payload, err := json.Marshal(inheritsFingerprintV2{Child: child, File: filePath, Parent: parent})
	if err != nil {
		return inheritsFingerprintV2Prefix
	}
	return inheritsFingerprintV2Prefix + string(payload)
}

// ParseInheritsFingerprint decodes v2 fingerprints first and falls back to the legacy contract.
// @intent keep resolver compatibility while parsers migrate to the unambiguous inherits fingerprint format.
func ParseInheritsFingerprint(filePath, fingerprint string) (child, parent string, ok bool) {
	if payload := strings.TrimPrefix(fingerprint, inheritsFingerprintV2Prefix); payload != fingerprint {
		var parsed inheritsFingerprintV2
		if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
			return "", "", false
		}
		if parsed.File != filePath || parsed.Child == "" || parsed.Parent == "" {
			return "", "", false
		}
		return parsed.Child, parsed.Parent, true
	}
	prefix := "inherits:" + filePath + ":"
	if !strings.HasPrefix(fingerprint, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(fingerprint, prefix)
	idx := strings.LastIndex(rest, ":")
	if idx < 0 {
		return "", "", false
	}
	child = rest[:idx]
	parent = rest[idx+1:]
	return child, parent, child != "" && parent != ""
}
