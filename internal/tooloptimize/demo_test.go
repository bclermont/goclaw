package tooloptimize

import "testing"

// TestDemoLadder is not an assertion test — it prints the effect of every level
// on the same catalog so the tokens<->reliability tradeoff is visible with
// `go test -run TestDemoLadder -v`.
func TestDemoLadder(t *testing.T) {
	// A larger, realistic catalog: 1 core + git domain + a big off-domain tail.
	c := []ToolDesc{{Name: "terminal", Description: "run a shell command", Core: true,
		Parameters: map[string]any{"type": "object"}}}
	for _, n := range []string{"git_clone", "git_commit", "git_push", "repo_get", "repo_list"} {
		c = append(c, ToolDesc{Name: n, Description: "operate on a git repository: " + n,
			Parameters: map[string]any{"type": "object", "properties": map[string]any{"arg": map[string]any{"type": "string", "description": "argument"}}}})
	}
	// A large off-domain tail (40 tools) — big enough that deferring it behind
	// the bridge is a genuine token win.
	tail := []string{"gmail_send", "gmail_search", "weather_now", "calendar_add", "drive_upload", "grafana_query"}
	for i := len(tail); i < 40; i++ {
		tail = append(tail, "misc_tool_"+string(rune('a'+i%26))+string(rune('0'+i/26)))
	}
	for _, n := range tail {
		c = append(c, ToolDesc{Name: n, Description: "off-domain capability with a moderately long description: " + n,
			Parameters: map[string]any{"type": "object", "properties": map[string]any{"arg": map[string]any{"type": "string", "description": "an argument for the tool"}}}})
	}
	profile := Profile{Role: "gitea-researcher", DomainKeywords: []string{"git", "repo"}}
	cfg := Config{ContextWindow: 8000} // realistic window; large tail trips the defer gate and clears the bridge floor

	t.Logf("catalog: %d tools, ~%d tokens", len(c), EstimateTokens(c))
	t.Logf("%-11s %6s %6s %8s %9s %8s", "level", "inline", "defer", "dropped", "tok_after", "activated")
	for lvl := LevelOff; lvl <= LevelAggressive; lvl++ {
		p := Optimize(c, profile, lvl, cfg)
		t.Logf("%-11s %6d %6d %8d %9d %8t",
			lvl, len(p.Inline), len(p.Deferred), len(p.Dropped), p.EstTokensAfter, p.Activated)
	}
}
