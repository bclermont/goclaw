// Package tooloptimize implements a graduated (level 0–5) strategy for shaping
// an agent's tool catalog before it is handed to the model.
//
// The problem: every exposed tool schema costs prompt tokens and, for small
// models, a long inline tool list degrades tool-selection accuracy. goclaw
// already has a search/deferral mechanism (internal/mcp maybeEnterSearchMode)
// but it splits tools globally by registration order. This package turns the
// split into a per-agent, level-driven decision.
//
// The ladder is a tokens <-> reliability <-> offline-compute dial, NOT a
// "higher is better" scale. Small models (e.g. gemma) are most reliable in the
// middle (Scope / DeferTail); the top of the ladder trades reliability for
// token savings and only pays off for stronger models or very large catalogs.
//
// Every level is deterministic and produces a Plan the caller applies to its
// registry. Levels that call an LLM (LLMCurate, Aggressive) do so only through
// the Optimizer interface, which is designed to run OFFLINE and be cached on
// the catalog hash — never in the model's per-turn hot path. The bundled
// HeuristicOptimizer needs no LLM and is used both for levels 0–3 and as the
// deterministic fallback for 4–5.
package tooloptimize

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
)

// Level selects an optimization strategy. Each level adds a strategy on top of
// the previous one.
type Level int

const (
	// LevelOff passes the catalog through unchanged: all tools inline, in the
	// original order. Maximum tokens, maximum reliability for small lists.
	LevelOff Level = iota
	// LevelMinify deterministically slims every schema and drops byte-identical
	// duplicate tools. Lossless; pure token savings.
	LevelMinify
	// LevelScope keeps only the agent's domain toolset (plus core) inline and
	// drops the rest entirely. Highest small-model reliability.
	LevelScope
	// LevelDeferTail keeps domain+core inline but, when the non-domain tail
	// would exceed the context threshold, defers it behind the search bridge
	// instead of dropping it.
	LevelDeferTail
	// LevelLLMCurate uses an offline Optimizer to pick the inline set and group
	// the deferred tail. Same runtime shape as DeferTail, smarter static config.
	LevelLLMCurate
	// LevelAggressive keeps only core inline and defers everything else
	// regardless of threshold. Maximum token savings, maximum indirection.
	// Expected to hurt small models.
	LevelAggressive
)

// String returns the human label for a level.
func (l Level) String() string {
	switch l {
	case LevelOff:
		return "off"
	case LevelMinify:
		return "minify"
	case LevelScope:
		return "scope"
	case LevelDeferTail:
		return "defer-tail"
	case LevelLLMCurate:
		return "llm-curate"
	case LevelAggressive:
		return "aggressive"
	default:
		return "unknown"
	}
}

// Valid reports whether l is a defined level.
func (l Level) Valid() bool {
	return l >= LevelOff && l <= LevelAggressive
}

// ToolDesc is a provider-agnostic description of a single tool. It carries just
// enough to make an inline/defer/drop decision and to estimate token cost.
type ToolDesc struct {
	Name        string
	Description string
	Parameters  map[string]any
	// Core marks a tool that must never be deferred or dropped ("always-load
	// means always-load"), regardless of level or domain relevance.
	Core bool
}

// Profile describes the agent the catalog is being shaped for.
type Profile struct {
	// Role is a short identifier (agent_key) used only for cache keying.
	Role string
	// DomainKeywords drive domain relevance for Scope/DeferTail: a tool is
	// in-domain when any keyword appears in its name or description. Empty
	// keywords means every tool is in-domain (Scope becomes a no-op filter).
	DomainKeywords []string
}

// Config tunes the threshold-gated levels.
type Config struct {
	// ContextWindow is the model's context size in tokens. Zero disables the
	// percentage gate and falls back to AbsoluteThreshold.
	ContextWindow int
	// ThresholdPct is the share of the context window the deferrable tail may
	// occupy before DeferTail defers it. Default 10.
	ThresholdPct float64
	// AbsoluteThreshold is the token cutoff used when ContextWindow is unknown.
	// Default 20000.
	AbsoluteThreshold int
	// BridgeToolCost is the estimated token cost of the search bridge tools that
	// replace a deferred tail. Default 300.
	BridgeToolCost int
	// OptimizerVariant is an opaque fingerprint of the LLM-optimizer settings
	// (provider + model + prompt). It participates in CatalogHash so that
	// changing the optimization model or prompt busts the cache and forces a
	// re-optimization. Empty for the deterministic levels.
	OptimizerVariant string
}

func (c Config) withDefaults() Config {
	if c.ThresholdPct <= 0 {
		c.ThresholdPct = 10
	}
	if c.AbsoluteThreshold <= 0 {
		c.AbsoluteThreshold = 20000
	}
	if c.BridgeToolCost <= 0 {
		c.BridgeToolCost = 300
	}
	return c
}

// thresholdTokens returns the token budget above which the tail is deferred.
func (c Config) thresholdTokens() int {
	if c.ContextWindow > 0 {
		return int(float64(c.ContextWindow) * (c.ThresholdPct / 100.0))
	}
	return c.AbsoluteThreshold
}

