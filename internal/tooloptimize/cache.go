package tooloptimize

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"sync"
)

// Optimizer produces a Plan for a catalog. Implementations for LevelLLMCurate /
// LevelAggressive may call a language model to curate the inline set and group
// the deferred tail by topic. They are expected to run OFFLINE — the result is
// cached (see CachingOptimizer) and reused across turns — never on the model's
// per-turn path.
type Optimizer interface {
	Optimize(ctx context.Context, toolset []ToolDesc, profile Profile, level Level, cfg Config) (*Plan, error)
}

// HeuristicOptimizer is the deterministic, no-LLM strategy. It backs levels 0–3
// directly and serves as the fallback the LLM levels degrade to when no model
// is configured.
type HeuristicOptimizer struct{}

// Optimize implements Optimizer.
func (HeuristicOptimizer) Optimize(_ context.Context, toolset []ToolDesc, profile Profile, level Level, cfg Config) (*Plan, error) {
	return Optimize(toolset, profile, level, cfg), nil
}

// CatalogHash returns a stable key over the inputs that determine a Plan. It is
// intentionally computed over the tool CATALOG (names, descriptions, schemas)
// plus the agent role, level and threshold config — NOT the full system prompt,
// which changes every turn (per-user files, memory) and would bust the cache
// while the tool list stays put.
func CatalogHash(toolset []ToolDesc, profile Profile, level Level, cfg Config) string {
	cfg = cfg.withDefaults()

	sorted := make([]ToolDesc, len(toolset))
	copy(sorted, toolset)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	keywords := make([]string, len(profile.DomainKeywords))
	copy(keywords, profile.DomainKeywords)
	sort.Strings(keywords)

	h := sha256.New()
	write := func(s string) { _, _ = h.Write([]byte(s)); _, _ = h.Write([]byte{0}) }

	write(profile.Role)
	write(strconv.Itoa(int(level)))
	write(strconv.Itoa(cfg.ContextWindow))
	write(strconv.FormatFloat(cfg.ThresholdPct, 'f', 4, 64))
	write(strconv.Itoa(cfg.AbsoluteThreshold))
	write(strconv.Itoa(cfg.BridgeToolCost))
	write(cfg.OptimizerVariant)
	for _, kw := range keywords {
		write(kw)
	}
	for _, td := range sorted {
		write(td.Name)
		write(td.Description)
		write(strconv.FormatBool(td.Core))
		if raw, err := json.Marshal(td.Parameters); err == nil {
			write(string(raw))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// CachingOptimizer memoizes an inner Optimizer on the catalog hash so an
// expensive (e.g. LLM-backed) strategy runs once per distinct catalog and is
// re-run automatically only when the catalog, role, level, or config changes.
type CachingOptimizer struct {
	inner Optimizer

	mu    sync.RWMutex
	cache map[string]*Plan
}

// NewCachingOptimizer wraps inner with a hash-keyed cache. A nil inner defaults
// to HeuristicOptimizer.
func NewCachingOptimizer(inner Optimizer) *CachingOptimizer {
	if inner == nil {
		inner = HeuristicOptimizer{}
	}
	return &CachingOptimizer{inner: inner, cache: make(map[string]*Plan)}
}

// Optimize returns the cached Plan for the catalog or computes and stores one.
func (c *CachingOptimizer) Optimize(ctx context.Context, toolset []ToolDesc, profile Profile, level Level, cfg Config) (*Plan, error) {
	key := CatalogHash(toolset, profile, level, cfg)

	c.mu.RLock()
	if plan, ok := c.cache[key]; ok {
		c.mu.RUnlock()
		return plan, nil
	}
	c.mu.RUnlock()

	plan, err := c.inner.Optimize(ctx, toolset, profile, level, cfg)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.cache[key] = plan
	c.mu.Unlock()
	return plan, nil
}

// Len reports the number of cached plans (for observability and tests).
func (c *CachingOptimizer) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.cache)
}
