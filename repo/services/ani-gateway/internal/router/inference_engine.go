package router

import (
	"fmt"
	"regexp"
	"strings"

	inferencecontrolv1 "github.com/kubercloud/ani/pkg/generated/pb/inference/control/v1"
)

const (
	maxInferenceEngineEnvItems     = 32
	maxInferenceEngineCommandItems = 64
	maxInferenceEngineEnvNameLen   = 64
	maxInferenceEngineEnvValueLen  = 4096
	maxInferenceEngineCommandLen   = 4096
)

var posixInferenceEngineEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var reservedInferenceEngineEnvNames = map[string]struct{}{
	"CUDA_VISIBLE_DEVICES":       {},
	"NVIDIA_VISIBLE_DEVICES":     {},
	"NVIDIA_DRIVER_CAPABILITIES": {},
	"PYTHONPATH":                 {},
	"PATH":                       {},
	"LD_PRELOAD":                 {},
	"LD_LIBRARY_PATH":            {},
	"RAY_EXPERIMENTAL_NOSET_CUDA_VISIBLE_DEVICES": {},
}

func protoEngineFromJSON(raw *inferenceServiceEngineJSON) (*inferencecontrolv1.InferenceServiceEngine, error) {
	if raw == nil {
		return nil, nil
	}
	if len(raw.Env) > maxInferenceEngineEnvItems {
		return nil, fmt.Errorf("engine.env exceeds %d items", maxInferenceEngineEnvItems)
	}
	if raw.Command != nil && len(raw.Command) == 0 {
		return nil, fmt.Errorf("engine.command must not be empty")
	}
	if len(raw.Command) > maxInferenceEngineCommandItems {
		return nil, fmt.Errorf("engine.command exceeds %d items", maxInferenceEngineCommandItems)
	}
	msg := &inferencecontrolv1.InferenceServiceEngine{}
	seen := map[string]struct{}{}
	for _, item := range raw.Env {
		name := strings.TrimSpace(item.Name)
		if name == "" || !posixInferenceEngineEnvName.MatchString(name) || len(name) > maxInferenceEngineEnvNameLen {
			return nil, fmt.Errorf("engine.env name is invalid")
		}
		if strings.TrimSpace(item.Value) == "" || len(item.Value) > maxInferenceEngineEnvValueLen {
			return nil, fmt.Errorf("engine.env value is invalid")
		}
		if _, reserved := reservedInferenceEngineEnvNames[name]; reserved {
			return nil, fmt.Errorf("engine.env name %s is reserved", name)
		}
		if _, dup := seen[name]; dup {
			return nil, fmt.Errorf("engine.env name %s is duplicated", name)
		}
		seen[name] = struct{}{}
		msg.Env = append(msg.Env, &inferencecontrolv1.InferenceServiceEngineEnvVar{Name: name, Value: item.Value})
	}
	for _, part := range raw.Command {
		if strings.TrimSpace(part) == "" || len(part) > maxInferenceEngineCommandLen {
			return nil, fmt.Errorf("engine.command item is invalid")
		}
		msg.Command = append(msg.Command, part)
	}
	if len(msg.Env) == 0 && len(msg.Command) == 0 {
		return nil, nil
	}
	return msg, nil
}

func inferenceEngineJSON(msg *inferencecontrolv1.InferenceServiceEngine) map[string]any {
	if msg == nil {
		return nil
	}
	if len(msg.GetEnv()) == 0 && len(msg.GetCommand()) == 0 {
		return nil
	}
	env := make([]map[string]any, 0, len(msg.GetEnv()))
	for _, item := range msg.GetEnv() {
		if item == nil {
			continue
		}
		env = append(env, map[string]any{"name": item.GetName(), "value": item.GetValue()})
	}
	body := map[string]any{}
	if len(env) > 0 {
		body["env"] = env
	}
	if len(msg.GetCommand()) > 0 {
		body["command"] = append([]string(nil), msg.GetCommand()...)
	}
	if len(body) == 0 {
		return nil
	}
	return body
}