// Plan is the outcome of optimizing a catalog. Inline tools are registered
// directly; Deferred tools are surfaced through the search bridge; Dropped
// names are removed from the agent entirely.
type Plan struct {
	Level           Level
	Inline          []ToolDesc
	Deferred        []ToolDesc
	Dropped         []string
	EstTokensBefore int
	// EstTokensAfter counts inline schemas plus the bridge cost when anything is
	// deferred. It is what the model actually pays per turn.
	EstTokensAfter int
	// Activated reports whether the level changed anything versus LevelOff.
	Activated bool
}

const charsPerToken = 4.0

// EstimateTokens approximates the token cost of a tool list via the chars/4
// rule of thumb — stable across providers and precise enough to gate decisions.
func EstimateTokens(toolset []ToolDesc) int {
	total := 0
	for i := range toolset {
		total += schemaChars(toolset[i])
	}
	return int(math.Ceil(float64(total) / charsPerToken))
}

func schemaChars(td ToolDesc) int {
	n := len(td.Name) + len(td.Description)
	if td.Parameters != nil {
		if raw, err := json.Marshal(td.Parameters); err == nil {
			n += len(raw)
		}
	}
	return n
}

// entrySearchText builds the BM25 search blob for a tool: its name (with
// snake_case/dotted/hyphenated segments broken into words so "git_clone" indexes
// "git" and "clone"), its description, and its top-level parameter names.
// Mirrors Hermes's _entry_search_text.
func entrySearchText(td ToolDesc) string {
	replacer := strings.NewReplacer("_", " ", ".", " ", "-", " ", ":", " ")
	nameWords := replacer.Replace(td.Name)
	var params string
	if props, ok := td.Parameters["properties"].(map[string]any); ok {
		names := make([]string, 0, len(props))
		for k := range props {
			names = append(names, k)
		}
		sort.Strings(names)
		params = strings.Join(names, " ")
	}
	return nameWords + " " + td.Description + " " + params
}

// domainSet returns the set of tool names considered in-domain for the profile,
// ranked by BM25 against the domain keywords (a stronger signal than substring
// match, still pure Go — no model). Core tools are always in-domain; with no
// keywords every tool is in-domain. A substring fallback covers zero-IDF query
// terms BM25 scores at zero. Note: a genuine vocabulary gap (keyword "gitea" vs
// tool token "git") is not bridged here — that needs the embedding tier.
func (p Profile) domainSet(toolset []ToolDesc) map[string]bool {
	set := make(map[string]bool, len(toolset))
	if len(p.DomainKeywords) == 0 {
		for _, td := range toolset {
			set[td.Name] = true
		}
		return set
	}

	candidates := make([]ToolDesc, 0, len(toolset))
	texts := make([]string, 0, len(toolset))
	for _, td := range toolset {
		if td.Core {
			set[td.Name] = true
			continue
		}
		candidates = append(candidates, td)
		texts = append(texts, entrySearchText(td))
	}

	ix := buildBM25Index(texts)
	query := tokenize(strings.Join(p.DomainKeywords, " "))
	for i, td := range candidates {
		if ix.score(query, i) > 0 || substringMatch(td, p.DomainKeywords) {
			set[td.Name] = true
		}
	}
	return set
}

// substringMatch is the zero-IDF fallback: true if any keyword occurs literally
// in the tool's name or description.
func substringMatch(td ToolDesc, keywords []string) bool {
	hay := strings.ToLower(td.Name + " " + td.Description)
	for _, kw := range keywords {
		kw = strings.ToLower(strings.TrimSpace(kw))
		if kw != "" && strings.Contains(hay, kw) {
			return true
		}
	}
	return false
}

// Optimize shapes toolset for the given level using the built-in heuristic
// strategy. It is deterministic and never calls out. For LevelLLMCurate and
// LevelAggressive a real deployment would route through a CachingOptimizer
// wrapping an LLM-backed Optimizer; this function provides the deterministic
// fallback those levels degrade to.
func Optimize(toolset []ToolDesc, profile Profile, level Level, cfg Config) *Plan {
	cfg = cfg.withDefaults()
	before := EstimateTokens(toolset)

	plan := &Plan{Level: level, EstTokensBefore: before}

	switch level {
	case LevelOff:
		plan.Inline = cloneAll(toolset)
	case LevelMinify:
		plan.Inline = minifyAll(dedupe(toolset))
		plan.Dropped = droppedNames(toolset, plan.Inline, nil)
	case LevelScope:
		inline, dropped := partitionDomain(dedupe(toolset), profile)
		plan.Inline = minifyAll(inline)
		plan.Dropped = names(dropped)
	case LevelDeferTail:
		plan.Inline, plan.Deferred = deferTail(dedupe(toolset), profile, cfg)
		plan.Inline = minifyAll(plan.Inline)
		plan.Deferred = minifyAll(plan.Deferred)
	case LevelLLMCurate:
		// Deterministic fallback == DeferTail. A real Optimizer improves the
		// selection quality and groups the deferred tail by topic.
		plan.Inline, plan.Deferred = deferTail(dedupe(toolset), profile, cfg)
		plan.Inline = minifyAll(plan.Inline)
		plan.Deferred = minifyAll(plan.Deferred)
	case LevelAggressive:
		inline, deferred := partitionCore(dedupe(toolset))
		plan.Inline = minifyAll(inline)
		plan.Deferred = minifyAll(deferred)
	default:
		plan.Inline = cloneAll(toolset)
	}

	plan.EstTokensAfter = EstimateTokens(plan.Inline)
	if len(plan.Deferred) > 0 {
		plan.EstTokensAfter += cfg.BridgeToolCost
	}
	plan.Activated = level != LevelOff &&
		(len(plan.Deferred) > 0 || len(plan.Dropped) > 0 || plan.EstTokensAfter != before)
	return plan
}

