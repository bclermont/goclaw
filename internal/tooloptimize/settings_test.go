package tooloptimize

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoadSettingsFromGetter(t *testing.T) {
	store := map[string]string{
		SettingProvider: "openrouter",
		SettingModel:    "deepseek/deepseek-v4-flash",
		SettingPrompt:   "custom {{.Tools}}",
	}
	s := LoadSettings(func(k string) string { return store[k] })
	assert.Equal(t, "openrouter", s.Provider)
	assert.Equal(t, "deepseek/deepseek-v4-flash", s.Model)
	assert.Equal(t, "custom {{.Tools}}", s.Prompt)
	assert.True(t, s.Configured())
}

func TestLoadSettingsNilGetter(t *testing.T) {
	s := LoadSettings(nil)
	assert.False(t, s.Configured())
	assert.Equal(t, DefaultOptimizePrompt, s.PromptTemplate())
}
