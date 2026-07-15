package tooloptimize

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// catalog builds a fixture: one core tool, two in-domain git tools, and two
// off-domain tools (email, weather).
func catalog() []ToolDesc {
	return []ToolDesc{
		{Name: "terminal", Description: "run a shell command", Core: true,
			Parameters: map[string]any{"type": "object", "title": "noise",
				"properties": map[string]any{"cmd": map[string]any{"type": "string", "description": "  the   command  "}}}},
		{Name: "git_clone", Description: "clone a git repository",
			Parameters: map[string]any{"type": "object", "$schema": "http://x",
				"properties": map[string]any{"url": map[string]any{"type": "string"}}}},
		{Name: "git_commit", Description: "commit to a git repo",
			Parameters: map[string]any{"type": "object"}},
		{Name: "gmail_send_email", Description: "send an email message",
			Parameters: map[string]any{"type": "object"}},
		{Name: "weather_now", Description: "current weather for a city",
			Parameters: map[string]any{"type": "object"}},
	}
}

func gitProfile() Profile {
	return Profile{Role: "gitea-researcher", DomainKeywords: []string{"git", "repo"}}
}

func inlineNames(p *Plan) []string   { return names(p.Inline) }
func deferredNames(p *Plan) []string { return names(p.Deferred) }

func countName(toolset []ToolDesc, name string) int {
	n := 0
	for _, td := range toolset {
		if td.Name == name {
			n++
		}
	}
	return n
}

func TestLevelOffPassthrough(t *testing.T) {
	c := catalog()
	plan := Optimize(c, gitProfile(), LevelOff, Config{})
	assert.Equal(t, LevelOff, plan.Level)
	assert.Len(t, plan.Inline, len(c))
	assert.Empty(t, plan.Deferred)
	assert.Empty(t, plan.Dropped)
	assert.False(t, plan.Activated)
}

func TestLevelMinifyIsLosslessSelectionButFewerTokens(t *testing.T) {
	c := catalog()
	plan := Optimize(c, gitProfile(), LevelMinify, Config{})
	// Same tools kept (no duplicates in fixture), but tokens drop from schema slimming.
	assert.Len(t, plan.Inline, len(c))
	assert.Empty(t, plan.Deferred)
	assert.Less(t, plan.EstTokensAfter, plan.EstTokensBefore, "minify should cut tokens")

	// Noise keys removed and whitespace collapsed in the terminal schema.
	var term ToolDesc
	for _, td := range plan.Inline {
		if td.Name == "terminal" {
			term = td
		}
	}
	require.NotNil(t, term.Parameters)
	_, hasTitle := term.Parameters["title"]
	assert.False(t, hasTitle, "noise key 'title' should be pruned")
	props := term.Parameters["properties"].(map[string]any)
	cmd := props["cmd"].(map[string]any)
	assert.Equal(t, "the command", cmd["description"], "description whitespace collapsed")
}

func TestLevelMinifyDropsByteIdenticalDuplicates(t *testing.T) {
	c := catalog()
	dup := c[3] // gmail_send_email, identical schema
	c = append(c, dup)
	plan := Optimize(c, gitProfile(), LevelMinify, Config{})
	assert.Len(t, plan.Inline, len(catalog()), "duplicate collapsed to one entry")
	// The name still resolves to the surviving copy, so nothing is "dropped" by name.
	assert.NotContains(t, inlineNames(plan), "", "no empty tool names")
	assert.Equal(t, 1, countName(plan.Inline, "gmail_send_email"), "exactly one gmail tool remains")
}

func TestLevelScopeKeepsDomainPlusCoreDropsRest(t *testing.T) {
	plan := Optimize(catalog(), gitProfile(), LevelScope, Config{})
	inline := inlineNames(plan)
	assert.ElementsMatch(t, []string{"terminal", "git_clone", "git_commit"}, inline)
	assert.ElementsMatch(t, []string{"gmail_send_email", "weather_now"}, plan.Dropped)
	assert.Empty(t, plan.Deferred)
	assert.True(t, plan.Activated)
}

func TestLevelDeferTailBelowThresholdStaysInline(t *testing.T) {
	// Huge context window → threshold far above the tiny tail → nothing deferred.
	plan := Optimize(catalog(), gitProfile(), LevelDeferTail, Config{ContextWindow: 128000})
	assert.Len(t, plan.Inline, len(catalog()))
	assert.Empty(t, plan.Deferred)
	assert.Empty(t, plan.Dropped, "defer-tail never drops")
}

func TestLevelDeferTailAboveThresholdDefersTail(t *testing.T) {
	// Force the gate: tiny threshold + tiny bridge cost so the small tail both
	// exceeds the threshold and clears the bridge-cost floor.
	plan := Optimize(catalog(), gitProfile(), LevelDeferTail, Config{ContextWindow: 1, BridgeToolCost: 1})
	assert.ElementsMatch(t, []string{"terminal", "git_clone", "git_commit"}, inlineNames(plan))
	assert.ElementsMatch(t, []string{"gmail_send_email", "weather_now"}, deferredNames(plan))
	assert.Empty(t, plan.Dropped)
	// The deferred tail is replaced by the bridge cost, not its full schemas.
	assert.Greater(t, plan.EstTokensAfter, 0)
}

func TestLevelDeferTailBridgeCostFloorKeepsSmallTailInline(t *testing.T) {
	// Threshold trips (tiny window) but the tail is smaller than the bridge that
	// would replace it, so deferring would COST tokens. Guard keeps it inline.
	plan := Optimize(catalog(), gitProfile(), LevelDeferTail, Config{ContextWindow: 1, BridgeToolCost: 100000})
	assert.Len(t, plan.Inline, len(catalog()))
	assert.Empty(t, plan.Deferred, "small tail must not be deferred when bridge costs more")
}

func TestLevelAggressiveKeepsOnlyCore(t *testing.T) {
	plan := Optimize(catalog(), gitProfile(), LevelAggressive, Config{ContextWindow: 128000})
	assert.Equal(t, []string{"terminal"}, inlineNames(plan))
	assert.ElementsMatch(t,
		[]string{"git_clone", "git_commit", "gmail_send_email", "weather_now"},
		deferredNames(plan))
}

func TestEmptyDomainKeywordsMakeEverythingInDomain(t *testing.T) {
	p := Profile{Role: "generalist"} // no keywords
	plan := Optimize(catalog(), p, LevelScope, Config{})
	assert.Len(t, plan.Inline, len(catalog()))
	assert.Empty(t, plan.Dropped)
}

func TestLevelString(t *testing.T) {
	assert.Equal(t, "scope", LevelScope.String())
	assert.Equal(t, "aggressive", LevelAggressive.String())
	assert.True(t, LevelOff.Valid())
	assert.False(t, Level(9).Valid())
}
