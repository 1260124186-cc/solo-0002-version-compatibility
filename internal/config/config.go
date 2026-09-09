package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type Config struct {
	Address         string
	DataDirectory   string
	MaxSteps        int
	RequestTimeout  time.Duration
	ShutdownTimeout time.Duration
}

func Load() (Config, error) {
	c := Config{Address: env("COMPAT_ADDRESS", "127.0.0.1:8092"), DataDirectory: env("COMPAT_DATA_DIR", "var/version-compatibility"), MaxSteps: 50000, RequestTimeout: 10 * time.Second, ShutdownTimeout: 10 * time.Second}
	if _, _, err := net.SplitHostPort(c.Address); err != nil {
		return c, fmt.Errorf("COMPAT_ADDRESS: %w", err)
	}
	absolute, err := filepath.Abs(c.DataDirectory)
	if err != nil {
		return c, fmt.Errorf("COMPAT_DATA_DIR: %w", err)
	}
	c.DataDirectory = absolute
	if raw := os.Getenv("COMPAT_MAX_STEPS"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 100 || n > 1000000 {
			return c, fmt.Errorf("COMPAT_MAX_STEPS must be between 100 and 1000000")
		}
		c.MaxSteps = n
	}
	if raw := os.Getenv("COMPAT_REQUEST_TIMEOUT"); raw != "" {
		duration, err := time.ParseDuration(raw)
		if err != nil || duration < time.Second || duration > time.Minute {
			return c, fmt.Errorf("COMPAT_REQUEST_TIMEOUT must be between 1s and 1m")
		}
		c.RequestTimeout = duration
	}
	return c, nil
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
