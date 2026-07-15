package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const defaultBaseURL = "http://127.0.0.1:4010/api/v1"

var (
	Version   = "dev"
	BuildTime = "unknown"
)

var servicesResources = map[string]bool{
	"model":              true,
	"models":             true,
	"inference":          true,
	"inference-service":  true,
	"inference-services": true,
	"knowledge-base":     true,
	"knowledge-bases":    true,
	"kb":                 true,
}

type command struct {
	Method string
	Path   string
	Query  url.Values
	Body   string
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout io.Writer, stderr io.Writer) int {
	global := flag.NewFlagSet("ani", flag.ContinueOnError)
	global.SetOutput(stderr)
	baseURL := global.String("base-url", envOrDefault("ANI_BASE_URL", defaultBaseURL), "Core API base URL")
	token := global.String("token", os.Getenv("ANI_TOKEN"), "Core API bearer token")
	showVersion := global.Bool("version", false, "print ANI Core CLI version")
	versionFormat := global.String("version-format", "text", "version output format: text or json")
	if err := global.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		if err := printVersion(stdout, *versionFormat); err != nil {
			if _, writeErr := fmt.Fprintln(stderr, err); writeErr != nil {
				return 2
			}
			return 2
		}
		return 0
	}
	cmd, err := parseCommand(global.Args())
	if err != nil {
		if _, writeErr := fmt.Fprintln(stderr, err); writeErr != nil {
			return 2
		}
		return 2
	}
	body, err := execute(context.Background(), http.DefaultClient, strings.TrimRight(*baseURL, "/"), *token, cmd)
	if err != nil {
		if _, writeErr := fmt.Fprintln(stderr, err); writeErr != nil {
			return 1
		}
		return 1
	}
	_, _ = stdout.Write(body)
	if len(body) == 0 || body[len(body)-1] != '\n' {
		_, _ = fmt.Fprintln(stdout)
	}
	return 0
}

func printVersion(stdout io.Writer, format string) error {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "text":
		_, _ = fmt.Fprintf(stdout, "ani version %s build %s\n", Version, BuildTime)
	case "json":
		payload := map[string]string{
			"name":       "ani",
			"scope":      "core",
			"version":    Version,
			"build_time": BuildTime,
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		_, _ = stdout.Write(encoded)
		_, _ = fmt.Fprintln(stdout)
	default:
		return fmt.Errorf("unsupported version format: %s", format)
	}
	return nil
}

func parseCommand(args []string) (command, error) {
	if len(args) < 2 {
		return command{}, errors.New("usage: ani [--base-url URL] [--token TOKEN] <core-resource> <action>")
	}
	resource := args[0]
	if servicesResources[resource] {
		return command{}, fmt.Errorf("services resources are not supported by this Core CLI: %s", resource)
	}
	action := args[1]
	rest := args[2:]
	switch resource {
	case "instances":
		return listCommand("/instances", action, rest)
	case "network-vpcs":
		return listCommand("/networks/vpcs", action, rest)
	case "network-subnets":
		return listCommand("/networks/subnets", action, rest)
	case "network-security-groups":
		return listCommand("/networks/security-groups", action, rest)
	case "network-load-balancers":
		return listCommand("/networks/load-balancers", action, rest)
	case "volumes":
		return listCommand("/volumes", action, rest)
	case "filesystems":
		return listCommand("/filesystems", action, rest)
	case "objects":
		return listCommand("/objects", action, rest)
	case "vector-stores":
		return listCommand("/vector-stores", action, rest)
	case "encryption-keys":
		return listCommand("/encryption/keys", action, rest)
	case "k8s-clusters":
		return listCommand("/k8s-clusters", action, rest)
	case "secrets":
		return listCommand("/secrets", action, rest)
	case "registry-projects":
		return listCommand("/registry/projects", action, rest)
	case "observability-alert-rules":
		return listCommand("/observability/alert-rules", action, rest)
	case "observability-query":
		return observabilityQueryCommand(action, rest)
	case "metering-usage":
		if action != "get" {
			return command{}, fmt.Errorf("unsupported action for metering-usage: %s", action)
		}
		return command{Method: http.MethodGet, Path: "/metering/usage", Query: url.Values{}}, nil
	default:
		return command{}, fmt.Errorf("unsupported Core resource: %s", resource)
	}
}

func listCommand(path string, action string, args []string) (command, error) {
	if action != "list" {
		return command{}, fmt.Errorf("unsupported action for %s: %s", strings.TrimPrefix(path, "/"), action)
	}
	flags := flag.NewFlagSet(path, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	limit := flags.String("limit", "", "page size")
	cursor := flags.String("cursor", "", "cursor")
	if err := flags.Parse(args); err != nil {
		return command{}, err
	}
	query := url.Values{}
	if *limit != "" {
		query.Set("limit", *limit)
	}
	if *cursor != "" {
		query.Set("cursor", *cursor)
	}
	return command{Method: http.MethodGet, Path: path, Query: query}, nil
}

func observabilityQueryCommand(action string, args []string) (command, error) {
	if action != "get" {
		return command{}, fmt.Errorf("unsupported action for observability-query: %s", action)
	}
	flags := flag.NewFlagSet("observability-query", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	queryText := flags.String("query", "", "PromQL query")
	tenantID := flags.String("tenant-id", "", "tenant id")
	if err := flags.Parse(args); err != nil {
		return command{}, err
	}
	if strings.TrimSpace(*queryText) == "" {
		return command{}, errors.New("observability-query requires --query")
	}
	query := url.Values{}
	query.Set("query", *queryText)
	if *tenantID != "" {
		query.Set("tenant_id", *tenantID)
	}
	return command{Method: http.MethodGet, Path: "/observability/query", Query: query}, nil
}

func execute(ctx context.Context, client *http.Client, baseURL string, token string, cmd command) ([]byte, error) {
	if client == nil {
		client = http.DefaultClient
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	requestURL := baseURL + cmd.Path
	if encoded := cmd.Query.Encode(); encoded != "" {
		requestURL += "?" + encoded
	}
	var body io.Reader
	if cmd.Body != "" {
		body = strings.NewReader(cmd.Body)
	}
	req, err := http.NewRequestWithContext(ctx, cmd.Method, requestURL, body)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if cmd.Body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	responseBody, err := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("core API returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	return responseBody, nil
}

func envOrDefault(key string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
