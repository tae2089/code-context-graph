package search

import (
	"context"

	"gorm.io/gorm"

	"github.com/imtaebin/code-context-graph/internal/model"
)

type Backend interface {
	Migrate(db *gorm.DB) error
	Rebuild(ctx context.Context, db *gorm.DB) error
	Query(ctx context.Context, db *gorm.DB, query string, limit int) ([]model.Node, error)
}
