package models

import (
	"fmt"
	"time"
)

type Config struct {
	Server   ServerConfig
	Redis    RedisConfig
	Policies []ClientPolicy
}

type ServerConfig struct {
	Port    int
	Timeout time.Duration
}

type RedisConfig struct {
	Host     string
	Port     int
	Password string
	DB       int
	TLS      bool
}

func (rc RedisConfig) Addr() string {
	return fmt.Sprintf("%s:%d", rc.Host, rc.Port)
}
