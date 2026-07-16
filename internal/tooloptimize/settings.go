package tooloptimize

// System-config keys for the user-chosen generative-optimizer settings. They are
// plain key/value entries in goclaw's SystemConfigStore, so they are already
// settable through the existing config surface (e.g. the goclaw_system_config_set
// tool or the config API) without a dedicated endpoint.
const (
	SettingProvider = "tool_optimization.provider"
	SettingModel    = "tool_optimization.model"
	SettingPrompt   = "tool_optimization.prompt"
)

// LoadSettings builds OptimizeSettings from a key getter, keeping this package
// free of any store dependency. The caller adapts its config source (typically
// store.SystemConfigStore.Get) into the getter closure; a missing key should
// yield "".
func LoadSettings(get func(key string) string) OptimizeSettings {
	if get == nil {
		return OptimizeSettings{}
	}
	return OptimizeSettings{
		Provider: get(SettingProvider),
		Model:    get(SettingModel),
		Prompt:   get(SettingPrompt),
	}
}
