package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	NotReadyThreshold    time.Duration
	PollInterval         time.Duration
	CooldownPeriod       time.Duration
	MaxConcurrentRestarts int
	AMTPort              int
	AMTUsername          string
	AMTPassword          string
	AMTAnnotation        string
	DryRun               bool
	MetricsAddr          string
	LeaseName            string
	LeaseNamespace       string
}

func Load() (*Config, error) {
	c := &Config{
		NotReadyThreshold:    15 * time.Minute,
		PollInterval:         30 * time.Second,
		CooldownPeriod:       1 * time.Hour,
		MaxConcurrentRestarts: 1,
		AMTPort:              16992,
		AMTAnnotation:        "watchdog.example.com/amt-ip",
		DryRun:               true,
		MetricsAddr:          ":8080",
		LeaseName:            "node-watchdog",
		LeaseNamespace:       "node-watchdog",
	}

	if v := os.Getenv("NOT_READY_THRESHOLD"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("invalid NOT_READY_THRESHOLD: %w", err)
		}
		c.NotReadyThreshold = d
	}

	if v := os.Getenv("POLL_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("invalid POLL_INTERVAL: %w", err)
		}
		c.PollInterval = d
	}

	if v := os.Getenv("COOLDOWN_PERIOD"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("invalid COOLDOWN_PERIOD: %w", err)
		}
		c.CooldownPeriod = d
	}

	if v := os.Getenv("MAX_CONCURRENT_RESTARTS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid MAX_CONCURRENT_RESTARTS: %w", err)
		}
		c.MaxConcurrentRestarts = n
	}

	if v := os.Getenv("AMT_PORT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid AMT_PORT: %w", err)
		}
		c.AMTPort = n
	}

	c.AMTUsername = os.Getenv("AMT_USERNAME")
	c.AMTPassword = os.Getenv("AMT_PASSWORD")

	if v := os.Getenv("AMT_ANNOTATION"); v != "" {
		c.AMTAnnotation = v
	}

	if v := os.Getenv("DRY_RUN"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("invalid DRY_RUN: %w", err)
		}
		c.DryRun = b
	}

	if v := os.Getenv("METRICS_ADDR"); v != "" {
		c.MetricsAddr = v
	}

	if v := os.Getenv("LEASE_NAME"); v != "" {
		c.LeaseName = v
	}

	if v := os.Getenv("LEASE_NAMESPACE"); v != "" {
		c.LeaseNamespace = v
	}

	if err := c.Validate(); err != nil {
		return nil, err
	}

	return c, nil
}

func (c *Config) Validate() error {
	if c.AMTUsername == "" {
		return fmt.Errorf("AMT_USERNAME is required")
	}
	if c.AMTPassword == "" {
		return fmt.Errorf("AMT_PASSWORD is required")
	}
	if c.NotReadyThreshold < time.Minute {
		return fmt.Errorf("NOT_READY_THRESHOLD must be at least 1m")
	}
	if c.PollInterval < 5*time.Second {
		return fmt.Errorf("POLL_INTERVAL must be at least 5s")
	}
	if c.MaxConcurrentRestarts < 1 {
		return fmt.Errorf("MAX_CONCURRENT_RESTARTS must be at least 1")
	}
	return nil
}
