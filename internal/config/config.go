// Пакет config загружает конфигурацию приложения из YAML-файла.
// Реализуйте этот пакет самостоятельно.
package config

import (
	"errors"
	"fmt"
	"gopkg.in/yaml.v3"
	"os"
)

// Config содержит параметры запуска сервера.
// Изучите config.yaml и добавьте поля самостоятельно.
type Config struct {
	Host     string `yaml:"server_host"`
	Port     int    `yaml:"server_port"`
	Log      string `yaml:"log_level"`
	Interval int    `yaml:"accrual_interval_seconds"`
	Worker   int    `yaml:"worker_concurrency"`
}

// Load читает конфигурацию из файла config.yaml.
// Если файл не найден или поле не задано, применяются значения по умолчанию.
func Load() (*Config, error) {
	cfg := &Config{
		Host:     "localhost",
		Port:     8080,
		Log:      "info",
		Interval: 3,
		Worker:   5,
	}

	data, err := os.ReadFile("config.yaml")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return cfg, nil
}
