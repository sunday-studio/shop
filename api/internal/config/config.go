// Package config loads runtime settings from the environment.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DataDir            string
	Addr               string
	OCRURL             string
	OCRFallbackURL     string
	FoodFactsUserAgent string
	FoodFactsBaseURL   string // OFF_BASE_URL: empty = the real Open Food Facts (the e2e stack points it at a fake)
	Workers            int
	LogoDevToken       string // logo.dev publishable key; logos are off without it
	BackupDir          string
	BackupKeepDays     int
	BackupAt           string // HH:MM local time for scheduled backups
	AutoProducts       bool   // AUTO_PRODUCTS (default on): confirm creates/links products, backfill allowed
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
		FoodFactsBaseURL:   env("OFF_BASE_URL", ""),
	}

	c.LogoDevToken = env("LOGO_DEV_TOKEN", "")
	switch strings.ToLower(env("AUTO_PRODUCTS", "on")) {
	case "on", "true", "1", "yes":
		c.AutoProducts = true
	}
	c.BackupDir = env("BACKUP_DIR", "/backup")
	c.BackupAt = env("BACKUP_AT", "03:30")
	keep, err := strconv.Atoi(env("BACKUP_KEEP_DAYS", "30"))
	if err != nil || keep < 1 {
		return c, fmt.Errorf("BACKUP_KEEP_DAYS must be a positive integer")
	}
	c.BackupKeepDays = keep

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
