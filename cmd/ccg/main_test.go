package main

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/store/gormstore"
)

func setupNamespaceMigrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate store: %v", err)
	}
	if err := db.AutoMigrate(&model.SearchDocument{}, &model.Flow{}, &model.FlowMembership{}); err != nil {
		t.Fatalf("migrate extra models: %v", err)
	}
	return db
}

func TestMigrateLegacyDefaultNamespace_BackfillsEmptyNamespaceRows(t *testing.T) {
	db := setupNamespaceMigrationDB(t)

	if err := db.Exec(`INSERT INTO nodes(namespace, qualified_name, kind, name, file_path, start_line, end_line, language, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, "", "pkg.Legacy", model.NodeKindFunction, "Legacy", "legacy.go", 1, 2, "go").Error; err != nil {
		t.Fatalf("insert legacy node: %v", err)
	}
	var node model.Node
	if err := db.Where("namespace = ? AND qualified_name = ?", "", "pkg.Legacy").First(&node).Error; err != nil {
		t.Fatalf("load legacy node: %v", err)
	}
	if err := db.Exec(`INSERT INTO edges(namespace, from_node_id, to_node_id, kind, fingerprint, created_at) VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`, "", node.ID, node.ID, model.EdgeKindCalls, "legacy-edge").Error; err != nil {
		t.Fatalf("insert legacy edge: %v", err)
	}
	if err := db.Create(&model.SearchDocument{Namespace: "", NodeID: node.ID, Content: "legacy doc", Language: "go"}).Error; err != nil {
		t.Fatalf("create search doc: %v", err)
	}
	if err := db.Exec(`INSERT INTO communities(namespace, key, label, strategy, created_at, updated_at) VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, "", "legacy", "legacy", "directory").Error; err != nil {
		t.Fatalf("insert legacy community: %v", err)
	}
	var community model.Community
	if err := db.Where("namespace = ? AND key = ?", "", "legacy").First(&community).Error; err != nil {
		t.Fatalf("load legacy community: %v", err)
	}
	if err := db.Exec(`INSERT INTO flows(namespace, name, created_at) VALUES (?, ?, CURRENT_TIMESTAMP)`, "", "legacy-flow").Error; err != nil {
		t.Fatalf("insert legacy flow: %v", err)
	}
	var flow model.Flow
	if err := db.Where("namespace = ? AND name = ?", "", "legacy-flow").First(&flow).Error; err != nil {
		t.Fatalf("load legacy flow: %v", err)
	}
	if err := db.Exec(`INSERT INTO flow_memberships(namespace, flow_id, node_id, ordinal) VALUES (?, ?, ?, ?)`, "", flow.ID, node.ID, 0).Error; err != nil {
		t.Fatalf("insert flow membership: %v", err)
	}

	if err := migrateLegacyDefaultNamespace(db); err != nil {
		t.Fatalf("migrate legacy default namespace: %v", err)
	}

	for _, tc := range []struct {
		name  string
		model any
	}{
		{name: "nodes", model: &model.Node{}},
		{name: "edges", model: &model.Edge{}},
		{name: "search_documents", model: &model.SearchDocument{}},
		{name: "communities", model: &model.Community{}},
		{name: "flows", model: &model.Flow{}},
		{name: "flow_memberships", model: &model.FlowMembership{}},
	} {
		var legacyCount int64
		if err := db.Model(tc.model).Where("namespace = ?", "").Count(&legacyCount).Error; err != nil {
			t.Fatalf("count legacy %s: %v", tc.name, err)
		}
		if legacyCount != 0 {
			t.Fatalf("expected no legacy rows in %s, got %d", tc.name, legacyCount)
		}
	}

	var defaultNode model.Node
	if err := db.Where("qualified_name = ?", "pkg.Legacy").First(&defaultNode).Error; err != nil {
		t.Fatalf("load migrated node: %v", err)
	}
	if defaultNode.Namespace != ctxns.DefaultNamespace {
		t.Fatalf("migrated node namespace = %q, want %q", defaultNode.Namespace, ctxns.DefaultNamespace)
	}
}

func TestMigrateLegacyDefaultNamespace_FailsOnNodeCollision(t *testing.T) {
	db := setupNamespaceMigrationDB(t)

	current := model.Node{Namespace: ctxns.DefaultNamespace, QualifiedName: "pkg.Collision", Kind: model.NodeKindFunction, Name: "Collision", FilePath: "same.go", StartLine: 1, EndLine: 2, Language: "go"}
	if err := db.Exec(`INSERT INTO nodes(namespace, qualified_name, kind, name, file_path, start_line, end_line, language, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, "", "pkg.Collision", model.NodeKindFunction, "Collision", "same.go", 1, 2, "go").Error; err != nil {
		t.Fatalf("insert legacy node: %v", err)
	}
	if err := db.Create(&current).Error; err != nil {
		t.Fatalf("create default node: %v", err)
	}

	err := migrateLegacyDefaultNamespace(db)
	if err == nil {
		t.Fatal("expected collision error")
	}

	var legacyCount int64
	if err := db.Model(&model.Node{}).Where("namespace = ?", "").Count(&legacyCount).Error; err != nil {
		t.Fatalf("count legacy nodes: %v", err)
	}
	if legacyCount != 1 {
		t.Fatalf("expected legacy row to remain after failed migration, got %d", legacyCount)
	}
}
