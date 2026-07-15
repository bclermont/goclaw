package tooloptimize

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countingOptimizer counts how many times the inner strategy actually runs.
type countingOptimizer struct {
	calls int
}

func (c *countingOptimizer) Optimize(toolset []ToolDesc, profile Profile, level Level, cfg Config) (*Plan, error) {
	c.calls++
	return Optimize(toolset, profile, level, cfg), nil
}

func TestHeuristicOptimizerMatchesPureFunction(t *testing.T) {
	plan, err := HeuristicOptimizer{}.Optimize(catalog(), gitProfile(), LevelScope, Config{})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"terminal", "git_clone", "git_commit"}, inlineNames(plan))
}

func TestCachingOptimizerReusesPlanForSameCatalog(t *testing.T) {
	inner := &countingOptimizer{}
	opt := NewCachingOptimizer(inner)

	for i := 0; i < 3; i++ {
		_, err := opt.Optimize(catalog(), gitProfile(), LevelLLMCurate, Config{ContextWindow: 1})
		require.NoError(t, err)
	}
	assert.Equal(t, 1, inner.calls, "inner strategy runs once, then serves from cache")
	assert.Equal(t, 1, opt.Len())
}

func TestCachingOptimizerReoptimizesWhenCatalogChanges(t *testing.T) {
	inner := &countingOptimizer{}
	opt := NewCachingOptimizer(inner)

	_, err := opt.Optimize(catalog(), gitProfile(), LevelLLMCurate, Config{ContextWindow: 1})
	require.NoError(t, err)

	// Add a tool → catalog hash changes → recompute.
	changed := append(catalog(), ToolDesc{Name: "git_push", Description: "push a git repo"})
	_, err = opt.Optimize(changed, gitProfile(), LevelLLMCurate, Config{ContextWindow: 1})
	require.NoError(t, err)

	assert.Equal(t, 2, inner.calls)
	assert.Equal(t, 2, opt.Len())
}

func TestCatalogHashStableAcrossToolOrder(t *testing.T) {
	c1 := catalog()
	c2 := []ToolDesc{c1[4], c1[0], c1[3], c1[1], c1[2]} // shuffled
	h1 := CatalogHash(c1, gitProfile(), LevelDeferTail, Config{})
	h2 := CatalogHash(c2, gitProfile(), LevelDeferTail, Config{})
	assert.Equal(t, h1, h2, "hash must not depend on catalog order")
}

func TestCatalogHashChangesWithLevel(t *testing.T) {
	h1 := CatalogHash(catalog(), gitProfile(), LevelScope, Config{})
	h2 := CatalogHash(catalog(), gitProfile(), LevelDeferTail, Config{})
	assert.NotEqual(t, h1, h2)
}
