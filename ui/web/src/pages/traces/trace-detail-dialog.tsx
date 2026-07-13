import { useState, useEffect, useCallback, useMemo } from "react";
import { useTranslation } from "react-i18next";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Copy, Check, Download, Square } from "lucide-react";
import { useClipboard } from "@/hooks/use-clipboard";
import { useHttp } from "@/hooks/use-ws";
import { useWsEvent } from "@/hooks/use-ws-event";
import { Events } from "@/api/protocol";
import { formatDate, formatDuration, formatTokens, computeDurationMs } from "@/lib/format";
import { useUiStore } from "@/stores/use-ui-store";
import { SpanTreeNode, StatusBadge } from "./trace-span-tree-node";
import { buildSpanTree } from "@/adapters/trace.adapter";
import { TracePreviewBlock } from "./trace-preview-block";
import type { TraceData, SpanData } from "./hooks/use-traces";
import type { AgentEventPayload } from "@/types/chat";

interface TraceDetailDialogProps {
  traceId: string;
  onClose: () => void;
  getTrace: (id: string) => Promise<{ trace: TraceData; spans: SpanData[] } | null>;
  onNavigateTrace?: (traceId: string) => void;
  onAbortRun?: (trace: TraceData, e: React.MouseEvent) => void;
}

export function TraceDetailDialog({ traceId, onClose, getTrace, onNavigateTrace, onAbortRun }: TraceDetailDialogProps) {
  const { t } = useTranslation("traces");
  const tz = useUiStore((s) => s.timezone);
  const http = useHttp();
  const [trace, setTrace] = useState<TraceData | null>(null);
  const [spans, setSpans] = useState<SpanData[]>([]);
  const [loading, setLoading] = useState(true);
  const [exporting, setExporting] = useState(false);
  const { copied, copy } = useClipboard();

  const handleExport = useCallback(async () => {
    setExporting(true);
    try {
      const blob = await http.downloadBlob(`/v1/traces/${traceId}/export`);
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url; a.download = `trace-${traceId.slice(0, 8)}.json.gz`; a.click();
      URL.revokeObjectURL(url);
    } catch { /* silently fail */ } finally { setExporting(false); }
  }, [http, traceId]);

  const fetchTrace = useCallback(() => {
    getTrace(traceId).then((result) => {
      if (result) { setTrace(result.trace); setSpans(result.spans ?? []); }
    });
  }, [traceId, getTrace]);

  useEffect(() => {
    setLoading(true);
    getTrace(traceId)
      .then((result) => { if (result) { setTrace(result.trace); setSpans(result.spans ?? []); } })
      .finally(() => setLoading(false));
  }, [traceId, getTrace]);

  // Auto-refetch when trace aggregates update (flush-buffered, 5s interval).
  useWsEvent(Events.TRACE_UPDATED, useCallback(
    (payload: unknown) => { const data = payload as { trace_ids?: string[] }; if (data?.trace_ids?.includes(traceId)) fetchTrace(); },
    [traceId, fetchTrace],
  ));

  // Immediate status update (fired on every status write, before flush tick).
  useWsEvent(Events.TRACE_STATUS, useCallback(
    (payload: unknown) => {
      const data = payload as { traceId?: string };
      if (data?.traceId === traceId) fetchTrace();
    },
    [traceId, fetchTrace],
  ));

  // Refetch on run completion
  useWsEvent(Events.AGENT, useCallback(
    (payload: unknown) => {
      const event = payload as AgentEventPayload;
      if (event?.type === "run.completed" || event?.type === "run.failed" || event?.type === "run.cancelled") fetchTrace();
    },
    [fetchTrace],
  ));

  const spanTree = useMemo(() => buildSpanTree(spans), [spans]);

  return (
    <Dialog open onOpenChange={() => onClose()}>
      <DialogContent className="max-h-[85vh] w-[95vw] flex flex-col sm:max-w-6xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 pr-8">
            {t("detail.title")}
            <button type="button" onClick={() => copy(traceId)} className="ml-auto flex items-center gap-1 rounded px-2 py-1 text-xs text-muted-foreground transition-colors hover:bg-muted hover:text-foreground">
              {copied ? <Check className="h-3.5 w-3.5 text-green-500" /> : <Copy className="h-3.5 w-3.5" />}
              {t("detail.copyTraceId")}
            </button>
            <button type="button" onClick={handleExport} disabled={exporting || !trace} className="flex items-center gap-1 rounded px-2 py-1 text-xs text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:opacity-50">
              {exporting ? <div className="h-3.5 w-3.5 animate-spin rounded-full border-2 border-muted-foreground border-t-transparent" /> : <Download className="h-3.5 w-3.5" />}
              {t("detail.export")}
            </button>
            {trace && trace.status === "running" && onAbortRun && (
              <button type="button" onClick={(e) => onAbortRun(trace, e)} className="flex cursor-pointer items-center gap-1 rounded-md bg-destructive px-2 py-1 text-xs text-destructive-foreground transition-colors hover:bg-destructive/90">
                <Square className="h-3.5 w-3.5" />{t("detail.stopRun")}
              </button>
            )}
          </DialogTitle>
        </DialogHeader>

        <div className="overflow-y-auto min-h-0 -mx-4 px-4 sm:-mx-6 sm:px-6">
          {loading && !trace ? (
            <div className="flex items-center justify-center py-12"><div className="h-6 w-6 animate-spin rounded-full border-2 border-muted-foreground border-t-transparent" /></div>
          ) : !trace ? (
            <p className="py-8 text-center text-sm text-muted-foreground">{t("detail.notFound")}</p>
          ) : (
            <div className="space-y-4">
              <TraceSummaryGrid trace={trace} tz={tz} onNavigateTrace={onNavigateTrace} />

              {trace.input_preview && <TracePreviewBlock label={t("detail.input")} content={trace.input_preview} />}
              {trace.output_preview && <TracePreviewBlock label={t("detail.output")} content={trace.output_preview} />}
              {trace.error && (
                <div className="rounded-md border border-red-400/30 bg-red-500/10 p-3">
                  <p className="break-all text-sm text-red-300">{trace.error}</p>
                </div>
              )}

              {spans.length > 0 && (
                <div>
                  <h4 className="mb-2 text-sm font-medium">{t("detail.spansCount", { count: spans.length })}</h4>
                  <div className="space-y-1">
                    {spanTree.map((node) => <SpanTreeNode key={node.span.id} node={node} depth={0} />)}
                  </div>
                </div>
              )}

              {trace.tool_calls && trace.tool_calls.length > 0 && (
                <div>
                  <h4 className="mb-2 text-sm font-medium">Tool Calls ({trace.tool_calls.length})</h4>
                  <div className="space-y-2">
                    {trace.tool_calls.map((call, idx) => (
                      <div key={idx} className="rounded-md border border-amber-400/30 bg-amber-500/10 p-3">
                        <div className="mb-2 flex items-center justify-between">
                          <span className="font-mono text-xs font-medium text-amber-200">{call.tool_name}</span>
                          <span className={`text-xs font-medium px-2 py-0.5 rounded ${
                            call.status === 'success' ? 'bg-green-500/30 text-green-200' :
                            call.status === 'error' ? 'bg-red-500/30 text-red-200' :
                            'bg-gray-500/30 text-gray-200'
                          }`}>{call.status}</span>
                        </div>
                        {call.input && <div className="mb-1 text-xs"><span className="text-muted-foreground">Input:</span> <span className="break-all text-amber-100">{call.input}</span></div>}
                        {call.output && <div className="mb-1 text-xs"><span className="text-muted-foreground">Output:</span> <span className="break-all text-amber-100">{call.output}</span></div>}
                        {call.error && <div className="text-xs text-red-300"><span className="text-muted-foreground">Error:</span> {call.error}</div>}
                        {call.duration_ms !== undefined && <div className="text-xs text-muted-foreground">Duration: {call.duration_ms}ms</div>}
                      </div>
                    ))}
                  </div>
                </div>
              )}

              {trace.hook_executions && trace.hook_executions.length > 0 && (
                <div>
                  <h4 className="mb-2 text-sm font-medium">Hook Executions ({trace.hook_executions.length})</h4>
                  <div className="space-y-2">
                    {trace.hook_executions.map((exec, idx) => (
                      <div key={idx} className="rounded-md border border-blue-400/30 bg-blue-500/10 p-3">
                        <div className="mb-2 flex items-center justify-between">
                          <span className="font-mono text-xs font-medium text-blue-200">{exec.event}</span>
                          <span className={`text-xs font-medium px-2 py-0.5 rounded ${
                            exec.decision === 'allow' ? 'bg-green-500/30 text-green-200' :
                            exec.decision === 'block' ? 'bg-red-500/30 text-red-200' :
                            exec.decision === 'timeout' ? 'bg-yellow-500/30 text-yellow-200' :
                            'bg-gray-500/30 text-gray-200'
                          }`}>{exec.decision}</span>
                        </div>
                        <div className="text-xs text-muted-foreground">Hook ID: <span className="font-mono text-blue-200">{exec.hook_id.slice(0, 12)}...</span></div>
                        {exec.error && <div className="mt-1 text-xs text-red-300">{exec.error}</div>}
                        {exec.duration_ms !== undefined && <div className="text-xs text-muted-foreground">Duration: {exec.duration_ms}ms</div>}
                      </div>
                    ))}
                  </div>
                </div>
              )}
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}

/** Trace metadata summary grid */
function TraceSummaryGrid({ trace, tz, onNavigateTrace }: { trace: TraceData; tz: string; onNavigateTrace?: (id: string) => void }) {
  const { t } = useTranslation("traces");
  return (
    <div className="grid grid-cols-2 gap-3 text-sm sm:grid-cols-4">
      <div><span className="text-muted-foreground">{t("detail.name")}</span> <span className="font-medium">{trace.name || t("unnamed")}</span></div>
      <div><span className="text-muted-foreground">{t("detail.status")}</span> <StatusBadge status={trace.status} /></div>
      <div><span className="text-muted-foreground">{t("detail.duration")}</span> {formatDuration(trace.duration_ms || computeDurationMs(trace.start_time, trace.end_time))}</div>
      <div><span className="text-muted-foreground">{t("detail.channel")}</span> {trace.channel || "—"}</div>
      <div>
        <span className="text-muted-foreground">{t("detail.tokens")}</span>{" "}
        {formatTokens(trace.total_input_tokens)} in / {formatTokens(trace.total_output_tokens)} out
        {((trace.metadata?.total_cache_read_tokens ?? 0) > 0) && (
          <span className="ml-1 text-xs text-green-400">{formatTokens(trace.metadata!.total_cache_read_tokens!)} {t("span.cached")}</span>
        )}
      </div>
      <div><span className="text-muted-foreground">{t("detail.spans")}</span> {trace.span_count} ({trace.llm_call_count} {t("detail.llmCalls")}, {trace.tool_call_count} {t("detail.toolCalls")})</div>
      <div><span className="text-muted-foreground">{t("detail.started")}</span> {formatDate(trace.start_time, tz)}</div>
      <div><span className="text-muted-foreground">{t("detail.createdAt")}</span> {formatDate(trace.created_at, tz)}</div>
      {trace.parent_trace_id && (
        <div>
          <span className="text-muted-foreground">{t("detail.delegatedFrom")}</span>{" "}
          <button type="button" className="cursor-pointer font-mono text-xs text-primary hover:underline" onClick={() => onNavigateTrace?.(trace.parent_trace_id!)}>
            {trace.parent_trace_id.slice(0, 8)}...
          </button>
        </div>
      )}
    </div>
  );
}
