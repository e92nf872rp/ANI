package main

import (
	"net/http"
	"testing"
	"time"
)

func TestGatewayInstanceWorkloadRuntimeDefaultsToLocal(t *testing.T) {
	runtime, err := newGatewayInstanceWorkloadRuntime(gatewayWorkloadRuntimeConfig{})
	if err != nil {
		t.Fatalf("newGatewayInstanceWorkloadRuntime() error = %v", err)
	}
	if runtime.Provider != "local" {
		t.Fatalf("provider = %q, want local", runtime.Provider)
	}
}

func TestGatewayInstanceWorkloadRuntimeUsesKubernetesRESTProvider(t *testing.T) {
	t.Setenv("KUBERNETES_CONFIG_AUTO_LOAD", "false")
	t.Setenv("KUBERNETES_API_HOST", "https://kubernetes.example.test")

	runtime, err := newGatewayInstanceWorkloadRuntime(gatewayWorkloadRuntimeConfig{
		ProviderMode:             "kubernetes_rest",
		ProviderApplyEnabled:     true,
		LifecycleProvider:        "kubernetes_rest",
		LifecycleApplyEnabled:    true,
		OpsProvider:              "kubernetes_rest",
		OpsEnabled:               true,
		KubernetesHTTPClient:     &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil })},
		KubernetesRequestTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("newGatewayInstanceWorkloadRuntime() error = %v", err)
	}
	if runtime.Provider != "kubernetes_rest" {
		t.Fatalf("provider = %q, want kubernetes_rest", runtime.Provider)
	}
	if runtime.DryRun == nil || runtime.Apply == nil || runtime.StatusReader == nil {
		t.Fatal("kubernetes workload runtime missing provider adapters")
	}
	if runtime.Lifecycle == nil || runtime.Ops == nil {
		t.Fatal("kubernetes workload runtime missing lifecycle or ops adapters")
	}
}
