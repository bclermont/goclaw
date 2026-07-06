package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTtsTool_WebcallChannelType verifies that the tts tool returns an
// informational no-op result for webcall sessions instead of synthesising a
// file. Webcall channels handle TTS internally via LiveKit audio track
// publishing; creating a MEDIA: file would strip the reply content to empty,
// resulting in silence on the call.
func TestTtsTool_WebcallChannelType(t *testing.T) {
	tool := NewTtsTool(makeTTSManager("openai"))

	// Simulate a webcall execution context: channel type is "webcall".
	ctx := WithToolChannelType(context.Background(), "webcall")
	// Channel instance name is the webcall instance (not used for type check).
	ctx = WithToolChannel(ctx, "webcall-prod")

	result := tool.Execute(ctx, map[string]any{"text": "Hello, how can I help you?"})
	require.NotNil(t, result)

	// Must NOT be an error — the agent should see this as a successful
	// tool call so it doesn't retry or panic.
	assert.False(t, result.IsError, "webcall no-op should not be an error")

	// Must NOT produce a MEDIA: path — that would be stripped to empty content.
	assert.NotContains(t, result.ForLLM, "MEDIA:", "webcall no-op must not produce a MEDIA: path")
	assert.Empty(t, result.Media, "webcall no-op must not produce Media files")

	// Must contain guidance for the agent to reply with plain text.
	assert.True(t, strings.Contains(result.ForLLM, "voice") || strings.Contains(result.ForLLM, "plain text"),
		"webcall no-op ForLLM should instruct agent to reply with plain text, got: %s", result.ForLLM)
}

// TestTtsTool_NonWebcallChannelType verifies that a non-webcall channel type
// (e.g. "telegram") still reaches the normal synthesis path (covered elsewhere;
// this just asserts the guard doesn't fire for other types).
func TestTtsTool_NonWebcallChannelType_DoesNotEarlyReturn(t *testing.T) {
	// Use a manager with a provider that returns fake audio.
	tool := NewTtsTool(makeTTSManager("openai"))

	// Telegram channel type — should NOT hit the webcall guard.
	ctx := WithToolChannelType(context.Background(), "telegram")
	ctx = WithToolChannel(ctx, "telegram")

	result := tool.Execute(ctx, map[string]any{"text": "Hello"})
	require.NotNil(t, result)

	// The informational webcall message must NOT appear for telegram.
	assert.NotContains(t, result.ForLLM, "voice calls handle speech synthesis automatically",
		"telegram should not get the webcall no-op message")
}
