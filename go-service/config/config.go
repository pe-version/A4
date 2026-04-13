package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds application configuration loaded from environment variables.
type Config struct {
	Port        int
	DatabaseDSN string
	APIToken    string
	LogLevel    string
	LogFormat   string
	SeedDataPath string
	RabbitMQURL  string
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	port, err := strconv.Atoi(getEnv("PORT", "8080"))
	if err != nil {
		port = 8080
	}

	apiToken := os.Getenv("API_TOKEN")
	if apiToken == "" {
		return nil, fmt.Errorf("API_TOKEN environment variable is required")
	}

	databaseDSN := os.Getenv("DATABASE_DSN")
	if databaseDSN == "" {
		return nil, fmt.Errorf("DATABASE_DSN environment variable is required")
	}

	rabbitmqURL := os.Getenv("RABBITMQ_URL")
	if rabbitmqURL == "" {
		return nil, fmt.Errorf("RABBITMQ_URL environment variable is required")
	}

	return &Config{
		Port:         port,
		DatabaseDSN:  databaseDSN,
		APIToken:     apiToken,
		LogLevel:     getEnv("LOG_LEVEL", "INFO"),
		LogFormat:    getEnv("LOG_FORMAT", "json"),
		SeedDataPath: getEnv("SEED_DATA_PATH", "/app/data/sensors.json"),
		RabbitMQURL:  rabbitmqURL,
	}, nil
}

// getEnv returns the value of an environment variable or a default value.
func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
