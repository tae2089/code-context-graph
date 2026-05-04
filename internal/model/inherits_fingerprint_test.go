package model

import "testing"

func TestBuildInheritsFingerprintV2Canonical(t *testing.T) {
	got := BuildInheritsFingerprintV2("a.py", "M.C", "M.P")
	want := `inherits:v2:{"child":"M.C","file":"a.py","parent":"M.P"}`
	if got != want {
		t.Fatalf("BuildInheritsFingerprintV2() = %q, want %q", got, want)
	}
}

func TestParseInheritsFingerprintV2(t *testing.T) {
	child, parent, ok := ParseInheritsFingerprint(
		`pkg:file".rs`,
		BuildInheritsFingerprintV2(`pkg:file".rs`, `crate::child:Child`, `crate::base::Parent`),
	)
	if !ok {
		t.Fatal("ParseInheritsFingerprint() returned ok=false, want true")
	}
	if child != `crate::child:Child` || parent != `crate::base::Parent` {
		t.Fatalf("ParseInheritsFingerprint() = (%q, %q), want (%q, %q)", child, parent, `crate::child:Child`, `crate::base::Parent`)
	}
}

func TestParseInheritsFingerprintLegacy(t *testing.T) {
	child, parent, ok := ParseInheritsFingerprint("foo.py", "inherits:foo.py:Child:Parent")
	if !ok {
		t.Fatal("ParseInheritsFingerprint() returned ok=false, want true")
	}
	if child != "Child" || parent != "Parent" {
		t.Fatalf("ParseInheritsFingerprint() = (%q, %q), want (%q, %q)", child, parent, "Child", "Parent")
	}
}

func TestParseInheritsFingerprintUnknown(t *testing.T) {
	if _, _, ok := ParseInheritsFingerprint("foo.py", "inherits:v2:not-json"); ok {
		t.Fatal("ParseInheritsFingerprint() ok=true, want false")
	}
	if _, _, ok := ParseInheritsFingerprint("foo.py", "something-else"); ok {
		t.Fatal("ParseInheritsFingerprint() ok=true for unknown prefix, want false")
	}
}
