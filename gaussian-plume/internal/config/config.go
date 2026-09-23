// Package config 从环境变量读取服务配置，给出适配 docker-compose 的默认值。
package config

import (
	"os"
)

// Config 是服务运行配置。
type Config struct {
	HTTPAddr       string // HTTP 监听地址
	DatabaseURL    string // PostgreSQL DSN
	DBMaxConns     int32
	SeedDemoOnBoot bool // 启动时预置地面源示范作业
}

// getenv 带默认值读取环境变量。
func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Load 读取配置。
func Load() Config {
	return Config{
		HTTPAddr:       getenv("HTTP_ADDR", ":8080"),
		DatabaseURL:    getenv("DATABASE_URL", "postgres://plume:plume@localhost:5432/plume?sslmode=disable"),
		DBMaxConns:     10,
		SeedDemoOnBoot: getenv("SEED_DEMO", "true") != "false",
	}
}
