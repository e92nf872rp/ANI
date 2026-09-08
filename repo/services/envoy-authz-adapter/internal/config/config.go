package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	GRPCPort                 int
	AuthServiceGRPCAddr      string
	InferenceServiceGRPCAddr string
	AuthTimeout              time.Duration
	InferenceTimeout         time.Duration
}

func Load() (Config, error) {
	addr := strings.TrimSpace(os.Getenv("AUTH_SERVICE_GRPC_ADDR"))
	if addr == "" {
		return Config{}, errors.New("AUTH_SERVICE_GRPC_ADDR is required")
	}
	inferenceAddr := strings.TrimSpace(os.Getenv("INFERENCE_SERVICE_GRPC_ADDR"))
	if inferenceAddr == "" {
		return Config{}, errors.New("INFERENCE_SERVICE_GRPC_ADDR is required")
	}
	port, err := positiveInt("GRPC_PORT", 9002)
	if err != nil {
		return Config{}, err
	}
	timeout, err := positiveDuration("AUTH_TIMEOUT", 2*time.Second)
	if err != nil {
		return Config{}, err
	}
	inferenceTimeout, err := positiveDuration("INFERENCE_TIMEOUT", 2*time.Second)
	if err != nil {
		return Config{}, err
	}
	return Config{GRPCPort: port, AuthServiceGRPCAddr: addr, InferenceServiceGRPCAddr: inferenceAddr, AuthTimeout: timeout, InferenceTimeout: inferenceTimeout}, nil
}

func positiveInt(name string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		if err != nil {
			return 0, fmt.Errorf("%s must be a positive integer: %w", name, err)
		}
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}

func positiveDuration(name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		if err != nil {
			return 0, fmt.Errorf("%s must be a positive duration: %w", name, err)
		}
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return parsed, nil
}
