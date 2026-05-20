package agent

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const unknownLabel = "unknown"

var agentTokenMetrics = newAgentTokenCollector()

// AgentTokenCollector exposes token usage reported by agent backends.
type AgentTokenCollector struct {
	desc *prometheus.Desc
	mu   sync.Mutex
	data map[agentTokenKey]uint64
}

type agentTokenKey struct {
	Model     string
	Agent     string
	Workspace string
	Kind      string
}

func newAgentTokenCollector() *AgentTokenCollector {
	return &AgentTokenCollector{
		desc: prometheus.NewDesc(
			"multica_agent_tokens_total",
			"Total LLM tokens consumed by Multica agent runs.",
			[]string{"model", "agent", "workspace", "kind"},
			nil,
		),
		data: make(map[agentTokenKey]uint64),
	}
}

func (c *AgentTokenCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.desc
}

func (c *AgentTokenCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, value := range c.data {
		ch <- prometheus.MustNewConstMetric(c.desc, prometheus.CounterValue, float64(value), key.Model, key.Agent, key.Workspace, key.Kind)
	}
}

func (c *AgentTokenCollector) add(model, agentName, workspaceID, kind string, tokens int64) {
	if tokens <= 0 {
		return
	}
	key := agentTokenKey{
		Model:     defaultTraceLabel(model),
		Agent:     defaultTraceLabel(agentName),
		Workspace: defaultTraceLabel(workspaceID),
		Kind:      kind,
	}
	c.mu.Lock()
	c.data[key] += uint64(tokens)
	c.mu.Unlock()
}

// AgentTokenMetrics returns the Prometheus collector for agent token usage.
func AgentTokenMetrics() prometheus.Collector {
	return agentTokenMetrics
}

// ResetAgentTokenMetricsForTest clears process-global agent token metrics.
func ResetAgentTokenMetricsForTest() {
	agentTokenMetrics = newAgentTokenCollector()
}

type agentRunTrace struct {
	ctx       context.Context
	span      trace.Span
	backend   string
	system    string
	opts      ExecOptions
	tokenSum  TokenUsage
	toolSpans map[string]trace.Span
	toolNames map[string]string
	mu        sync.Mutex
	ended     atomic.Bool
}

// StartRunSpan starts an agent.run span with the standard agent attributes.
func StartRunSpan(ctx context.Context, backend, model, runID, issueID, workspaceID string) (context.Context, trace.Span) {
	trace := startRunSpan(ctx, backend, ExecOptions{
		Model:       model,
		RunID:       runID,
		IssueID:     issueID,
		WorkspaceID: workspaceID,
	})
	return trace.Context(), trace.span
}

// StartTurnSpan starts an llm.turn span under ctx.
func StartTurnSpan(ctx context.Context) (context.Context, trace.Span) {
	return otel.Tracer("multica/agent").Start(ctx, "llm.turn")
}

// StartToolSpan starts a tool.<name> span under ctx.
func StartToolSpan(ctx context.Context, name, callID string) (context.Context, trace.Span) {
	name = sanitizeSpanName(name)
	return otel.Tracer("multica/agent").Start(ctx, "tool."+name, trace.WithAttributes(
		attribute.String("tool.name", name),
		attribute.String("tool.call_id", callID),
	))
}

func startRunSpan(ctx context.Context, backend string, opts ExecOptions) *agentRunTrace {
	system := genAISystemForBackend(backend)
	attrs := []attribute.KeyValue{
		attribute.String("agent.backend", backend),
		attribute.String("gen_ai.system", system),
	}
	if opts.Model != "" {
		attrs = append(attrs, attribute.String("gen_ai.request.model", opts.Model))
	}
	if opts.RunID != "" {
		attrs = append(attrs, attribute.String("agent.run_id", opts.RunID))
	}
	if opts.IssueID != "" {
		attrs = append(attrs, attribute.String("issue.id", opts.IssueID))
	}
	if opts.IssueIdentifier != "" {
		attrs = append(attrs, attribute.String("issue.identifier", opts.IssueIdentifier))
	}
	if opts.WorkspaceID != "" {
		attrs = append(attrs, attribute.String("workspace.id", opts.WorkspaceID))
	}
	if opts.AgentID != "" {
		attrs = append(attrs, attribute.String("agent.id", opts.AgentID))
	}
	if opts.AgentName != "" {
		attrs = append(attrs, attribute.String("agent.name", opts.AgentName))
	}

	spanCtx, span := otel.Tracer("multica/agent").Start(ctx, "agent.run", trace.WithAttributes(attrs...))
	return &agentRunTrace{
		ctx:       spanCtx,
		span:      span,
		backend:   backend,
		system:    system,
		opts:      opts,
		toolSpans: make(map[string]trace.Span),
		toolNames: make(map[string]string),
	}
}

func (t *agentRunTrace) Context() context.Context {
	if t == nil {
		return context.Background()
	}
	return t.ctx
}

