package config

import "time"

// Minimal config subset needed by the embedded xtpro server.
// This intentionally avoids .env loading; Nezha config drives everything.

type Config struct {
	Server   ServerConfig
	Auth     AuthConfig
	Database DatabaseConfig
}

type ServerConfig struct {
	Host            string
	Port            int
	PublicPortStart int
	PublicPortEnd   int
	PublicHost      string
	HTTPDomain      string
	HTTPPort        int
}

type DatabaseConfig struct {
	Path string
}

type AuthConfig struct {
	JWTSecret   string
	TokenExpiry time.Duration
}

func (c *Config) GetDatabaseDSN() string {
	return c.Database.Path
}

