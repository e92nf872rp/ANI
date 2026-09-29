package router

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	inferencecontrolv1 "github.com/kubercloud/ani/pkg/generated/pb/inference/control/v1"
)

const (
	maxInferenceEngineEnvItems       = 32
	maxInferenceEngineCommandItems   = 64
	maxInferenceEngineEnvNameLen     = 64
	maxInferenceEngineEnvValueLen    = 4096
	maxInferenceEngineCommandLen     = 4096
	maxInferenceEngineCommandTextLen = 262144
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
	command, err := parseInferenceEngineCommand(raw.Command)
	if err != nil {
		return nil, err
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
	for _, part := range command {
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

func parseInferenceEngineCommand(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	raw = bytes.TrimSpace(raw)
	if bytes.Equal(raw, []byte("null")) {
		return nil, fmt.Errorf("engine.command must be a string or array")
	}
	if raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, fmt.Errorf("engine.command string is invalid: %w", err)
		}
		return parseInferenceEngineCommandText(text)
	}
	if raw[0] != '[' {
		return nil, fmt.Errorf("engine.command must be a string or array")
	}
	var command []string
	if err := json.Unmarshal(raw, &command); err != nil {
		return nil, fmt.Errorf("engine.command array is invalid: %w", err)
	}
	if len(command) == 0 {
		return nil, fmt.Errorf("engine.command must not be empty")
	}
	if len(command) > maxInferenceEngineCommandItems {
		return nil, fmt.Errorf("engine.command exceeds %d items", maxInferenceEngineCommandItems)
	}
	return command, nil
}

func parseInferenceEngineCommandText(text string) ([]string, error) {
	if len(text) == 0 {
		return nil, fmt.Errorf("engine.command must not be empty")
	}
	if len(text) > maxInferenceEngineCommandTextLen {
		return nil, fmt.Errorf("engine.command text exceeds %d bytes", maxInferenceEngineCommandTextLen)
	}
	var command []string
	var token strings.Builder
	quote := rune(0)
	started := false
	flush := func() error {
		if !started {
			return nil
		}
		value := token.String()
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("engine.command contains an empty argument")
		}
		if len(value) > maxInferenceEngineCommandLen {
			return fmt.Errorf("engine.command item exceeds %d bytes", maxInferenceEngineCommandLen)
		}
		command = append(command, value)
		if len(command) > maxInferenceEngineCommandItems {
			return fmt.Errorf("engine.command exceeds %d items", maxInferenceEngineCommandItems)
		}
		token.Reset()
		started = false
		return nil
	}
	appendRune := func(value rune) error {
		token.WriteRune(value)
		if token.Len() > maxInferenceEngineCommandLen {
			return fmt.Errorf("engine.command item exceeds %d bytes", maxInferenceEngineCommandLen)
		}
		return nil
	}
	nextRune := func(index int) (rune, int, error) {
		if index >= len(text) {
			return 0, 0, fmt.Errorf("engine.command has a trailing escape")
		}
		value, size := utf8.DecodeRuneInString(text[index:])
		if value == utf8.RuneError && size == 1 {
			return 0, 0, fmt.Errorf("engine.command contains invalid UTF-8")
		}
		return value, size, nil
	}

	for index := 0; index < len(text); {
		value, size := utf8.DecodeRuneInString(text[index:])
		if value == utf8.RuneError && size == 1 {
			return nil, fmt.Errorf("engine.command contains invalid UTF-8")
		}
		index += size
		if value == 0 {
			return nil, fmt.Errorf("engine.command contains NUL")
		}
		if quote == '\'' {
			if value == '\'' {
				quote = 0
				continue
			}
			started = true
			if err := appendRune(value); err != nil {
				return nil, err
			}
			continue
		}
		if quote == '"' {
			switch value {
			case '"':
				quote = 0
			case '\\':
				next, nextSize, err := nextRune(index)
				if err != nil {
					return nil, err
				}
				index += nextSize
				switch next {
				case '"', '\\', '$', '`':
					if err := appendRune(next); err != nil {
						return nil, err
					}
				case '\n':
					// Backslash-newline is a line continuation.
				case '\r':
					if index < len(text) && text[index] == '\n' {
						index++
					}
				default:
					if err := appendRune('\\'); err != nil {
						return nil, err
					}
					if err := appendRune(next); err != nil {
						return nil, err
					}
				}
			case '$', '`', '\n', '\r':
				return nil, fmt.Errorf("engine.command contains unsupported shell syntax")
			default:
				started = true
				if err := appendRune(value); err != nil {
					return nil, err
				}
			}
			continue
		}

		switch value {
		case ' ', '\t':
			if err := flush(); err != nil {
				return nil, err
			}
		case '\n', '\r':
			return nil, fmt.Errorf("engine.command contains unsupported shell syntax")
		case '\'', '"':
			quote = value
			started = true
		case '\\':
			next, nextSize, err := nextRune(index)
			if err != nil {
				return nil, err
			}
			index += nextSize
			if next == '\n' {
				continue
			}
			if next == '\r' {
				if index < len(text) && text[index] == '\n' {
					index++
				}
				continue
			}
			started = true
			if err := appendRune(next); err != nil {
				return nil, err
			}
		case ';', '&', '|', '<', '>', '(', ')', '$', '`':
			return nil, fmt.Errorf("engine.command contains unsupported shell syntax")
		default:
			started = true
			if err := appendRune(value); err != nil {
				return nil, err
			}
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("engine.command has an unterminated quote")
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if len(command) == 0 {
		return nil, fmt.Errorf("engine.command must not be empty")
	}
	return command, nil
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
