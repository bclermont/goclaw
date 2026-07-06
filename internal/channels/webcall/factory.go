// Package webcall implements a goclaw channel that accepts browser-initiated
// WebRTC voice calls via LiveKit. The browser joins a LiveKit room using the
// LiveKit JS SDK; goclaw joins the same room as an agent participant.
//
// Inbound audio is transcribed via STT, routed through the agent pipeline, and
// the reply is synthesised via TTS and published back as an audio track.
// The channel also mounts HTTP handlers on the main gateway mux via the
// WebhookChannel interface to issue LiveKit JWT tokens for browser clients.
package webcall

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/nextlevelbuilder/goclaw/internal/audio"
	"github.com/nextlevelbuilder/goclaw/internal/audio/proxy_stt"
	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/channels"
	"github.com/nextlevelbuilder/goclaw/internal/channels/media"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// webcallConfig holds the non-secret per-instance config.
// Stored in channel_instances.config.
// Note: agent routing uses channel_instances.agent_id (set at creation time);
// there is no agent_key or voice_agent_id field here.
type webcallConfig struct {
	LiveKitURL       string   `json:"livekit_url"`        // wss://livekit.robotinfra.com
	LiveKitAPIKey    string   `json:"livekit_api_key"`    // LiveKit API key
	LiveKitAPISecret string   `json:"livekit_api_secret"` // LiveKit API secret
	Greeting         string   `json:"greeting,omitempty"`           // spoken greeting at call start
	MaxCallSeconds   int      `json:"max_call_seconds,omitempty"`   // hard call duration cap (0 = 300)
	AllowFrom        []string `json:"allow_from,omitempty"`         // allowlist of user IDs (empty = accept all)
	TTSVoiceID       string   `json:"tts_voice_id,omitempty"`       // TTS provider voice override
	STTProxyURL      string   `json:"stt_proxy_url,omitempty"`      // STT proxy endpoint
	STTAPIKey        string   `json:"stt_api_key,omitempty"`        // STT API key
	STTTenantID      string   `json:"stt_tenant_id,omitempty"`      // STT tenant ID
	STTTimeoutSecs   int      `json:"stt_timeout_seconds,omitempty"`   // per-utterance STT timeout (0 = 30)
	MaxUtteranceSecs int      `json:"max_utterance_seconds,omitempty"` // hard cap on utterance collection (0 = 30)
	VADThresholdBytes int     `json:"vad_threshold_bytes,omitempty"`   // min payload bytes to classify a frame as voiced (0 = 80)
}

// FactoryWithAudio returns a ChannelFactory for webcall instances.
// audioMgr is injected for STT (Transcribe) and TTS (Synthesize); may be nil if unconfigured.
// sessStore is used to record the greeting as an assistant message in the session history.
func FactoryWithAudio(audioMgr *audio.Manager, sessStore store.SessionStore) channels.ChannelFactory {
	return func(name string, creds json.RawMessage, cfg json.RawMessage,
		msgBus *bus.MessageBus, pairingSvc store.PairingStore) (channels.Channel, error) {
		return buildChannel(name, cfg, msgBus, audioMgr, sessStore)
	}
}

// Factory creates a webcall channel without audio (STT/TTS disabled).
// Intended for testing or minimal deployments.
func Factory(name string, _ json.RawMessage, cfg json.RawMessage,
	msgBus *bus.MessageBus, _ store.PairingStore) (channels.Channel, error) {
	return buildChannel(name, cfg, msgBus, nil, nil)
}

func buildChannel(name string, cfg json.RawMessage, msgBus *bus.MessageBus, audioMgr *audio.Manager, sessStore store.SessionStore) (channels.Channel, error) {
	var ic webcallConfig
	if len(cfg) > 0 {
		if err := json.Unmarshal(cfg, &ic); err != nil {
			return nil, fmt.Errorf("decode webcall config: %w", err)
		}
	}

	maxSecs := ic.MaxCallSeconds
	if maxSecs <= 0 {
		maxSecs = defaultMaxCallSeconds
	}
	sttTimeout := ic.STTTimeoutSecs
	if sttTimeout <= 0 {
		sttTimeout = defaultSTTTimeoutSecs
	}
	vadThreshold := ic.VADThresholdBytes
	if vadThreshold <= 0 {
		vadThreshold = defaultVADThresholdBytes
	}

	// Register a channel-scoped STT provider for "webcall" when stt_proxy_url is
	// configured in the channel instance config. Without this step the audio.Manager
	// cannot find a provider for the "webcall" channel context and returns
	// "chain is empty".
	if audioMgr != nil && ic.STTProxyURL != "" {
		sttProvider := proxy_stt.NewProvider(media.STTConfig{
			ProxyURL:       ic.STTProxyURL,
			APIKey:         ic.STTAPIKey,
			TenantID:       ic.STTTenantID,
			TimeoutSeconds: sttTimeout,
		})
		audioMgr.RegisterChannelSTT(channelTypeWebCall, sttProvider)
		slog.Info("webcall: registered STT provider",
			"channel", name,
			"provider", sttProvider.Name(),
			"stt_proxy_url", ic.STTProxyURL,
			"stt_tenant_id", ic.STTTenantID,
		)
	} else if audioMgr != nil {
		slog.Warn("webcall: no stt_proxy_url in channel config — STT will use default manager chain",
			"channel", name)
	} else {
		slog.Warn("webcall: no audio manager — STT/TTS disabled", "channel", name)
	}

	ch := newChannel(channelParams{
		name:             name,
		livekitURL:       ic.LiveKitURL,
		livekitAPIKey:    ic.LiveKitAPIKey,
		livekitAPISecret: ic.LiveKitAPISecret,
		allowFrom:        ic.AllowFrom,
		greeting:         ic.Greeting,
		maxCallSecs:      maxSecs,
		maxUtteranceSecs:  ic.MaxUtteranceSecs,
		ttsVoiceID:        ic.TTSVoiceID,
		sttTimeoutSecs:    sttTimeout,
		vadThresholdBytes: vadThreshold,
		msgBus:           msgBus,
		audioMgr:         audioMgr,
		sessStore:        sessStore,
	})
	return ch, nil
}
