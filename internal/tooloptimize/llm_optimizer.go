package tooloptimize

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"
)

// Completer performs a single LLM completion against a chosen provider+model.
// A concrete adapter (wired in cmd/) routes this through goclaw's provider
// registry; the tooloptimize package stays provider-agnostic and unit-testable.
type Completer interface {
	Complete(ctx context.Context, provider, model, prompt string) (string, error)
}

// OptimizeSettings are the user-chosen knobs for the generative (LevelLLMCurate /
// LevelAggressive) tier. Provider and Model must reference an existing configured
// goclaw provider/model — never hardcoded. Prompt overrides DefaultOptimizePrompt
// when non-empty.
type OptimizeSettings struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Prompt   string `json:"prompt"`
}

// Configured reports whether a usable provider+model is set.
func (s OptimizeSettings) Configured() bool {
	return strings.TrimSpace(s.Provider) != "" && strings.TrimSpace(s.Model) != ""
}

// PromptTemplate returns the effective prompt template (custom or default).
func (s OptimizeSettings) PromptTemplate() string {
	if strings.TrimSpace(s.Prompt) != "" {
		return s.Prompt
	}
	return DefaultOptimizePrompt
}

// Fingerprint is an opaque hash of the settings, used as Config.OptimizerVariant
// so a change to the model or prompt busts the CachingOptimizer cache.
func (s OptimizeSettings) Fingerprint() string {
	h := sha256.Sum256([]byte(s.Provider + "\x00" + s.Model + "\x00" + s.PromptTemplate()))
	return hex.EncodeToString(h[:8])
}

// DefaultOptimizePrompt is the built-in curation prompt. It receives a
// promptData via text/template. Users may override it via OptimizeSettings.Prompt
// while keeping the same {{.Field}} placeholders.
const DefaultOptimizePrompt = `You are curating the tool list for an AI agent so a smaller model can use it reliably.

Agent role: {{.Role}}
Optimization level: {{.Level}} ({{.LevelName}})

Decide, for this agent's role, which tools to keep INLINE (always visible to the
agent), which to DEFER behind a search tool (reachable on demand, not inline),
and which to DROP (irrelevant to this agent). Prefer a small inline set — small
models select better from fewer tools. Keep the agent's core-job tools inline;
defer occasionally-useful tools; drop clearly unrelated ones.

Tools (name: description):
{{.Tools}}

Respond with ONLY a JSON object and nothing else:
{"inline": ["tool_name", ...], "deferred": ["tool_name", ...], "dropped": ["tool_name", ...]}`

// LLMOptimizer curates the tool list by asking a user-chosen model. It force-keeps
// core tools inline regardless of the model's answer, and degrades to the
// deterministic HeuristicOptimizer on any error (unconfigured, call failure, or
// unparseable output) so optimization is never worse than the heuristic.
type LLMOptimizer struct {
	completer Completer
	settings  OptimizeSettings
	fallback  Optimizer
}

// NewLLMOptimizer builds an LLMOptimizer. A nil completer or unconfigured
// settings make every call transparently fall back to the heuristic.
func NewLLMOptimizer(completer Completer, settings OptimizeSettings) *LLMOptimizer {
	return &LLMOptimizer{completer: completer, settings: settings, fallback: HeuristicOptimizer{}}
}

type promptData struct {
	Role      string
	Level     int
	LevelName string
	Tools     string
}

type llmDecision struct {
	Inline   []string `json:"inline"`
	Deferred []string `json:"deferred"`
	Dropped  []string `json:"dropped"`
}

