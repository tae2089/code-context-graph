package parse

import (
	"testing"
)

func TestRegistry_RegisterAndLookup(t *testing.T) {
	r := NewRegistry()
	spec := &LanguageSpec{
		Name:       "go",
		Extensions: []string{".go"},
	}
	r.Register(spec)

	got := r.Lookup("go")
	if got == nil {
		t.Fatal("expected spec, got nil")
	}
	if got.Name != "go" {
		t.Errorf("Name = %q, want %q", got.Name, "go")
	}
}

func TestRegistry_LookupByExtension(t *testing.T) {
	r := NewRegistry()
	spec := &LanguageSpec{
		Name:       "go",
		Extensions: []string{".go"},
	}
	r.Register(spec)

	got := r.LookupByExtension(".go")
	if got == nil {
		t.Fatal("expected spec for .go, got nil")
	}
	if got.Name != "go" {
		t.Errorf("Name = %q, want %q", got.Name, "go")
	}
}

func TestRegistry_UnknownExtension(t *testing.T) {
	r := NewRegistry()
	got := r.LookupByExtension(".xyz")
	if got != nil {
		t.Errorf("expected nil for unknown extension, got %+v", got)
	}
}

func TestRegistry_AllLanguages(t *testing.T) {
	r := NewRegistry()

	// 15개 언어와 그 확장자 정의
	languages := []struct {
		name       string
		extensions []string
	}{
		{"go", []string{".go"}},
		{"python", []string{".py", ".pyi"}},
		{"typescript", []string{".ts", ".tsx"}},
		{"java", []string{".java"}},
		{"ruby", []string{".rb"}},
		{"javascript", []string{".js", ".jsx", ".mjs", ".cjs"}},
		{"c", []string{".c", ".h"}},
		{"cpp", []string{".cpp", ".cc", ".cxx", ".hpp", ".hh", ".hxx"}},
		{"rust", []string{".rs"}},
		{"csharp", []string{".cs"}},
		{"kotlin", []string{".kt", ".kts"}},
		{"php", []string{".php"}},
		{"swift", []string{".swift"}},
		{"scala", []string{".scala", ".sc"}},
		{"lua", []string{".lua"}},
	}

	for _, lang := range languages {
		r.Register(&LanguageSpec{
			Name:       lang.name,
			Extensions: lang.extensions,
		})
	}

	// 15개 모두 등록되었는지 확인
	for _, lang := range languages {
		got := r.Lookup(lang.name)
		if got == nil {
			t.Errorf("언어 %q가 등록되지 않음", lang.name)
			continue
		}
		for _, ext := range lang.extensions {
			gotExt := r.LookupByExtension(ext)
			if gotExt == nil {
				t.Errorf("확장자 %q로 언어 %q를 찾을 수 없음", ext, lang.name)
			} else if gotExt.Name != lang.name {
				t.Errorf("확장자 %q → %q, want %q", ext, gotExt.Name, lang.name)
			}
		}
	}
}

func TestRegistry_15Languages(t *testing.T) {
	r := NewRegistry()

	languages := []struct {
		name       string
		extensions []string
	}{
		{"go", []string{".go"}},
		{"python", []string{".py", ".pyi"}},
		{"typescript", []string{".ts", ".tsx"}},
		{"java", []string{".java"}},
		{"ruby", []string{".rb"}},
		{"javascript", []string{".js", ".jsx", ".mjs", ".cjs"}},
		{"c", []string{".c", ".h"}},
		{"cpp", []string{".cpp", ".cc", ".cxx", ".hpp", ".hh", ".hxx"}},
		{"rust", []string{".rs"}},
		{"csharp", []string{".cs"}},
		{"kotlin", []string{".kt", ".kts"}},
		{"php", []string{".php"}},
		{"swift", []string{".swift"}},
		{"scala", []string{".scala", ".sc"}},
		{"lua", []string{".lua"}},
	}

	for _, lang := range languages {
		r.Register(&LanguageSpec{
			Name:       lang.name,
			Extensions: lang.extensions,
		})
	}

	// 15개 모두 등록되었는지 확인
	if len(languages) != 15 {
		t.Fatalf("expected 15 languages, got %d", len(languages))
	}

	for _, lang := range languages {
		got := r.Lookup(lang.name)
		if got == nil {
			t.Errorf("언어 %q가 등록되지 않음", lang.name)
			continue
		}
		if got.Name != lang.name {
			t.Errorf("Lookup(%q).Name = %q, want %q", lang.name, got.Name, lang.name)
		}
		for _, ext := range lang.extensions {
			gotExt := r.LookupByExtension(ext)
			if gotExt == nil {
				t.Errorf("확장자 %q로 언어 %q를 찾을 수 없음", ext, lang.name)
			} else if gotExt.Name != lang.name {
				t.Errorf("확장자 %q → %q, want %q", ext, gotExt.Name, lang.name)
			}
		}
	}
}

func TestRegistry_DuplicateRegister(t *testing.T) {
	r := NewRegistry()
	spec1 := &LanguageSpec{
		Name:       "go",
		Extensions: []string{".go"},
	}
	spec2 := &LanguageSpec{
		Name:       "go",
		Extensions: []string{".go", ".go2"},
	}
	r.Register(spec1)
	r.Register(spec2)

	got := r.Lookup("go")
	if got == nil {
		t.Fatal("expected spec, got nil")
	}
	if len(got.Extensions) != 2 {
		t.Errorf("expected 2 extensions after overwrite, got %d", len(got.Extensions))
	}

	got2 := r.LookupByExtension(".go2")
	if got2 == nil {
		t.Fatal("expected spec for .go2 after overwrite, got nil")
	}
}
