package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestAgentRunTracePairsToolUseAndResult(t *testing.T) {
	sr, cleanup := installSpanRecorder(t)
	defer cleanup()

	tr := startRunSpan(context.Background(), "claude", ExecOptions{
		RunID:       "task-1",
		WorkspaceID: "ws-1",
		AgentName:   "coding-agent",
	})
	tr.RecordToolUse("Read File", "call-1")
	tr.RecordToolResult("call-1", 42)
	tr.Finish("completed", "")

	stubs := tracetest.SpanStubsFromReadOnlySpans(sr.Ended())
	tool := findSpan(t, stubs, "tool.Read_File")
	if got := attrString(tool, "tool.call_id"); got != "call-1" {
		t.Fatalf("tool.call_id = %q, want call-1", got)
	}
	if got := attrInt(tool, "tool.output_length"); got != 42 {
		t.Fatalf("tool.output_length = %d, want 42", got)
	}
	if tool.Status.Code != 0 {
		t.Fatalf("tool span status = %v, want unset", tool.Status)
	}
}

func TestAgentRunTraceClosesUnpairedToolUse(t *testing.T) {
	sr, cleanup := installSpanRecorder(t)
	defer cleanup()

	tr := startRunSpan(context.Background(), "claude", ExecOptions{})
	tr.RecordToolUse("Bash", "call-2")
	tr.Finish("failed", "boom")

	stubs := tracetest.SpanStubsFromReadOnlySpans(sr.Ended())
	tool := findSpan(t, stubs, "tool.Bash")
	if tool.Status.Code.String() != "Error" {
		t.Fatalf("tool span status = %v, want error", tool.Status)
	}
	if got := attrString(tool, "tool.call_id"); got != "call-2" {
		t.Fatalf("tool.call_id = %q, want call-2", got)
	}
	run := findSpan(t, stubs, "agent.run")
	if run.Status.Code.String() != "Error" {
		t.Fatalf("run span status = %v, want error", run.Status)
	}
}

func TestAgentRunTraceRecordsUnpairedToolResult(t *testing.T) {
	sr, cleanup := installSpanRecorder(t)
	defer cleanup()

	tr := startRunSpan(context.Background(), "claude", ExecOptions{})
	tr.RecordToolResult("missing-call", 7)
	tr.Finish("completed", "")

	stubs := tracetest.SpanStubsFromReadOnlySpans(sr.Ended())
	tool := findSpan(t, stubs, "tool.unknown")
	if tool.Status.Code.String() != "Error" {
		t.Fatalf("tool span status = %v, want error", tool.Status)
	}
}

func TestAgentRunTraceRecordsTokenMetrics(t *testing.T) {
	_, cleanup := installSpanRecorder(t)
	defer cleanup()
	ResetAgentTokenMetricsForTest()

	tr := startRunSpan(context.Background(), "claude", ExecOptions{
		WorkspaceID: "ws-1",
		AgentName:   "coding-agent",
	})
	tr.RecordAssistantTurn("claude-sonnet", TokenUsage{
		InputTokens:      10,
		OutputTokens:     4,
		CacheReadTokens:  3,
		CacheWriteTokens: 2,
	}, "end_turn")
	tr.Finish("completed", "")

	reg := prometheus.NewRegistry()
	reg.MustRegister(AgentTokenMetrics())
	if err := testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP multica_agent_tokens_total Total LLM tokens consumed by Multica agent runs.
# TYPE multica_agent_tokens_total counter
multica_agent_tokens_total{agent="coding-agent",kind="cache_read",model="claude-sonnet",workspace="ws-1"} 3
multica_agent_tokens_total{agent="coding-agent",kind="cache_write",model="claude-sonnet",workspace="ws-1"} 2
multica_agent_tokens_total{agent="coding-agent",kind="input",model="claude-sonnet",workspace="ws-1"} 10
multica_agent_tokens_total{agent="coding-agent",kind="output",model="claude-sonnet",workspace="ws-1"} 4
`), "multica_agent_tokens_total"); err != nil {
		t.Fatal(err)
	}
}

func installSpanRecorder(t *testing.T) (*tracetest.SpanRecorder, func()) {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(sr),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	return sr, func() {
		_ = tp.Shutdown(context.Background())
		if old == nil {
			otel.SetTracerProvider(noop.NewTracerProvider())
			return
		}
		otel.SetTracerProvider(old)
	}
}

func findSpan(t *testing.T, spans tracetest.SpanStubs, name string) tracetest.SpanStub {
	t.Helper()
	for _, span := range spans {
		if span.Name == name {
			return span
		}
	}
	t.Fatalf("span %q not found in %+v", name, spans)
	return tracetest.SpanStub{}
}

func attrString(span tracetest.SpanStub, key string) string {
	for _, attr := range span.Attributes {
		if string(attr.Key) == key {
			return attr.Value.AsString()
		}
	}
	return ""
}

func attrInt(span tracetest.SpanStub, key string) int64 {
	for _, attr := range span.Attributes {
		if string(attr.Key) == key {
			return attr.Value.AsInt64()
		}
	}
	return 0
}
