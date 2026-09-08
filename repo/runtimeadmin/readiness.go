package runtimeadmin

import (
	"context"
	"errors"
	"time"
)

var errNotServing = errors.New("service is not serving")

type readinessResult struct {
	Status string
	Checks map[string]readinessCheckState
}

type readinessCheckState struct {
	Status    string `json:"status"`
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
}

type completedCheck struct {
	name        string
	criticality Criticality
	state       readinessCheckState
}

func (runtime *Runtime) evaluateReadiness(requestContext context.Context) readinessResult {
	startedAt := time.Now()
	ctx, cancel := context.WithTimeout(requestContext, runtime.overallTimeout)
	defer cancel()

	checks := make([]Check, 0, len(runtime.checks)+1)
	checks = append(checks, Check{
		Name:        servingCheckName,
		Criticality: Strong,
		Probe: func(context.Context) error {
			if runtime.serving.Load() {
				return nil
			}
			return errNotServing
		},
	})
	checks = append(checks, runtime.checks...)

	completed := make(chan completedCheck, len(checks))
	pending := make(map[string]Criticality, len(checks))
	for _, check := range checks {
		pending[check.Name] = check.Criticality
		go runtime.runCheck(ctx, check, completed)
	}

	result := readinessResult{Status: "ok", Checks: make(map[string]readinessCheckState, len(checks))}
	for len(pending) > 0 {
		select {
		case item := <-completed:
			if _, exists := pending[item.name]; !exists {
				continue
			}
			delete(pending, item.name)
			result.Checks[item.name] = item.state
			applyReadinessStatus(&result, item.criticality, item.state.Status)
		case <-ctx.Done():
			for name, criticality := range pending {
				result.Checks[name] = readinessCheckState{
					Status:    "fail",
					LatencyMS: elapsedMilliseconds(startedAt),
					Error:     sanitizeError(ctx.Err()),
				}
				applyReadinessStatus(&result, criticality, "fail")
			}
			pending = nil
		}
	}
	runtime.updateSummary(result.Status, time.Now().UTC())
	return result
}

func (runtime *Runtime) runCheck(parent context.Context, check Check, completed chan<- completedCheck) {
	startedAt := time.Now()
	ctx, cancel := context.WithTimeout(parent, runtime.checkTimeout)
	defer cancel()
	err := check.Probe(ctx)
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	state := readinessCheckState{Status: "ok", LatencyMS: elapsedMilliseconds(startedAt)}
	if err != nil {
		state.Status = "fail"
		state.Error = sanitizeError(err)
		if !errors.Is(err, errNotServing) {
			runtime.logger.ErrorContext(parent, "runtime readiness check failed", "check", check.Name, "error", state.Error)
		}
	}
	completed <- completedCheck{name: check.Name, criticality: check.Criticality, state: state}
}

func applyReadinessStatus(result *readinessResult, criticality Criticality, checkStatus string) {
	if checkStatus == "ok" {
		return
	}
	if criticality == Strong {
		result.Status = "error"
		return
	}
	if result.Status == "ok" {
		result.Status = "degraded"
	}
}

func elapsedMilliseconds(startedAt time.Time) int64 {
	return time.Since(startedAt).Milliseconds()
}
