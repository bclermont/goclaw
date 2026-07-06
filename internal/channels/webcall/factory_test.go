package webcall_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/channels/webcall"
)

func TestFactory_DefaultConfig(t *testing.T) {
	t.Parallel()
	msgBus := bus.New()

	// webcall requires no credentials — nil creds must succeed.
	ch, err := webcall.Factory("webvoice", nil, nil, msgBus, nil)
	require.NoError(t, err)
	require.NotNil(t, ch)

	assert.Equal(t, "webvoice", ch.Name())
	assert.Equal(t, "webcall", ch.Type())
	assert.False(t, ch.IsRunning())
}

func TestFactory_WithConfig(t *testing.T) {
	t.Parallel()
	msgBus := bus.New()

	cfg := json.RawMessage(`{"max_call_seconds": 120, "tts_voice_id": "echo", "voice_agent_id": "my-agent"}`)
	ch, err := webcall.Factory("webvoice", nil, cfg, msgBus, nil)
	require.NoError(t, err)
	require.NotNil(t, ch)

	assert.Equal(t, "webvoice", ch.Name())
	assert.Equal(t, "webcall", ch.Type())
}

func TestFactoryWithAudio_NilAudioMgr(t *testing.T) {
	t.Parallel()
	msgBus := bus.New()

	factory := webcall.FactoryWithAudio(nil, nil)
	ch, err := factory("voice1", nil, nil, msgBus, nil)
	require.NoError(t, err)
	assert.NotNil(t, ch)
}

func TestChannel_IsAllowed_WithAllowlist(t *testing.T) {
	t.Parallel()
	msgBus := bus.New()

	cfg := json.RawMessage(`{"allow_from": ["user123", "user456"]}`)
	ch, err := webcall.Factory("voice", nil, cfg, msgBus, nil)
	require.NoError(t, err)

	assert.True(t, ch.IsAllowed("user123"))
	assert.True(t, ch.IsAllowed("user456"))
	assert.False(t, ch.IsAllowed("user999"))
}

func TestChannel_IsAllowed_EmptyAllowlist(t *testing.T) {
	t.Parallel()
	msgBus := bus.New()

	// Empty allowlist means all are allowed.
	ch, err := webcall.Factory("voice", nil, nil, msgBus, nil)
	require.NoError(t, err)

	assert.True(t, ch.IsAllowed("anyone"))
	assert.True(t, ch.IsAllowed("user123"))
}

func TestChannel_TypeConst(t *testing.T) {
	t.Parallel()
	msgBus := bus.New()

	ch, err := webcall.Factory("v", nil, nil, msgBus, nil)
	require.NoError(t, err)
	assert.Equal(t, "webcall", ch.Type())
}

func TestFactory_WithLiveKitConfig(t *testing.T) {
	t.Parallel()
	msgBus := bus.New()

	cfg := json.RawMessage(`{
		"livekit_url": "wss://livekit.robotinfra.com",
		"livekit_api_key": "mykey",
		"livekit_api_secret": "mysecret",
		"voice_agent_id": "voice-bot",
		"max_call_seconds": 180,
		"tts_voice_id": "nova"
	}`)
	ch, err := webcall.Factory("webvoice", nil, cfg, msgBus, nil)
	require.NoError(t, err)
	require.NotNil(t, ch)

	assert.Equal(t, "webvoice", ch.Name())
	assert.Equal(t, "webcall", ch.Type())
}

func TestFactory_InvalidConfig(t *testing.T) {
	t.Parallel()
	msgBus := bus.New()

	_, err := webcall.Factory("v", nil, json.RawMessage(`{invalid json`), msgBus, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode webcall config")
}
