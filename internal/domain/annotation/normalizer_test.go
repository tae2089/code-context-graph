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

func TestNormalize_PythonDocstringPrefixes(t *testing.T) {
	n := NewNormalizer()
	accepted := []struct {
		name  string
		input string
		want  string
	}{
		{name: "raw", input: "r\"\"\"@intent raw docstring.\"\"\"", want: "@intent raw docstring."},
		{name: "unicode", input: "u\"\"\"@intent unicode docstring.\"\"\"", want: "@intent unicode docstring."},
		{name: "raw single quote", input: "r'''@intent raw single docstring.'''", want: "@intent raw single docstring."},
	}

	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			got := n.Normalize(tc.input, "python")
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}

	rejected := []struct {
		name  string
		input string
	}{
		{name: "format", input: "f\"\"\"@intent format docstring.\"\"\""},
		{name: "bytes", input: "b\"\"\"@intent bytes docstring.\"\"\""},
		{name: "raw bytes", input: "rb\"\"\"@intent raw bytes docstring.\"\"\""},
		{name: "format raw", input: "fr\"\"\"@intent format raw docstring.\"\"\""},
		{name: "format single quote", input: "f'''@intent format single docstring.'''"},
	}

	for _, tc := range rejected {
		t.Run("reject_"+tc.name, func(t *testing.T) {
			got := n.Normalize(tc.input, "python")
			if got == "@intent format docstring." ||
				got == "@intent bytes docstring." ||
				got == "@intent raw bytes docstring." ||
				got == "@intent format raw docstring." ||
				got == "@intent format single docstring." {
				t.Fatalf("unsupported python literal prefix should not normalize as docstring: %q", got)
			}
		})
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

func TestNormalize_GoDirectiveSkip(t *testing.T) {
	n := NewNormalizer()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "go:generate after intent",
			input: "// @intent 약 타입 이넘\n//go:generate stringer -type=Pill",
			want:  "@intent 약 타입 이넘",
		},
		{
			name:  "go:noinline after intent",
			input: "// @intent 인라인 금지\n//go:noinline",
			want:  "@intent 인라인 금지",
		},
		{
			name:  "directive between intent and domainRule",
			input: "// @intent 상태\n//go:generate stringer -type=S\n// @domainRule 완료 불가역",
			want:  "@intent 상태\n@domainRule 완료 불가역",
		},
		{
			name:  "non-directive // go: phrase is kept",
			input: "// @intent // go: style discussion",
			want:  "@intent // go: style discussion",
		},
		{
			name:  "go:build constraint after intent",
			input: "// @intent 빌드 제약\n//go:build linux",
			want:  "@intent 빌드 제약",
		},
		{
			name:  "go:embed after intent",
			input: "// @intent 임베드\n//go:embed assets/*",
			want:  "@intent 임베드",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := n.Normalize(tc.input, "go")
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNormalize_PhpDocComment(t *testing.T) {
	n := NewNormalizer()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "single line PHPDoc",
			input: "/** @intent 사용자 조회 API */",
			want:  "@intent 사용자 조회 API",
		},
		{
			name:  "multiline PHPDoc",
			input: "/**\n * @intent 사용자 조회\n * @param id 사용자 ID\n */",
			want:  "@intent 사용자 조회\n@param id 사용자 ID",
		},
		{
			name:  "double slash",
			input: "// 일반 주석",
			want:  "일반 주석",
		},
		{
			name:  "hash comment",
			input: "# 쉘 스타일 주석",
			want:  "쉘 스타일 주석",
		},
		{
			name:  "multiline double slash",
			input: "// 첫 줄\n// @intent 의도",
			want:  "첫 줄\n@intent 의도",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := n.Normalize(tc.input, "php")
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNormalize_RustDocComment(t *testing.T) {
	n := NewNormalizer()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "triple slash with space",
			input: "/// @intent 설명",
			want:  "@intent 설명",
		},
		{
			name:  "triple slash without space",
			input: "///설명",
			want:  "설명",
		},
		{
			name:  "double slash with space",
			input: "// 일반 주석",
			want:  "일반 주석",
		},
		{
			name:  "multiline triple slash block",
			input: "/// 첫 줄\n/// @intent 의도",
			want:  "첫 줄\n@intent 의도",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := n.Normalize(tc.input, "rust")
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
