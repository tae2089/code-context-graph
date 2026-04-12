package annotation

import "testing"

func TestNormalize_GoSlashSlash(t *testing.T) {
	n := NewNormalizer()
	got := n.Normalize("// 사용자 인증을 수행한다\n// 로그인 핸들러에서 호출됨", "go")
	want := "사용자 인증을 수행한다\n로그인 핸들러에서 호출됨"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNormalize_GoBlockComment(t *testing.T) {
	n := NewNormalizer()
	got := n.Normalize("/* 사용자 인증을 수행한다\n   로그인 핸들러에서 호출됨 */", "go")
	want := "사용자 인증을 수행한다\n로그인 핸들러에서 호출됨"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNormalize_PythonHash(t *testing.T) {
	n := NewNormalizer()
	got := n.Normalize("# 사용자 인증을 수행한다\n# 로그인 핸들러에서 호출됨", "python")
	want := "사용자 인증을 수행한다\n로그인 핸들러에서 호출됨"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNormalize_PythonDocstring(t *testing.T) {
	n := NewNormalizer()
	got := n.Normalize("\"\"\"사용자 인증을 수행한다\n로그인 핸들러에서 호출됨\"\"\"", "python")
	want := "사용자 인증을 수행한다\n로그인 핸들러에서 호출됨"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNormalize_JavaDocComment(t *testing.T) {
	n := NewNormalizer()
	got := n.Normalize("/**\n * 사용자 인증을 수행한다\n * @param username ID\n */", "java")
	want := "사용자 인증을 수행한다\n@param username ID"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNormalize_CppSlashSlash(t *testing.T) {
	n := NewNormalizer()
	got := n.Normalize("// authenticate user\n// called from login", "cpp")
	want := "authenticate user\ncalled from login"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNormalize_RubyHash(t *testing.T) {
	n := NewNormalizer()
	got := n.Normalize("# authenticate user\n# called from login", "ruby")
	want := "authenticate user\ncalled from login"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNormalize_EmptyComment(t *testing.T) {
	n := NewNormalizer()
	got := n.Normalize("", "go")
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}
