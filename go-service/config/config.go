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

	return &Config{
		Port:         port,
		DatabaseDSN:  getEnv("DATABASE_DSN", "postgres://iot_user:iot_secret@sensor-db:5432/sensors?sslmode=disable"),
		APIToken:     apiToken,
		LogLevel:     getEnv("LOG_LEVEL", "INFO"),
		LogFormat:    getEnv("LOG_FORMAT", "json"),
		SeedDataPath: getEnv("SEED_DATA_PATH", "/app/data/sensors.json"),
		RabbitMQURL:  getEnv("RABBITMQ_URL", "amqp://iot_service:iot_secret@rabbitmq:5672/"),
	}, nil
}

// getEnv returns the value of an environment variable or a default value.
func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
