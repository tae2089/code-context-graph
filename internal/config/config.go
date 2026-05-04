package config

import (
	"strings"

	"github.com/spf13/viper"
)

// DatabaseDriver returns the configured database driver.
// @intent 설정에서 현재 런타임이 사용할 DB 드라이버를 공통 helper로 읽는다.
func DatabaseDriver() string {
	return viper.GetString("db.driver")
}

// DatabaseDSN returns the configured database DSN.
// @intent 설정에서 현재 런타임이 사용할 DB 연결 문자열을 공통 helper로 읽는다.
func DatabaseDSN() string {
	return viper.GetString("db.dsn")
}

// MigrationsDir returns the configured migration directory.
//
// Empty string means use embedded migrations.
// @intent 외부 migration 디렉터리를 지정했는지 확인하고 공백은 비설정으로 정규화한다.
func MigrationsDir() string {
	return strings.TrimSpace(viper.GetString("migrations.dir"))
}

// RagIndexDir returns the configured RAG index directory.
// @intent 문서/RAG 인덱스 출력 경로를 config helper로 재사용한다.
func RagIndexDir() string {
	return viper.GetString("rag.index_dir")
}

// RagDescription returns the configured RAG project description.
// @intent RAG 인덱스에 포함할 프로젝트 설명 문자열을 config helper로 노출한다.
func RagDescription() string {
	return viper.GetString("rag.description")
}
