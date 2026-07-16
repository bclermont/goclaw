package tooloptimize

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeCompleter returns a canned reply (or error) and records the prompt.
type fakeCompleter struct {
	reply      string
	err        error
	lastPrompt string
	lastModel  string
	callCount  int
}

func (f *fakeCompleter) Complete(_ context.Context, _ /*provider*/, model, prompt string) (string, error) {
	f.callCount++
	f.lastPrompt = prompt
	f.lastModel = model
	return f.reply, f.err
}

func settings() OptimizeSettings {
	return OptimizeSettings{Provider: "ollama-gpu", Model: "gemma-4-e4b-128k:latest"}
}

func TestLLMOptimizerAppliesModelDecision(t *testing.T) {
	fc := &fakeCompleter{reply: `Here you go:
{"inline": ["git_clone"], "deferred": ["gmail_send_email"], "dropped": ["weather_now"]}`}
	opt := NewLLMOptimizer(fc, settings())

	plan, err := opt.Optimize(context.Background(), catalog(), gitProfile(), LevelLLMCurate, Config{})
	require.NoError(t, err)

	// terminal is core → forced inline even though the model didn't mention it.
	assert.Contains(t, inlineNames(plan), "terminal")
	assert.Contains(t, inlineNames(plan), "git_clone")
	assert.ElementsMatch(t, []string{"gmail_send_email"}, deferredNames(plan))
	assert.Equal(t, []string{"weather_now"}, plan.Dropped)
	// git_commit omitted by the model → defaults to inline (never silently lost).
	assert.Contains(t, inlineNames(plan), "git_commit")
	// The user-chosen model was passed through to the completer.
	assert.Equal(t, "gemma-4-e4b-128k:latest", fc.lastModel)
}

func TestLLMOptimizerNeverDefersOrDropsCore(t *testing.T) {
	// Model wrongly tries to defer/drop the core "terminal" tool.
	fc := &fakeCompleter{reply: `{"inline": [], "deferred": ["terminal"], "dropped": ["terminal"]}`}
	plan, err := NewLLMOptimizer(fc, settings()).Optimize(context.Background(), catalog(), gitProfile(), LevelLLMCurate, Config{})
	require.NoError(t, err)
	assert.Contains(t, inlineNames(plan), "terminal")
	assert.NotContains(t, deferredNames(plan), "terminal")
	assert.NotContains(t, plan.Dropped, "terminal")
}

func TestLLMOptimizerFallsBackWhenUnconfigured(t *testing.T) {
	// No provider/model → heuristic fallback (scope behavior).
	opt := NewLLMOptimizer(&fakeCompleter{}, OptimizeSettings{})
	plan, err := opt.Optimize(context.Background(), catalog(), gitProfile(), LevelScope, Config{})
	require.NoError(t, err)
	assert.True(t, plan.Activated)
}

func TestLLMOptimizerFallsBackOnCompleterError(t *testing.T) {
	fc := &fakeCompleter{err: errors.New("provider down")}
	opt := NewLLMOptimizer(fc, settings())
	plan, err := opt.Optimize(context.Background(), catalog(), gitProfile(), LevelScope, Config{})
	require.NoError(t, err, "completer error must degrade to heuristic, not fail")
	assert.NotNil(t, plan)
}

func TestLLMOptimizerFallsBackOnGarbageReply(t *testing.T) {
	fc := &fakeCompleter{reply: "I cannot help with that."}
	plan, err := NewLLMOptimizer(fc, settings()).Optimize(context.Background(), catalog(), gitProfile(), LevelScope, Config{})
	require.NoError(t, err)
	assert.NotNil(t, plan)
}

func TestCustomPromptIsUsedAndRendered(t *testing.T) {
	fc := &fakeCompleter{reply: `{"inline":["git_clone"]}`}
	s := settings()
	s.Prompt = "ROLE={{.Role}} LEVEL={{.LevelName}}\n{{.Tools}}"
	_, err := NewLLMOptimizer(fc, s).Optimize(context.Background(), catalog(), gitProfile(), LevelLLMCurate, Config{})
	require.NoError(t, err)
	assert.Contains(t, fc.lastPrompt, "ROLE=gitea-researcher")
	assert.Contains(t, fc.lastPrompt, "LEVEL=llm-curate")
	assert.Contains(t, fc.lastPrompt, "git_clone: clone a git repository")
}

func TestSettingsFingerprintChangesWithPrompt(t *testing.T) {
	a := OptimizeSettings{Provider: "p", Model: "m", Prompt: "one"}
	b := OptimizeSettings{Provider: "p", Model: "m", Prompt: "two"}
	assert.NotEqual(t, a.Fingerprint(), b.Fingerprint())
}

func TestOptimizerVariantBustsCache(t *testing.T) {
	inner := &countingOptimizer{}
	opt := NewCachingOptimizer(inner)
	base := Config{ContextWindow: 1, OptimizerVariant: "v1"}
	_, _ = opt.Optimize(context.Background(), catalog(), gitProfile(), LevelLLMCurate, base)
	_, _ = opt.Optimize(context.Background(), catalog(), gitProfile(), LevelLLMCurate, base)
	assert.Equal(t, 1, inner.calls, "same variant → cached")

	changed := Config{ContextWindow: 1, OptimizerVariant: "v2"} // prompt/model changed
	_, _ = opt.Optimize(context.Background(), catalog(), gitProfile(), LevelLLMCurate, changed)
	assert.Equal(t, 2, inner.calls, "new variant → re-optimized")
}

func TestPromptTemplateFallsBackToDefault(t *testing.T) {
	assert.Equal(t, DefaultOptimizePrompt, OptimizeSettings{}.PromptTemplate())
	assert.True(t, strings.Contains(DefaultOptimizePrompt, "{{.Tools}}"))
}
