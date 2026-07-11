package config

import (
	"strings"

	"github.com/spf13/viper"
)

// MigrationsDir returns the configured migration directory.
//
// Empty string means use embedded migrations.
// @intent 외부 migration 디렉터리를 지정했는지 확인하고 공백은 비설정으로 정규화한다.
func MigrationsDir() string {
	return strings.TrimSpace(viper.GetString("migrations.dir"))
}

// RagIndexDir returns the legacy-named Wiki index directory setting.
// @intent Wiki 호환 snapshot 출력 경로를 config helper로 재사용한다.
func RagIndexDir() string {
	return viper.GetString("rag.index_dir")
}

// RagDescription returns the legacy-named Wiki root description setting.
// @intent Wiki root summary에 포함할 프로젝트 설명 문자열을 config helper로 노출한다.
func RagDescription() string {
	return viper.GetString("rag.description")
}
