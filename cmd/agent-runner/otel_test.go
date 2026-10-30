package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/sympozium-ai/sympozium/pkg/genaiattrs"
)

// spanAttrs indexes a recorded span's attributes by key.
func spanAttrs(s tracetest.SpanStub) map[string]attribute.Value {
	out := make(map[string]attribute.Value, len(s.Attributes))
	for _, a := range s.Attributes {
		out[string(a.Key)] = a.Value
	}
	return out
}

// spansNamed returns the recorded spans with the given name.
func spansNamed(spans tracetest.SpanStubs, name string) []tracetest.SpanStub {
	var out []tracetest.SpanStub
	for _, s := range spans {
		if s.Name == name {
			out = append(out, s)
		}
	}
	return out
}

// TestOTelSpanHierarchy verifies that the agent-runner produces the expected
// OTel span hierarchy when calling LLMs via mock servers.
// Uses a single TracerProvider to avoid issues with Go OTel's one-shot
// delegate resolution for package-level tracers.
func TestOTelSpanHierarchy(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
	)
	defer tp.Shutdown(context.Background())

	// Install test TracerProvider globally so agentTracer picks it up.
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	t.Run("simple_end_turn", func(t *testing.T) {
		exporter.Reset()

		// Mock Anthropic server returning a simple end_turn response.
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"id": "msg_test", "type": "message", "role": "assistant",
				"model":       "claude-sonnet-4-20250514",
				"content":     []map[string]string{{"type": "text", "text": "Hello!"}},
				"stop_reason": "end_turn",
				"usage":       map[string]int{"input_tokens": 10, "output_tokens": 5},
			})
		})
		srv := httptest.NewServer(handler)
		defer srv.Close()

		ctx := context.Background()
		tracer := otel.Tracer("test")
		ctx, rootSpan := tracer.Start(ctx, "test-root")

		text, inTok, outTok, _, err := callAnthropic(ctx, "test-key", srv.URL, "claude-sonnet-4-20250514", "sys", "Say hello", nil, nil)
		if err != nil {
			t.Fatalf("callAnthropic error: %v", err)
		}
		if text != "Hello!" {
			t.Errorf("text = %q, want %q", text, "Hello!")
		}
		if inTok != 10 || outTok != 5 {
			t.Errorf("tokens = (%d, %d), want (10, 5)", inTok, outTok)
		}

		rootSpan.End()
		tp.ForceFlush(ctx)

		spans := exporter.GetSpans()
		if len(spans) < 2 {
			t.Fatalf("expected at least 2 spans (test-root + gen_ai.chat), got %d", len(spans))
		}

		// Find the gen_ai.chat span.
		var chatSpan *tracetest.SpanStub
		for i := range spans {
			if spans[i].Name == "gen_ai.chat" {
				chatSpan = &spans[i]
				break
			}
		}
		if chatSpan == nil {
			names := make([]string, len(spans))
			for i, s := range spans {
				names[i] = s.Name
			}
			t.Fatalf("gen_ai.chat span not found; got spans: %v", names)
		}

		// Verify the chat span is a child of the root span.
		if chatSpan.Parent.TraceID() != rootSpan.SpanContext().TraceID() {
			t.Errorf("gen_ai.chat span trace ID %s != root trace ID %s",
				chatSpan.Parent.TraceID(), rootSpan.SpanContext().TraceID())
		}

		// Verify GenAI semantic convention attributes.
		attrs := make(map[string]string)
		for _, a := range chatSpan.Attributes {
			attrs[string(a.Key)] = a.Value.Emit()
		}
		if v, ok := attrs["gen_ai.system"]; !ok || v != "anthropic" {
			t.Errorf("gen_ai.system = %q, want %q", v, "anthropic")
		}
		// gen_ai.provider.name is the current OTel convention, emitted alongside
		// the deprecated gen_ai.system for one back-compat cycle.
		if v, ok := attrs["gen_ai.provider.name"]; !ok || v != "anthropic" {
			t.Errorf("gen_ai.provider.name = %q, want %q", v, "anthropic")
		}
		if v, ok := attrs["gen_ai.request.model"]; !ok || v != "claude-sonnet-4-20250514" {
			t.Errorf("gen_ai.request.model = %q, want %q", v, "claude-sonnet-4-20250514")
		}
		// gen_ai.response.model reflects the model the backend reports serving
		// (from the mock Anthropic response body).
		if v, ok := attrs["gen_ai.response.model"]; !ok || v != "claude-sonnet-4-20250514" {
			t.Errorf("gen_ai.response.model = %q, want %q", v, "claude-sonnet-4-20250514")
		}
		// The convention types finish_reasons as a string array.
		fr, ok := spanAttrs(*chatSpan)["gen_ai.response.finish_reasons"]
		if !ok {
			t.Fatalf("gen_ai.response.finish_reasons missing from chat span")
		}
		if fr.Type() != attribute.STRINGSLICE {
			t.Errorf("gen_ai.response.finish_reasons type = %v, want STRINGSLICE", fr.Type())
		}
		if got := fr.AsStringSlice(); len(got) != 1 || got[0] != "end_turn" {
			t.Errorf("gen_ai.response.finish_reasons = %v, want [end_turn]", got)
		}
		// Prompt content is never recorded on chat spans, opted in or not.
		if _, ok := spanAttrs(*chatSpan)["gen_ai.system_instructions"]; ok {
			t.Errorf("gen_ai.system_instructions must not be set on gen_ai.chat spans")
		}
	})

	t.Run("response_model_only_when_reported", func(t *testing.T) {
		exporter.Reset()

		// Mock backend that omits the "model" field from its response.
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"id": "msg_nomodel", "type": "message", "role": "assistant",
				"content":     []map[string]string{{"type": "text", "text": "Hi"}},
				"stop_reason": "end_turn",
				"usage":       map[string]int{"input_tokens": 1, "output_tokens": 1},
			})
		})
		srv := httptest.NewServer(handler)
		defer srv.Close()

		ctx := context.Background()
		if _, _, _, _, err := callAnthropic(ctx, "key", srv.URL, "claude-sonnet-4-20250514", "sys", "Hi", nil, nil); err != nil {
			t.Fatalf("callAnthropic error: %v", err)
		}
		tp.ForceFlush(ctx)

		chats := spansNamed(exporter.GetSpans(), "gen_ai.chat")
		if len(chats) != 1 {
			t.Fatalf("expected 1 gen_ai.chat span, got %d", len(chats))
		}
		attrs := spanAttrs(chats[0])
		if v, ok := attrs["gen_ai.response.model"]; ok {
			t.Errorf("gen_ai.response.model = %q, want unset when the backend reports no model", v.AsString())
		}
		if v, ok := attrs["gen_ai.request.model"]; !ok || v.AsString() != "claude-sonnet-4-20250514" {
			t.Errorf("gen_ai.request.model = %v, want claude-sonnet-4-20250514", v.Emit())
		}
	})

	// gen_ai.system_instructions is prompt content: off by default, and when
	// opted in it lands exactly once on the run span, never on the per-round
	// gen_ai.chat spans.
	t.Run("system_instructions_opt_in_set_once", func(t *testing.T) {
		const systemPrompt = "You are a careful SRE assistant."

		// Two-round tool flow so there are multiple gen_ai.chat spans under the
		// run span.
		newHandler := func() http.Handler {
			callCount := 0
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				callCount++
				w.Header().Set("Content-Type", "application/json")
				if callCount == 1 {
					json.NewEncoder(w).Encode(map[string]any{
						"id": "msg_tool", "type": "message", "role": "assistant",
						"model": "claude-sonnet-4-20250514",
						"content": []map[string]any{
							{"type": "tool_use", "id": "toolu_02Y", "name": "read_file",
								"input": map[string]string{"path": "/tmp/otel-test-2.txt"}},
						},
						"stop_reason": "tool_use",
						"usage":       map[string]int{"input_tokens": 10, "output_tokens": 15},
					})
					return
				}
				json.NewEncoder(w).Encode(map[string]any{
					"id": "msg_final", "type": "message", "role": "assistant",
					"model":       "claude-sonnet-4-20250514",
					"content":     []map[string]any{{"type": "text", "text": "Done."}},
					"stop_reason": "end_turn",
					"usage":       map[string]int{"input_tokens": 20, "output_tokens": 5},
				})
			})
		}
		tools := []ToolDef{{
			Name:        "read_file",
			Description: "Read a file",
			Parameters: map[string]any{
				"properties": map[string]any{"path": map[string]any{"type": "string"}},
				"required":   []string{"path"},
			},
		}}

		// run mirrors main(): start the run span, record the system prompt once,
		// then drive the agent loop beneath it.
		run := func(t *testing.T) (runSpans, chatSpans []tracetest.SpanStub) {
			exporter.Reset()
			srv := httptest.NewServer(newHandler())
			defer srv.Close()

			ctx := context.Background()
			ctx, runSpan := obs.startRunSpan(ctx,
				attribute.String("instance", "test-agent"),
				genaiattrs.Agent("test-agent"),
				genaiattrs.Model("claude-sonnet-4-20250514"),
			)
			recordSystemInstructions(runSpan, systemPrompt)
			if _, _, _, _, err := callAnthropic(ctx, "key", srv.URL, "claude-sonnet-4-20250514", systemPrompt, "Read file", tools, nil); err != nil {
				t.Fatalf("callAnthropic error: %v", err)
			}
			runSpan.End()
			tp.ForceFlush(ctx)

			spans := exporter.GetSpans()
			runSpans = spansNamed(spans, "sympozium.agent.run")
			chatSpans = spansNamed(spans, "gen_ai.chat")
			if len(runSpans) != 1 {
				t.Fatalf("expected 1 sympozium.agent.run span, got %d", len(runSpans))
			}
			if len(chatSpans) < 2 {
				t.Fatalf("expected at least 2 gen_ai.chat spans, got %d", len(chatSpans))
			}
			return runSpans, chatSpans
		}

		assertNoChatContent := func(t *testing.T, chatSpans []tracetest.SpanStub) {
			for i, cs := range chatSpans {
				if _, ok := spanAttrs(cs)["gen_ai.system_instructions"]; ok {
					t.Errorf("gen_ai.chat span %d carries gen_ai.system_instructions; it must only be on the run span", i)
				}
			}
		}

		t.Run("default_off", func(t *testing.T) {
			t.Setenv(genaiattrs.CaptureMessageContentEnv, "")
			runSpans, chatSpans := run(t)
			if v, ok := spanAttrs(runSpans[0])["gen_ai.system_instructions"]; ok {
				t.Errorf("gen_ai.system_instructions = %q on run span without opt-in; want unset", v.AsString())
			}
			assertNoChatContent(t, chatSpans)
		})

		t.Run("explicit_false", func(t *testing.T) {
			t.Setenv(genaiattrs.CaptureMessageContentEnv, "false")
			runSpans, chatSpans := run(t)
			if _, ok := spanAttrs(runSpans[0])["gen_ai.system_instructions"]; ok {
				t.Errorf("gen_ai.system_instructions set on run span with opt-in=false; want unset")
			}
			assertNoChatContent(t, chatSpans)
		})

		t.Run("opted_in", func(t *testing.T) {
			t.Setenv(genaiattrs.CaptureMessageContentEnv, "true")
			runSpans, chatSpans := run(t)
			attrs := spanAttrs(runSpans[0])
			if v, ok := attrs["gen_ai.system_instructions"]; !ok || v.AsString() != systemPrompt {
				t.Errorf("gen_ai.system_instructions = %q, want %q", v.AsString(), systemPrompt)
			}
			// Set once: the SDK deduplicates keys, so count raw attributes too.
			n := 0
			for _, a := range runSpans[0].Attributes {
				if string(a.Key) == "gen_ai.system_instructions" {
					n++
				}
			}
			if n != 1 {
				t.Errorf("gen_ai.system_instructions appears %d times on run span, want 1", n)
			}
			if v, ok := attrs["gen_ai.agent.name"]; !ok || v.AsString() != "test-agent" {
				t.Errorf("gen_ai.agent.name = %v, want test-agent", v.Emit())
			}
			assertNoChatContent(t, chatSpans)
		})
	})

	t.Run("tool_use_flow", func(t *testing.T) {
		exporter.Reset()

		callCount := 0
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount++
			w.Header().Set("Content-Type", "application/json")

			if callCount == 1 {
				json.NewEncoder(w).Encode(map[string]any{
					"id": "msg_tool", "type": "message", "role": "assistant",
					"model": "claude-sonnet-4-20250514",
					"content": []map[string]any{
						{"type": "tool_use", "id": "toolu_01X", "name": "read_file",
							"input": map[string]string{"path": "/tmp/otel-test.txt"}},
					},
					"stop_reason": "tool_use",
					"usage":       map[string]int{"input_tokens": 10, "output_tokens": 15},
				})
				return
			}

			json.NewEncoder(w).Encode(map[string]any{
				"id": "msg_final", "type": "message", "role": "assistant",
				"model":       "claude-sonnet-4-20250514",
				"content":     []map[string]any{{"type": "text", "text": "Done."}},
				"stop_reason": "end_turn",
				"usage":       map[string]int{"input_tokens": 20, "output_tokens": 5},
			})
		})
		srv := httptest.NewServer(handler)
		defer srv.Close()

		tools := []ToolDef{
			{
				Name:        "read_file",
				Description: "Read a file",
				Parameters: map[string]any{
					"properties": map[string]any{
						"path": map[string]any{"type": "string"},
					},
					"required": []string{"path"},
				},
			},
		}

		ctx := context.Background()
		tracer := otel.Tracer("test")
		ctx, rootSpan := tracer.Start(ctx, "test-root")

		text, _, _, toolCalls, err := callAnthropic(ctx, "key", srv.URL, "claude-sonnet-4-20250514", "sys", "Read file", tools, nil)
		if err != nil {
			t.Fatalf("callAnthropic error: %v", err)
		}
		if text != "Done." {
			t.Errorf("text = %q, want %q", text, "Done.")
		}
		if toolCalls != 1 {
			t.Errorf("toolCalls = %d, want 1", toolCalls)
		}

		rootSpan.End()
		tp.ForceFlush(ctx)

		spans := exporter.GetSpans()

		// Collect span names.
		spanNames := map[string]int{}
		for _, s := range spans {
			spanNames[s.Name]++
		}

		if spanNames["gen_ai.chat"] < 2 {
			t.Errorf("expected at least 2 gen_ai.chat spans, got %d; all spans: %v",
				spanNames["gen_ai.chat"], spanNames)
		}
		if spanNames["gen_ai.execute_tool"] < 1 {
			t.Errorf("expected at least 1 gen_ai.execute_tool span, got %d; all spans: %v",
				spanNames["gen_ai.execute_tool"], spanNames)
		}

		// All spans should share the same trace ID.
		rootTraceID := rootSpan.SpanContext().TraceID()
		for _, s := range spans {
			if s.SpanContext.TraceID() != rootTraceID {
				t.Errorf("span %q has trace ID %s, want %s",
					s.Name, s.SpanContext.TraceID(), rootTraceID)
			}
		}
	})
}
