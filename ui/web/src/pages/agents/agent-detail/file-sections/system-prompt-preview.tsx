import { useState } from "react";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { useQuery } from "@tanstack/react-query";
import { Eye, Loader2 } from "lucide-react";
import { useHttp } from "@/hooks/use-ws";
import { queryKeys } from "@/lib/query-keys";
import { cn } from "@/lib/utils";
import { ToolsPreviewSection, type ToolDefinition } from "./tools-preview-section";

const MODES = ["full", "task", "minimal", "none"] as const;
type PromptMode = (typeof MODES)[number];

interface OptimizationView {
  level: number;
  level_name: string;
  activated: boolean;
  tools?: ToolDefinition[];
  deferred_names?: string[];
  dropped_names?: string[];
  tool_tokens_before: number;
  tool_tokens_after: number;
}

interface PreviewResponse {
  mode: string;
  prompt: string;
  token_count: number;
  sections: { name: string; start: number; end: number }[];
  tools?: ToolDefinition[];
  optimization?: OptimizationView;
}

type PromptView = "original" | "optimized";

interface SystemPromptPreviewProps {
  agentKey: string;
}

/** Cache boundary marker — highlighted in the preview output. */
const CACHE_BOUNDARY = "<!-- GOCLAW_CACHE_BOUNDARY -->";

/**
 * Readonly preview of the actual system prompt built for an agent.
 * Fetches from GET /v1/agents/{id}/system-prompt-preview?mode=
 */
export function SystemPromptPreview({ agentKey }: SystemPromptPreviewProps) {
  const { t } = useTranslation("agents");
  const http = useHttp();
  const [mode, setMode] = useState<PromptMode>("full");
  const [view, setView] = useState<PromptView>("original");

  const { data, isLoading: loading, error: queryError } = useQuery({
    queryKey: queryKeys.agents.systemPromptPreview(agentKey, mode),
    queryFn: () => http.get<PreviewResponse>(
      `/v1/agents/${agentKey}/system-prompt-preview?mode=${mode}`,
    ),
    staleTime: 30_000,
  });
  const error = queryError instanceof Error ? queryError.message : queryError ? "Failed to load preview" : "";

  return (
    <div className="flex h-full flex-col rounded-lg border bg-background">
      {/* Header */}
      <div className="flex items-center justify-between gap-3 border-b px-3 py-2">
        <div className="flex items-center gap-2">
          <Eye className="h-4 w-4 text-muted-foreground" />
          <span className="text-sm font-medium">{t("files.systemPromptPreview")}</span>
        </div>
        {data && (
          <span className="rounded bg-muted px-2 py-0.5 text-xs tabular-nums text-muted-foreground">
            {data.token_count.toLocaleString()} {t("files.tokens")}
          </span>
        )}
      </div>

      {/* Mode selector */}
      <div className="flex gap-1 border-b px-3 py-1.5">
        {MODES.map((m) => (
          <button
            key={m}
            type="button"
            onClick={() => setMode(m)}
            className={cn(
              "rounded-md px-2.5 py-1 text-xs font-medium transition-colors cursor-pointer",
              mode === m
                ? "bg-primary text-primary-foreground"
                : "text-muted-foreground hover:bg-muted",
            )}
          >
            {m.charAt(0).toUpperCase() + m.slice(1)}
          </button>
        ))}
      </div>

      {/* Original / Optimized tabs — only when the agent has an optimization level set */}
      {data?.optimization && (
        <div className="flex items-center gap-1 border-b px-3 py-1.5">
          {(["original", "optimized"] as const).map((v) => (
            <button
              key={v}
              type="button"
              onClick={() => setView(v)}
              className={cn(
                "rounded-md px-2.5 py-1 text-xs font-medium transition-colors cursor-pointer",
                view === v
                  ? "bg-primary text-primary-foreground"
                  : "text-muted-foreground hover:bg-muted",
              )}
            >
              {v === "original"
                ? t("files.promptOriginal", "Original")
                : t("files.promptOptimized", "Optimized")}
            </button>
          ))}
          <span className="ml-auto rounded bg-muted px-2 py-0.5 text-2xs font-medium tabular-nums text-muted-foreground">
            L{data.optimization.level} · {data.optimization.level_name}
          </span>
        </div>
      )}

      {/* Content */}
      <div className="flex-1 overflow-auto p-3">
        {loading ? (
          <div className="flex h-full items-center justify-center">
            <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
          </div>
        ) : error ? (
          <p className="text-sm text-destructive">{error}</p>
        ) : data ? (
          view === "optimized" && data.optimization ? (
            <>
              <OptimizationSummary opt={data.optimization} t={t} />
              <pre className="whitespace-pre-wrap break-words font-mono text-xs leading-relaxed text-foreground/90">
                {renderPromptWithBoundary(data.prompt)}
              </pre>
              <ToolsPreviewSection tools={data.optimization.tools} />
            </>
          ) : (
            <>
              <pre className="whitespace-pre-wrap break-words font-mono text-xs leading-relaxed text-foreground/90">
                {renderPromptWithBoundary(data.prompt)}
              </pre>
              <ToolsPreviewSection tools={data.tools} />
            </>
          )
        ) : null}
      </div>
    </div>
  );
}

/** Summary banner for the optimized view: token delta + deferred/dropped counts. */
function OptimizationSummary({
  opt,
  t,
}: {
  opt: OptimizationView;
  t: TFunction<"agents">;
}) {
  const before = opt.tool_tokens_before;
  const after = opt.tool_tokens_after;
  const saved = before - after;
  const pct = before > 0 ? Math.round((saved / before) * 100) : 0;
  return (
    <div className="mb-3 rounded-md border border-border bg-muted/40 px-3 py-2 text-xs">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <span className="font-medium">
          {t("files.toolTokens", "Tool tokens")}:{" "}
          <span className="tabular-nums">{before.toLocaleString()}</span>
          {" → "}
          <span className="tabular-nums text-emerald-600 dark:text-emerald-400">
            {after.toLocaleString()}
          </span>
          {saved > 0 && (
            <span className="ml-1 text-emerald-600 dark:text-emerald-400">
              (−{pct}%)
            </span>
          )}
        </span>
        {opt.deferred_names && opt.deferred_names.length > 0 && (
          <span className="text-muted-foreground">
            {opt.deferred_names.length} {t("files.deferred", "deferred to search")}
          </span>
        )}
        {opt.dropped_names && opt.dropped_names.length > 0 && (
          <span className="text-muted-foreground">
            {opt.dropped_names.length} {t("files.dropped", "dropped")}
          </span>
        )}
      </div>
    </div>
  );
}

/** Render prompt text with cache boundary highlighted. */
function renderPromptWithBoundary(prompt: string) {
  const idx = prompt.indexOf(CACHE_BOUNDARY);
  if (idx === -1) return prompt;

  const before = prompt.slice(0, idx);
  const after = prompt.slice(idx + CACHE_BOUNDARY.length);

  return (
    <>
      {before}
      <span className="my-1 block border-t border-dashed border-amber-500/50 py-1 text-2xs font-medium text-amber-600 dark:text-amber-400">
        ── cache boundary ── stable above · dynamic below ──
      </span>
      {after}
    </>
  );
}
