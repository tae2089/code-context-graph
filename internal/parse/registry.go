package parse

type LanguageSpec struct {
	Name       string
	Extensions []string
}

type Registry struct {
	byName map[string]*LanguageSpec
	byExt  map[string]*LanguageSpec
}

func NewRegistry() *Registry {
	return &Registry{
		byName: make(map[string]*LanguageSpec),
		byExt:  make(map[string]*LanguageSpec),
	}
}

func (r *Registry) Register(spec *LanguageSpec) {
	old, exists := r.byName[spec.Name]
	if exists {
		for _, ext := range old.Extensions {
			delete(r.byExt, ext)
		}
	}

	r.byName[spec.Name] = spec
	for _, ext := range spec.Extensions {
		r.byExt[ext] = spec
	}
}

func (r *Registry) Lookup(name string) *LanguageSpec {
	return r.byName[name]
}

func (r *Registry) LookupByExtension(ext string) *LanguageSpec {
	return r.byExt[ext]
}
