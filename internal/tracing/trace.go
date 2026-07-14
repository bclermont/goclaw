package tracing

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// AddToolCall appends a tool call record to the trace identified in ctx.
// No-op if the context has no trace ID or collector.
func AddToolCall(ctx context.Context, call store.ToolCallData) {
	traceID := TraceIDFromContext(ctx)
	if traceID == uuid.Nil {
		return
	}
	collector := CollectorFromContext(ctx)
	if collector == nil {
		return
	}
	data, err := collector.GetTrace(ctx, traceID)
	if err != nil {
		slog.Warn("tracing: failed to get trace for AddToolCall", "trace_id", traceID, "error", err)
		return
	}
	if data == nil {
		return
	}
	data.ToolCalls = append(data.ToolCalls, call)
	collector.UpdateTrace(ctx, traceID, map[string]any{"tool_calls": data.ToolCalls})
}

// AddHookExecution appends a hook execution record to the trace identified in ctx.
// No-op if the context has no trace ID or collector.
func AddHookExecution(ctx context.Context, exec store.HookExecutionData) {
	traceID := TraceIDFromContext(ctx)
	if traceID == uuid.Nil {
		return
	}
	collector := CollectorFromContext(ctx)
	if collector == nil {
		return
	}
	data, err := collector.GetTrace(ctx, traceID)
	if err != nil {
		slog.Warn("tracing: failed to get trace for AddHookExecution", "trace_id", traceID, "error", err)
		return
	}
	if data == nil {
		return
	}
	data.HookExecutions = append(data.HookExecutions, exec)
	collector.UpdateTrace(ctx, traceID, map[string]any{"hook_executions": data.HookExecutions})
}
