package http

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/internal/tokencount"
	"github.com/nextlevelbuilder/goclaw/internal/tooloptimize"
)

// promptOptimizationView is the "Optimized" tab of the prompt preview: the tool
// list after the agent's tool_optimization_level is applied, plus the deferred/
// dropped sets and a token delta versus the raw ("Original" tab) tool list.
type promptOptimizationView struct {
	Level            int                        `json:"level"`
	LevelName        string                     `json:"level_name"`
	Activated        bool                       `json:"activated"`
	Tools            []providers.ToolDefinition `json:"tools"`
	DeferredNames    []string                   `json:"deferred_names,omitempty"`
	DroppedNames     []string                   `json:"dropped_names,omitempty"`
	ToolTokensBefore int                        `json:"tool_tokens_before"`
	ToolTokensAfter  int                        `json:"tool_tokens_after"`
}

var keywordSplit = regexp.MustCompile(`[^a-z0-9]+`)

// buildOptimizationView applies the agent's tool_optimization_level to the raw
// tool definitions and returns the resulting view. Returns nil when the level is
// off (0) or there are no tools — the UI then shows only the Original tab.
func buildOptimizationView(ag *store.AgentData, rawTools []providers.ToolDefinition, counter tokencount.TokenCounter) *promptOptimizationView {
	level := tooloptimize.Level(ag.ToolOptimizationLevel)
	if !level.Valid() || level == tooloptimize.LevelOff || len(rawTools) == 0 {
		return nil
	}

	descs, byName := toToolDescs(rawTools)
	profile := tooloptimize.Profile{Role: ag.AgentKey, DomainKeywords: domainKeywords(ag)}
	cfg := tooloptimize.Config{ContextWindow: ag.ContextWindow}

	plan := tooloptimize.Optimize(descs, profile, level, cfg)

	inline := toToolDefs(plan.Inline, byName)
	view := &promptOptimizationView{
		Level:            int(level),
		LevelName:        level.String(),
		Activated:        plan.Activated,
		Tools:            inline,
		DeferredNames:    descNames(plan.Deferred),
		DroppedNames:     plan.Dropped,
		ToolTokensBefore: countToolTokens(counter, rawTools),
		ToolTokensAfter:  countToolTokens(counter, inline),
	}
	if len(plan.Deferred) > 0 {
		view.ToolTokensAfter += countToolTokens(counter, []providers.ToolDefinition{searchBridgeDef(len(plan.Deferred))})
	}
	return view
}

// toToolDescs converts provider tool defs into tooloptimize descriptors. goclaw
// builtin tools (name without the "mcp_" prefix) are marked Core so they are
// never deferred or dropped, mirroring maybeEnterSearchMode which only defers
// MCP tools. It also returns a name→original-def map for reconstruction.
func toToolDescs(rawTools []providers.ToolDefinition) ([]tooloptimize.ToolDesc, map[string]providers.ToolDefinition) {
	descs := make([]tooloptimize.ToolDesc, 0, len(rawTools))
	byName := make(map[string]providers.ToolDefinition, len(rawTools))
	for _, td := range rawTools {
		if td.Function == nil {
			continue
		}
		name := td.Function.Name
		byName[name] = td
		descs = append(descs, tooloptimize.ToolDesc{
			Name:        name,
			Description: td.Function.Description,
			Parameters:  td.Function.Parameters,
			Core:        !strings.HasPrefix(name, "mcp_"),
		})
	}
	return descs, byName
}

// toToolDefs rebuilds provider tool defs from optimized descriptors, preserving
// each original def's Type/Strict while adopting the (possibly minified)
// description and parameters the optimizer produced.
func toToolDefs(descs []tooloptimize.ToolDesc, byName map[string]providers.ToolDefinition) []providers.ToolDefinition {
	out := make([]providers.ToolDefinition, 0, len(descs))
	for _, d := range descs {
		orig, ok := byName[d.Name]
		if !ok || orig.Function == nil {
			out = append(out, providers.ToolDefinition{
				Type:     "function",
				Function: &providers.ToolFunctionSchema{Name: d.Name, Description: d.Description, Parameters: d.Parameters},
			})
			continue
		}
		fn := *orig.Function
		fn.Description = d.Description
		fn.Parameters = d.Parameters
		out = append(out, providers.ToolDefinition{Type: orig.Type, Function: &fn})
	}
	return out
}

// domainKeywords derives the agent's domain vocabulary from its key (e.g.
// "gitea-researcher" → {gitea, researcher}). Used to decide which tools stay
// inline at Scope/DeferTail levels.
func domainKeywords(ag *store.AgentData) []string {
	var out []string
	for _, w := range keywordSplit.Split(strings.ToLower(ag.AgentKey), -1) {
		if len(w) >= 3 {
			out = append(out, w)
		}
	}
	return out
}

func descNames(descs []tooloptimize.ToolDesc) []string {
	if len(descs) == 0 {
		return nil
	}
	out := make([]string, len(descs))
	for i, d := range descs {
		out[i] = d.Name
	}
	return out
}

func countToolTokens(counter tokencount.TokenCounter, tools []providers.ToolDefinition) int {
	if len(tools) == 0 {
		return 0
	}
	raw, err := json.Marshal(tools)
	if err != nil {
		return 0
	}
	return counter.Count("claude-3", string(raw))
}

// searchBridgeDef is a representative schema for the search bridge tool that
// replaces a deferred tail, used only to estimate the "after" token cost.
func searchBridgeDef(deferredCount int) providers.ToolDefinition {
	_ = deferredCount
	return providers.ToolDefinition{
		Type: "function",
		Function: &providers.ToolFunctionSchema{
			Name:        "mcp_tool_search",
			Description: "Search additional tools that are loaded on demand and call them by name.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"query": map[string]any{"type": "string"}},
				"required":   []any{"query"},
			},
		},
	}
}
