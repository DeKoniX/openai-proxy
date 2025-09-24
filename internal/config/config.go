package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds application settings sourced from environment variables.
type Config struct {
	ListenAddr  string
	UpstreamURL string
	UpstreamKey string
	DatabaseURL string
	USDRUBRate  float64
}

// Load reads environment variables and returns a populated Config.
func Load() (*Config, error) {
	cfg := &Config{
		ListenAddr:  getEnvDefault("APP_LISTEN_ADDR", ":8080"),
		UpstreamURL: os.Getenv("UPSTREAM_URL"),
		UpstreamKey: os.Getenv("UPSTREAM_API_KEY"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
	}

	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}

	rateStr := getEnvDefault("USD_RUB_RATE", "90")
	rate, err := strconv.ParseFloat(rateStr, 64)
	if err != nil {
		return nil, fmt.Errorf("parse USD_RUB_RATE: %w", err)
	}
	cfg.USDRUBRate = rate

	return cfg, nil
}

func getEnvDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
