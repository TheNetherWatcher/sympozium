package genaiattrs

import (
	"reflect"
	"testing"

	"go.opentelemetry.io/otel/attribute"
)

func TestStringBuilders(t *testing.T) {
	cases := []struct {
		name string
		kv   attribute.KeyValue
		key  string
		want string
	}{
		{"Provider", Provider("anthropic"), ProviderNameKey, "anthropic"},
		{"System", System("anthropic"), SystemKey, "anthropic"},
		{"Model", Model("claude-sonnet-4"), RequestModelKey, "claude-sonnet-4"},
		{"ResponseModel", ResponseModel("claude-sonnet-4-20250514"), ResponseModelKey, "claude-sonnet-4-20250514"},
		{"Agent", Agent("sre-bot"), AgentNameKey, "sre-bot"},
		{"SystemInstructions", SystemInstructions("You are helpful."), SystemInstructionsKey, "You are helpful."},
		{"ToolName", ToolName("read_file"), ToolNameKey, "read_file"},
		{"ToolCallID", ToolCallID("toolu_01X"), ToolCallIDKey, "toolu_01X"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if string(tc.kv.Key) != tc.key {
				t.Fatalf("key = %q, want %q", tc.kv.Key, tc.key)
			}
			if tc.kv.Value.Type() != attribute.STRING {
				t.Fatalf("type = %v, want STRING", tc.kv.Value.Type())
			}
			if got := tc.kv.Value.AsString(); got != tc.want {
				t.Fatalf("value = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIntBuilders(t *testing.T) {
	cases := []struct {
		name string
		kv   attribute.KeyValue
		key  string
		want int64
	}{
		{"InputTokens", InputTokens(42), InputTokensKey, 42},
		{"OutputTokens", OutputTokens(7), OutputTokensKey, 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if string(tc.kv.Key) != tc.key {
				t.Fatalf("key = %q, want %q", tc.kv.Key, tc.key)
			}
			if tc.kv.Value.Type() != attribute.INT64 {
				t.Fatalf("type = %v, want INT64", tc.kv.Value.Type())
			}
			if got := tc.kv.Value.AsInt64(); got != tc.want {
				t.Fatalf("value = %d, want %d", got, tc.want)
			}
		})
	}
}

// The convention types finish_reasons as a string array, so a single reason
// must still be recorded as a one-element slice rather than a plain string.
func TestFinishReasonsIsStringSlice(t *testing.T) {
	kv := FinishReasons("end_turn")
	if string(kv.Key) != FinishReasonsKey {
		t.Fatalf("key = %q, want %q", kv.Key, FinishReasonsKey)
	}
	if kv.Value.Type() != attribute.STRINGSLICE {
		t.Fatalf("type = %v, want STRINGSLICE", kv.Value.Type())
	}
	if got := kv.Value.AsStringSlice(); !reflect.DeepEqual(got, []string{"end_turn"}) {
		t.Fatalf("value = %v, want [end_turn]", got)
	}

	multi := FinishReasons("stop", "length")
	if got := multi.Value.AsStringSlice(); !reflect.DeepEqual(got, []string{"stop", "length"}) {
		t.Fatalf("value = %v, want [stop length]", got)
	}
}

func TestCaptureMessageContent(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"false", false},
		{"0", false},
		{"1", false},
		{"yes", false},
		{"true", true},
		{"TRUE", true},
		{" true ", true},
	}
	for _, tc := range cases {
		t.Run("value="+tc.value, func(t *testing.T) {
			t.Setenv(CaptureMessageContentEnv, tc.value)
			if got := CaptureMessageContent(); got != tc.want {
				t.Fatalf("CaptureMessageContent() with %q = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}
