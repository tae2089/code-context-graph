package graph

import (
	"reflect"
	"strings"
	"testing"
)

func TestGraphModelStringFieldsUseText(t *testing.T) {
	models := []any{
		Node{},
		Edge{},
		Annotation{},
		DocTag{},
		Community{},
		Flow{},
		FlowMembership{},
		ParseCacheEntry{},
		SearchDocument{},
		UnresolvedEdgeCandidate{},
		UnresolvedIndexState{},
		SchemaVersion{},
	}

	for _, model := range models {
		modelType := reflect.TypeOf(model)
		for i := 0; i < modelType.NumField(); i++ {
			field := modelType.Field(i)
			if field.Type.Kind() != reflect.String {
				continue
			}
			tag := field.Tag.Get("gorm")
			if !strings.Contains(tag, "type:text") {
				t.Errorf("%s.%s gorm tag = %q, want type:text", modelType.Name(), field.Name, tag)
			}
			if strings.Contains(tag, "size:") {
				t.Errorf("%s.%s gorm tag = %q, must not use size", modelType.Name(), field.Name, tag)
			}
		}
	}
}
