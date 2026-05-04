package config

import (
	"strings"

	"github.com/spf13/viper"
)

// DatabaseDriver returns the configured database driver.
func DatabaseDriver() string {
	return viper.GetString("db.driver")
}

// DatabaseDSN returns the configured database DSN.
func DatabaseDSN() string {
	return viper.GetString("db.dsn")
}

// MigrationsDir returns the configured migration directory.
//
// Empty string means use embedded migrations.
func MigrationsDir() string {
	return strings.TrimSpace(viper.GetString("migrations.dir"))
}

// RagIndexDir returns the configured RAG index directory.
func RagIndexDir() string {
	return viper.GetString("rag.index_dir")
}

// RagDescription returns the configured RAG project description.
func RagDescription() string {
	return viper.GetString("rag.description")
}