func (t *agentRunTrace) RecordAssistantTurn(model string, usage TokenUsage, finishReason string) {
	if t == nil {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String("gen_ai.system", t.system),
		attribute.String("gen_ai.request.model", defaultTraceLabel(model)),
		attribute.Int64("gen_ai.usage.input_tokens", usage.InputTokens),
		attribute.Int64("gen_ai.usage.output_tokens", usage.OutputTokens),
		attribute.Int64("gen_ai.usage.cache_read_tokens", usage.CacheReadTokens),
		attribute.Int64("gen_ai.usage.cache_write_tokens", usage.CacheWriteTokens),
	}
	if finishReason != "" {
		attrs = append(attrs, attribute.String("gen_ai.response.finish_reason", finishReason))
	}
	_, span := otel.Tracer("multica/agent").Start(t.ctx, "llm.turn", trace.WithAttributes(attrs...))
	span.End()

	t.addTokens(model, usage)
}

func (t *agentRunTrace) RecordToolUse(name, callID string) {
	if t == nil || callID == "" {
		return
	}
	name = sanitizeSpanName(name)
	t.mu.Lock()
	defer t.mu.Unlock()
	if old := t.toolSpans[callID]; old != nil {
		old.SetStatus(codes.Error, "duplicate tool_use without tool_result")
		old.End()
	}
	_, span := otel.Tracer("multica/agent").Start(t.ctx, "tool."+name, trace.WithAttributes(
		attribute.String("tool.name", name),
		attribute.String("tool.call_id", callID),
	))
	t.toolSpans[callID] = span
	t.toolNames[callID] = name
}

func (t *agentRunTrace) RecordToolResult(callID string, outputLen int) {
	if t == nil || callID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	span := t.toolSpans[callID]
	if span == nil {
		_, span := otel.Tracer("multica/agent").Start(t.ctx, "tool."+unknownLabel, trace.WithAttributes(
			attribute.String("tool.name", unknownLabel),
			attribute.String("tool.call_id", callID),
			attribute.Int("tool.output_length", outputLen),
		))
		span.SetStatus(codes.Error, "tool_use missing")
		span.End()
		return
	}
	span.SetAttributes(attribute.Int("tool.output_length", outputLen))
	span.End()
	delete(t.toolSpans, callID)
	delete(t.toolNames, callID)
}

func (t *agentRunTrace) RecordMessage(msg Message) {
	if t == nil {
		return
	}
	switch msg.Type {
	case MessageToolUse:
		t.RecordToolUse(msg.Tool, msg.CallID)
	case MessageToolResult:
		t.RecordToolResult(msg.CallID, len(msg.Output))
	}
}

func (t *agentRunTrace) RecordUsageMap(usage map[string]TokenUsage) {
	if t == nil {
		return
	}
	for model, u := range usage {
		t.RecordAssistantTurn(model, u, "")
	}
}

func (t *agentRunTrace) Finish(status, errorMessage string) {
	if t == nil || !t.ended.CompareAndSwap(false, true) {
		return
	}
	t.mu.Lock()
	for callID, span := range t.toolSpans {
		span.SetAttributes(attribute.String("tool.call_id", callID))
		span.SetStatus(codes.Error, "tool_result missing")
		span.End()
		delete(t.toolSpans, callID)
		delete(t.toolNames, callID)
	}
	t.mu.Unlock()

	t.span.SetAttributes(
		attribute.String("agent.status", status),
		attribute.Int64("gen_ai.usage.input_tokens.total", t.tokenSum.InputTokens),
		attribute.Int64("gen_ai.usage.output_tokens.total", t.tokenSum.OutputTokens),
		attribute.Int64("gen_ai.usage.cache_read_tokens.total", t.tokenSum.CacheReadTokens),
		attribute.Int64("gen_ai.usage.cache_write_tokens.total", t.tokenSum.CacheWriteTokens),
	)
	if errorMessage != "" || status == "failed" || status == "timeout" || status == "aborted" || status == "cancelled" {
		if errorMessage != "" {
			t.span.RecordError(&agentTraceError{message: errorMessage})
		}
		t.span.SetStatus(codes.Error, errorMessage)
	}
	t.span.End()
}

func (t *agentRunTrace) addTokens(model string, usage TokenUsage) {
	t.mu.Lock()
	t.tokenSum.InputTokens += usage.InputTokens
	t.tokenSum.OutputTokens += usage.OutputTokens
	t.tokenSum.CacheReadTokens += usage.CacheReadTokens
	t.tokenSum.CacheWriteTokens += usage.CacheWriteTokens
	t.mu.Unlock()

	agentTokenMetrics.add(model, t.opts.AgentName, t.opts.WorkspaceID, "input", usage.InputTokens)
	agentTokenMetrics.add(model, t.opts.AgentName, t.opts.WorkspaceID, "output", usage.OutputTokens)
	agentTokenMetrics.add(model, t.opts.AgentName, t.opts.WorkspaceID, "cache_read", usage.CacheReadTokens)
	agentTokenMetrics.add(model, t.opts.AgentName, t.opts.WorkspaceID, "cache_write", usage.CacheWriteTokens)
}

func genAISystemForBackend(backend string) string {
	switch strings.ToLower(backend) {
	case "claude":
		return "anthropic"
	case "codex":
		return "openai"
	case "kimi":
		return "moonshot"
	case "gemini":
		return "google"
	default:
		return strings.ToLower(backend)
	}
}

func sanitizeSpanName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return unknownLabel
	}
	replacer := strings.NewReplacer(" ", "_", "/", "_", "\\", "_", "\n", "_", "\t", "_")
	return replacer.Replace(name)
}

func defaultTraceLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return unknownLabel
	}
	return value
}

type agentTraceError struct {
	message string
}

func (e *agentTraceError) Error() string {
	return e.message
}
