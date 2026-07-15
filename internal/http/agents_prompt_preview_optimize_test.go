package http

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/internal/tokencount"
)

func toolDef(name, desc string) providers.ToolDefinition {
	return providers.ToolDefinition{
		Type: "function",
		Function: &providers.ToolFunctionSchema{
			Name: name, Description: desc,
			Parameters: map[string]any{"type": "object"},
		},
	}
}

func previewTools() []providers.ToolDefinition {
	return []providers.ToolDefinition{
		toolDef("terminal", "run a shell command"),              // builtin → core
		toolDef("mcp_git__git_clone", "clone a git repository"), // domain (gitea key)
		toolDef("mcp_gmail__send", "send an email"),             // off-domain
		toolDef("mcp_weather__now", "current weather"),          // off-domain
	}
}

func TestBuildOptimizationViewOffReturnsNil(t *testing.T) {
	ag := &store.AgentData{AgentKey: "gitea-researcher", ToolOptimizationLevel: 0}
	assert.Nil(t, buildOptimizationView(ag, previewTools(), tokencount.NewFallbackCounter()))
}

func TestBuildOptimizationViewScopeKeepsCorePlusDomain(t *testing.T) {
	ag := &store.AgentData{AgentKey: "gitea-researcher", ToolOptimizationLevel: 2, ContextWindow: 8000}
	view := buildOptimizationView(ag, previewTools(), tokencount.NewFallbackCounter())
	require.NotNil(t, view)
	assert.Equal(t, "scope", view.LevelName)

	var inline []string
	for _, td := range view.Tools {
		inline = append(inline, td.Function.Name)
	}
	// terminal is core; git_clone matches the "git"-ish domain via the agent key
	// token "gitea"? key tokens are {gitea, researcher} — git_clone contains "git"
	// but not "gitea", so it is NOT domain here. Only core survives.
	assert.Contains(t, inline, "terminal")
	assert.NotContains(t, inline, "mcp_gmail__send")
	assert.Less(t, view.ToolTokensAfter, view.ToolTokensBefore)
	assert.True(t, view.Activated)
}

func TestBuildOptimizationViewAggressiveDefersAllMCP(t *testing.T) {
	ag := &store.AgentData{AgentKey: "gitea-researcher", ToolOptimizationLevel: 5, ContextWindow: 8000}
	view := buildOptimizationView(ag, previewTools(), tokencount.NewFallbackCounter())
	require.NotNil(t, view)
	assert.Equal(t, "aggressive", view.LevelName)
	// Only the builtin (core) tool stays inline; all mcp_ tools defer.
	require.Len(t, view.Tools, 1)
	assert.Equal(t, "terminal", view.Tools[0].Function.Name)
	assert.Len(t, view.DeferredNames, 3)
}

func TestDomainKeywordsFromAgentKey(t *testing.T) {
	ag := &store.AgentData{AgentKey: "gitea-researcher"}
	assert.ElementsMatch(t, []string{"gitea", "researcher"}, domainKeywords(ag))
}
