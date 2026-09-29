package router

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseInferenceEngineCommandText(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "quoted argument and escaped space",
			text: `python3 -m vllm --model "/models/qwen model" --served-model-name qwen\ 3.5`,
			want: []string{"python3", "-m", "vllm", "--model", "/models/qwen model", "--served-model-name", "qwen 3.5"},
		},
		{
			name: "adjacent quoted fragments",
			text: `echo pre"mid"'post'`,
			want: []string{"echo", "premidpost"},
		},
		{
			name: "double quote preserves unsupported escapes",
			text: `echo "C:\models\qwen"`,
			want: []string{"echo", `C:\models\qwen`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseInferenceEngineCommandText(tt.text)
			if err != nil {
				t.Fatalf("parse error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("argv = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestParseInferenceEngineCommandTextRejectsShellSyntax(t *testing.T) {
	for _, text := range []string{
		"python3 --model qwen && echo bad",
		"python3 --model qwen | cat",
		"python3 --model $MODEL",
		"python3 --model `echo qwen`",
		"python3 --model 'unterminated",
		"python3 --model qwen\\",
		"python3 --model \" \"",
	} {
		if _, err := parseInferenceEngineCommandText(text); err == nil {
			t.Fatalf("parse(%q) succeeded, want error", text)
		}
	}
}

func TestParseInferenceEngineCommandTextRejectsLimits(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{name: "too many arguments", text: "python3 " + strings.Repeat("x ", maxInferenceEngineCommandItems)},
		{name: "argument too long", text: "python3 " + strings.Repeat("x", maxInferenceEngineCommandLen+1)},
		{name: "text too long", text: strings.Repeat("x", maxInferenceEngineCommandTextLen+1)},
		{name: "whitespace only", text: " \t "},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseInferenceEngineCommandText(tt.text); err == nil {
				t.Fatalf("parse(%q) succeeded, want error", tt.text)
			}
		})
	}
}

func TestParseInferenceEngineCommandAcceptsLegacyArgv(t *testing.T) {
	got, err := parseInferenceEngineCommand([]byte("[\"python3\",\"-m\",\"vllm\",\"--dtype\",\"auto\"]"))
	if err != nil {
		t.Fatalf("parse error = %v", err)
	}
	want := []string{"python3", "-m", "vllm", "--dtype", "auto"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %#v, want %#v", got, want)
	}
}
