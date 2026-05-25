package config

import "testing"

func TestLoadDefaultsToDedicatedReconcileWorker(t *testing.T) {
	cfg := Load()

	if cfg.ServiceName != "reconcile-worker" {
		t.Fatalf("ServiceName = %q, want reconcile-worker", cfg.ServiceName)
	}
	if cfg.HealthPort != 9205 {
		t.Fatalf("HealthPort = %d, want 9205", cfg.HealthPort)
	}
	if !cfg.WorkloadReconcileControllerEnabled {
		t.Fatalf("WorkloadReconcileControllerEnabled = false, want true")
	}
}