// partitionDomain splits into (in-domain-or-core, out-of-domain) using a
// BM25-ranked domain membership set computed once over the toolset.
func partitionDomain(toolset []ToolDesc, profile Profile) (keep, drop []ToolDesc) {
	inDomain := profile.domainSet(toolset)
	for _, td := range toolset {
		if inDomain[td.Name] {
			keep = append(keep, td)
		} else {
			drop = append(drop, td)
		}
	}
	return keep, drop
}

// deferTail keeps domain+core inline and defers the out-of-domain tail only
// when doing so is worthwhile: the tail must exceed the context-% threshold AND
// be larger than the bridge tools that would replace it. The bridge-cost floor
// prevents the perverse case where deferring a small tail costs MORE tokens
// than leaving it inline (and adds indirection for nothing). Below either bar,
// everything stays inline; nothing is dropped.
func deferTail(toolset []ToolDesc, profile Profile, cfg Config) (inline, deferred []ToolDesc) {
	keep, tail := partitionDomain(toolset, profile)
	tailTokens := EstimateTokens(tail)
	if tailTokens < cfg.thresholdTokens() || tailTokens <= cfg.BridgeToolCost {
		return append(keep, tail...), nil
	}
	return keep, tail
}

// partitionCore splits into (core, everything-else).
func partitionCore(toolset []ToolDesc) (inline, deferred []ToolDesc) {
	for _, td := range toolset {
		if td.Core {
			inline = append(inline, td)
		} else {
			deferred = append(deferred, td)
		}
	}
	return inline, deferred
}

// dedupe drops tools whose full schema is byte-identical to one already seen,
// keeping the first occurrence and stable order.
func dedupe(toolset []ToolDesc) []ToolDesc {
	seen := make(map[string]struct{}, len(toolset))
	out := make([]ToolDesc, 0, len(toolset))
	for _, td := range toolset {
		key := canonical(td)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, td)
	}
	return out
}

func canonical(td ToolDesc) string {
	raw, err := json.Marshal(td.Parameters)
	if err != nil {
		raw = []byte(err.Error())
	}
	return td.Name + "\x00" + td.Description + "\x00" + string(raw)
}

// minifyAll returns minified copies of every tool.
func minifyAll(toolset []ToolDesc) []ToolDesc {
	out := make([]ToolDesc, len(toolset))
	for i, td := range toolset {
		out[i] = minify(td)
	}
	return out
}

// minify slims a schema losslessly: it trims description whitespace and strips
// noise keys that cost tokens without steering tool selection.
func minify(td ToolDesc) ToolDesc {
	td.Description = strings.TrimSpace(collapseSpaces(td.Description))
	td.Parameters = pruneParams(td.Parameters)
	return td
}

var noiseKeys = map[string]struct{}{
	"$schema":              {},
	"additionalProperties": {},
	"examples":             {},
	"title":                {},
	"$id":                  {},
}

// pruneParams recursively removes noise keys and empty values from a schema.
func pruneParams(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		if _, noise := noiseKeys[k]; noise {
			continue
		}
		switch val := v.(type) {
		case map[string]any:
			pruned := pruneParams(val)
			if len(pruned) > 0 {
				out[k] = pruned
			}
		case string:
			trimmed := strings.TrimSpace(collapseSpaces(val))
			if trimmed != "" {
				out[k] = trimmed
			}
		case nil:
			// drop nulls
		default:
			out[k] = v
		}
	}
	return out
}

func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func cloneAll(toolset []ToolDesc) []ToolDesc {
	out := make([]ToolDesc, len(toolset))
	copy(out, toolset)
	return out
}

func names(toolset []ToolDesc) []string {
	out := make([]string, len(toolset))
	for i, td := range toolset {
		out[i] = td.Name
	}
	return out
}

// droppedNames returns the names present in original but absent from kept,
// excluding anything already listed in extra.
func droppedNames(original, kept []ToolDesc, extra []string) []string {
	keptSet := make(map[string]struct{}, len(kept))
	for _, td := range kept {
		keptSet[td.Name] = struct{}{}
	}
	for _, n := range extra {
		keptSet[n] = struct{}{}
	}
	var out []string
	for _, td := range original {
		if _, ok := keptSet[td.Name]; !ok {
			out = append(out, td.Name)
		}
	}
	sort.Strings(out)
	return out
}
