package gatewaypublish

import (
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultGatewayNamespace  = "ani-aigw"
	defaultGatewayName       = "ani-aigw"
	defaultGatewayController = "gateway.envoyproxy.io/gatewayclass-controller"
)

// Config holds only the publisher's process-local settings.
type Config struct {
	DatabaseURL       string
	PublicBaseURL     *url.URL
	GatewayNamespace  string
	GatewayName       string
	GatewayController string
	ReconcileInterval time.Duration
	RequestTimeout    time.Duration
	StatusTimeout     time.Duration
	LeaseDuration     time.Duration
	HealthPort        int
	AllowHTTP         bool
}

func LoadConfig() (Config, error) {
	allowHTTP, err := envBool("INFERENCE_AI_GATEWAY_ALLOW_HTTP", false)
	if err != nil {
		return Config{HealthPort: 9206}, errors.New("publisher configuration invalid")
	}
	reconcileInterval, err := envDuration("INFERENCE_AI_GATEWAY_RECONCILE_INTERVAL_SECONDS", time.Second, time.Minute)
	if err != nil {
		return Config{HealthPort: 9206}, errors.New("publisher configuration invalid")
	}
	requestTimeout, err := envDuration("INFERENCE_AI_GATEWAY_REQUEST_TIMEOUT_SECONDS", 2*time.Second, 30*time.Second)
	if err != nil {
		return Config{HealthPort: 9206}, errors.New("publisher configuration invalid")
	}
	statusTimeout, err := envDuration("INFERENCE_AI_GATEWAY_STATUS_TIMEOUT_SECONDS", 45*time.Second, 5*time.Minute)
	if err != nil {
		return Config{HealthPort: 9206}, errors.New("publisher configuration invalid")
	}
	leaseDuration, err := envDuration("INFERENCE_AI_GATEWAY_LEASE_DURATION_SECONDS", 30*time.Second, 5*time.Minute)
	if err != nil {
		return Config{HealthPort: 9206}, errors.New("publisher configuration invalid")
	}
	healthPort, err := envInt("INFERENCE_AI_GATEWAY_HEALTH_PORT", 9206)
	if err != nil {
		return Config{HealthPort: 9206}, errors.New("publisher configuration invalid")
	}
	cfg := Config{
		DatabaseURL:       strings.TrimSpace(os.Getenv("DATABASE_URL")),
		GatewayNamespace:  envString("INFERENCE_AI_GATEWAY_NAMESPACE", defaultGatewayNamespace),
		GatewayName:       envString("INFERENCE_AI_GATEWAY_NAME", defaultGatewayName),
		GatewayController: envString("INFERENCE_AI_GATEWAY_CONTROLLER", defaultGatewayController),
		ReconcileInterval: reconcileInterval,
		RequestTimeout:    requestTimeout,
		StatusTimeout:     statusTimeout,
		LeaseDuration:     leaseDuration,
		HealthPort:        healthPort,
		AllowHTTP:         allowHTTP,
	}
	if cfg.DatabaseURL == "" || cfg.GatewayNamespace != defaultGatewayNamespace || cfg.GatewayName != defaultGatewayName || cfg.GatewayController == "" {
		return cfg, errors.New("publisher configuration invalid")
	}
	if cfg.LeaseDuration <= 2*cfg.RequestTimeout {
		return cfg, errors.New("publisher configuration invalid")
	}
	base, err := parsePublicBaseURL(os.Getenv("INFERENCE_AI_GATEWAY_PUBLIC_BASE_URL"), cfg.AllowHTTP)
	if err != nil {
		return cfg, errors.New("publisher configuration invalid")
	}
	cfg.PublicBaseURL = base
	return cfg, nil
}

func parsePublicBaseURL(raw string, allowHTTP bool) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" || strings.Contains(raw, "#") || strings.Contains(raw, "\\") {
		return nil, errors.New("invalid public base URL")
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, errors.New("invalid public base URL")
	}
	if u.Scheme != "https" && (u.Scheme != "http" || !allowHTTP) {
		return nil, errors.New("invalid public base URL")
	}
	if u.RawPath != "" || (u.Path != "" && !strings.HasPrefix(u.Path, "/")) || strings.Contains(u.Path, "//") {
		return nil, errors.New("invalid public base URL")
	}
	for _, segment := range strings.Split(u.Path, "/") {
		if segment == "." || segment == ".." {
			return nil, errors.New("invalid public base URL")
		}
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u, nil
}

func envString(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envDuration(name string, fallback, maximum time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || seconds <= 0 || seconds > int64(maximum/time.Second) {
		return 0, errors.New("invalid duration")
	}
	value := time.Duration(seconds) * time.Second
	if value <= 0 || value > maximum {
		return 0, errors.New("invalid duration")
	}
	return value, nil
}

func envInt(name string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > 65535 {
		return 0, errors.New("invalid port")
	}
	return value, nil
}

func envBool(name string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	if strings.EqualFold(raw, "true") {
		return true, nil
	}
	if strings.EqualFold(raw, "false") {
		return false, nil
	}
	return false, errors.New("invalid boolean")
}