// Optimize implements Optimizer.
func (o *LLMOptimizer) Optimize(ctx context.Context, toolset []ToolDesc, profile Profile, level Level, cfg Config) (*Plan, error) {
	if o.completer == nil || !o.settings.Configured() {
		return o.fallback.Optimize(ctx, toolset, profile, level, cfg)
	}

	prompt, err := o.renderPrompt(toolset, profile, level)
	if err != nil {
		return o.fallback.Optimize(ctx, toolset, profile, level, cfg)
	}
	raw, err := o.completer.Complete(ctx, o.settings.Provider, o.settings.Model, prompt)
	if err != nil {
		return o.fallback.Optimize(ctx, toolset, profile, level, cfg)
	}
	decision, err := parseDecision(raw)
	if err != nil {
		return o.fallback.Optimize(ctx, toolset, profile, level, cfg)
	}
	return o.planFromDecision(dedupe(toolset), decision, level, cfg), nil
}

func (o *LLMOptimizer) renderPrompt(toolset []ToolDesc, profile Profile, level Level) (string, error) {
	tmpl, err := template.New("optimize").Parse(o.settings.PromptTemplate())
	if err != nil {
		return "", fmt.Errorf("parse optimize prompt: %w", err)
	}
	var tools strings.Builder
	for _, td := range toolset {
		tools.WriteString("- ")
		tools.WriteString(td.Name)
		tools.WriteString(": ")
		tools.WriteString(collapseSpaces(td.Description))
		tools.WriteByte('\n')
	}
	var out strings.Builder
	if err := tmpl.Execute(&out, promptData{
		Role: profile.Role, Level: int(level), LevelName: level.String(), Tools: tools.String(),
	}); err != nil {
		return "", fmt.Errorf("render optimize prompt: %w", err)
	}
	return out.String(), nil
}

// planFromDecision turns the model's name lists into a Plan, enforcing invariants:
// core tools are always inline; any tool the model omitted defaults to inline
// (never silently lost); deferred/dropped never contain core tools.
func (o *LLMOptimizer) planFromDecision(toolset []ToolDesc, d *llmDecision, level Level, cfg Config) *Plan {
	cfg = cfg.withDefaults()
	byName := make(map[string]ToolDesc, len(toolset))
	order := make([]string, 0, len(toolset))
	for _, td := range toolset {
		byName[td.Name] = td
		order = append(order, td.Name)
	}

	deferredSet := make(map[string]bool)
	droppedSet := make(map[string]bool)
	for _, n := range d.Deferred {
		if td, ok := byName[n]; ok && !td.Core {
			deferredSet[n] = true
		}
	}
	for _, n := range d.Dropped {
		if td, ok := byName[n]; ok && !td.Core && !deferredSet[n] {
			droppedSet[n] = true
		}
	}

	plan := &Plan{Level: level, EstTokensBefore: EstimateTokens(toolset)}
	for _, n := range order { // stable original order
		td := byName[n]
		switch {
		case td.Core:
			plan.Inline = append(plan.Inline, td)
		case deferredSet[n]:
			plan.Deferred = append(plan.Deferred, td)
		case droppedSet[n]:
			plan.Dropped = append(plan.Dropped, n)
		default: // inline explicitly, or omitted by the model → keep inline
			plan.Inline = append(plan.Inline, td)
		}
	}

	plan.Inline = minifyAll(plan.Inline)
	plan.Deferred = minifyAll(plan.Deferred)
	plan.EstTokensAfter = EstimateTokens(plan.Inline)
	if len(plan.Deferred) > 0 {
		plan.EstTokensAfter += cfg.BridgeToolCost
	}
	plan.Activated = len(plan.Deferred) > 0 || len(plan.Dropped) > 0 ||
		plan.EstTokensAfter != plan.EstTokensBefore
	return plan
}

// parseDecision extracts the JSON object from a possibly-noisy model reply
// (models sometimes wrap JSON in prose or code fences).
func parseDecision(raw string) (*llmDecision, error) {
	start := strings.IndexByte(raw, '{')
	end := strings.LastIndexByte(raw, '}')
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON object in model reply")
	}
	var d llmDecision
	if err := json.Unmarshal([]byte(raw[start:end+1]), &d); err != nil {
		return nil, fmt.Errorf("parse decision JSON: %w", err)
	}
	return &d, nil
}
