// Package config loads runtime settings from the environment.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type Config struct {
	DataDir            string
	Addr               string
	OCRURL             string
	OCRFallbackURL     string
	FoodFactsUserAgent string
	Workers            int
	Location           *time.Location
}

func (c Config) DBPath() string    { return filepath.Join(c.DataDir, "pantry.db") }
func (c Config) ImagesDir() string { return filepath.Join(c.DataDir, "images") }

func Load() (Config, error) {
	c := Config{
		DataDir:            env("PANTRY_DATA_DIR", "./data"),
		Addr:               env("PANTRY_ADDR", ":8080"),
		OCRURL:             env("OCR_URL", "http://localhost:8000"),
		OCRFallbackURL:     env("OCR_FALLBACK_URL", ""),
		FoodFactsUserAgent: env("OFF_USER_AGENT", "Pantry/0.1 (self-hosted)"),
	}

	workers, err := strconv.Atoi(env("PANTRY_WORKERS", "1"))
	if err != nil || workers < 1 {
		return c, fmt.Errorf("PANTRY_WORKERS must be a positive integer")
	}
	c.Workers = workers

	loc, err := time.LoadLocation(env("PANTRY_TZ", "Europe/Amsterdam"))
	if err != nil {
		return c, fmt.Errorf("PANTRY_TZ: %w", err)
	}
	c.Location = loc

	return c, nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
