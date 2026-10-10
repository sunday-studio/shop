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

func (c Config) DBPath() string    { return filepath.Join(c.DataDir, "shop.db") }
func (c Config) ImagesDir() string { return filepath.Join(c.DataDir, "images") }

func Load() (Config, error) {
	c := Config{
		DataDir:            env("SHOP_DATA_DIR", "./data"),
		Addr:               env("SHOP_ADDR", ":8080"),
		OCRURL:             env("OCR_URL", "http://localhost:8000"),
		OCRFallbackURL:     env("OCR_FALLBACK_URL", ""),
		FoodFactsUserAgent: env("OFF_USER_AGENT", "Shop/0.1 (self-hosted)"),
	}

	workers, err := strconv.Atoi(env("SHOP_WORKERS", "1"))
	if err != nil || workers < 1 {
		return c, fmt.Errorf("SHOP_WORKERS must be a positive integer")
	}
	c.Workers = workers

	loc, err := time.LoadLocation(env("SHOP_TZ", "Europe/Amsterdam"))
	if err != nil {
		return c, fmt.Errorf("SHOP_TZ: %w", err)
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
