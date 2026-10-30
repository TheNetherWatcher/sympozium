// Package genaiattrs provides the OpenTelemetry GenAI semantic convention
// attribute keys and builders shared by Sympozium's instrumentation (the
// agent-runner and the controller's channel router), so every component emits
// the same gen_ai.* attribute names.
//
// Backward compatibility: existing sympozium.* attributes are kept alongside
// the gen_ai.* attributes, and the deprecated gen_ai.system is emitted next to
// its replacement gen_ai.provider.name for one release cycle.
//
// Content capture: raw prompt and completion content (gen_ai.input.messages,
// gen_ai.output.messages, gen_ai.system_instructions) is opt-in under the
// conventions because it carries PII and large payloads. Only
// gen_ai.system_instructions is implemented here, and callers must check
// CaptureMessageContent before recording it; the message attributes are not
// emitted at all.
package genaiattrs

import (
	"os"
	"strings"

	"go.opentelemetry.io/otel/attribute"
)

// GenAI semantic convention attribute keys.
const (
	// ProviderNameKey is the current convention for the backend provider.
	// SystemKey is its deprecated predecessor, kept for one release cycle so
	// existing dashboards keyed on it keep working.
	ProviderNameKey = "gen_ai.provider.name"
	SystemKey       = "gen_ai.system"

	// Models
	RequestModelKey  = "gen_ai.request.model"
	ResponseModelKey = "gen_ai.response.model"

	// Token usage
	InputTokensKey  = "gen_ai.usage.input_tokens"
	OutputTokensKey = "gen_ai.usage.output_tokens"

	// Response
	FinishReasonsKey = "gen_ai.response.finish_reasons"

	// System / agent
	SystemInstructionsKey = "gen_ai.system_instructions"
	AgentNameKey          = "gen_ai.agent.name"

	// Tool call
	ToolNameKey   = "gen_ai.tool.name"
	ToolCallIDKey = "gen_ai.tool.call.id"
)

// CaptureMessageContentEnv is the standard OTel GenAI opt-in for recording
// prompt and completion content on spans. Anything that is content rather than
// metadata (today: gen_ai.system_instructions) is recorded only when it is set
// to "true".
const CaptureMessageContentEnv = "OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT"

// CaptureMessageContent reports whether message content may be recorded on
// spans. It is false unless OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT
// is exactly "true" (case-insensitive, surrounding whitespace ignored).
func CaptureMessageContent() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(CaptureMessageContentEnv)), "true")
}

// Provider returns gen_ai.provider.name.
func Provider(name string) attribute.KeyValue {
	return attribute.String(ProviderNameKey, name)
}

// System returns the deprecated gen_ai.system attribute. Emit it alongside
// Provider for one release cycle so existing consumers keep working.
func System(name string) attribute.KeyValue {
	return attribute.String(SystemKey, name)
}

// Model returns gen_ai.request.model.
func Model(model string) attribute.KeyValue {
	return attribute.String(RequestModelKey, model)
}

// ResponseModel returns gen_ai.response.model. Only set it when the backend
// actually reports the model it served; do not fall back to the requested
// model, which is already recorded as gen_ai.request.model.
func ResponseModel(model string) attribute.KeyValue {
	return attribute.String(ResponseModelKey, model)
}

// InputTokens returns gen_ai.usage.input_tokens.
func InputTokens(n int) attribute.KeyValue {
	return attribute.Int(InputTokensKey, n)
}

// OutputTokens returns gen_ai.usage.output_tokens.
func OutputTokens(n int) attribute.KeyValue {
	return attribute.Int(OutputTokensKey, n)
}

// FinishReasons returns gen_ai.response.finish_reasons. The convention types
// it as a string array (one entry per choice), so even a single reason is
// recorded as a slice.
func FinishReasons(reasons ...string) attribute.KeyValue {
	return attribute.StringSlice(FinishReasonsKey, reasons)
}

// Agent returns gen_ai.agent.name.
func Agent(name string) attribute.KeyValue {
	return attribute.String(AgentNameKey, name)
}

// SystemInstructions returns gen_ai.system_instructions. This is prompt
// content: callers must only record it when CaptureMessageContent is true.
func SystemInstructions(text string) attribute.KeyValue {
	return attribute.String(SystemInstructionsKey, text)
}

// ToolName returns gen_ai.tool.name.
func ToolName(name string) attribute.KeyValue {
	return attribute.String(ToolNameKey, name)
}

// ToolCallID returns gen_ai.tool.call.id.
func ToolCallID(id string) attribute.KeyValue {
	return attribute.String(ToolCallIDKey, id)
}
