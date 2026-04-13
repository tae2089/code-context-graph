package docs

import "gorm.io/gorm"

// Generator reads the SQLite graph and writes markdown documentation.
type Generator struct {
	DB     *gorm.DB
	OutDir string
}

// Run generates index.md and per-file docs into g.OutDir.
func (g *Generator) Run() error {
	return nil
}
