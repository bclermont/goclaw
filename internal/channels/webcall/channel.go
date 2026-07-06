package webcall

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/livekit/protocol/auth"
	pionwebrtc "github.com/pion/webrtc/v4"

	"github.com/nextlevelbuilder/goclaw/internal/audio"
	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/channels"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

const (
	// defaultMaxCallSeconds is the hard duration cap per call (5 minutes).
	defaultMaxCallSeconds = 300
	// defaultSTTTimeoutSecs is the per-utterance STT timeout.
	defaultSTTTimeoutSecs = 30
	// agentParticipantIdentity is the fixed identity used when goclaw joins a room.
	agentParticipantIdentity = "goclaw-agent"
	// tokenTTL is the validity period for LiveKit JWT tokens issued to browsers.
	tokenTTL = 30 * time.Minute
)

// channelParams holds construction parameters for a Channel.
type channelParams struct {
	name             string
	livekitURL       string
	livekitAPIKey    string
	livekitAPISecret string
	allowFrom        []string
	greeting         string
	maxCallSecs      int
	maxUtteranceSecs  int
	ttsVoiceID        string
	sttTimeoutSecs    int
	vadThresholdBytes int
	msgBus            *bus.MessageBus
	audioMgr          *audio.Manager
	sessStore         store.SessionStore
}

// agentReply bundles the text reply with the TTS options derived from the
// dispatching agent's audio configuration (voice ID, model, etc.).
type agentReply struct {
	text    string
	ttsOpts audio.TTSOptions
}

// activeRoomState bundles the per-room LiveKit connection and reply routing.
type activeRoomState struct {
	room    *lksdk.Room
	replyCh chan agentReply // receives outbound text + TTS options to speak; buffered
}

// Channel implements channels.Channel and channels.WebhookChannel for browser
// WebRTC voice calls via LiveKit. It mounts HTTP handlers on the main gateway
// mux to issue LiveKit JWT tokens and join rooms as an agent participant.
// Agent routing uses BaseChannel.AgentID() which is set by InstanceLoader from
// the channel_instances.agent_id column — no config field is needed.
type Channel struct {
	*channels.BaseChannel

	livekitURL       string
	livekitAPIKey    string
	livekitAPISecret string
	greeting         string
	maxCallSecs      int
	maxUtteranceSecs  int
	ttsVoiceID        string
	sttTimeoutSecs    int
	vadThresholdBytes int
	audioMgr          *audio.Manager
	sessStore         store.SessionStore

	// activeRooms maps room name → *activeRoomState. Protected by sync.Map.
	activeRooms sync.Map

	stopCh chan struct{}
}

// newChannel constructs a Channel from params.
func newChannel(p channelParams) *Channel {
	base := channels.NewBaseChannel(channels.TypeWebCall, p.msgBus, p.allowFrom)
	base.SetName(p.name)

	return &Channel{
		BaseChannel:      base,
		livekitURL:       p.livekitURL,
		livekitAPIKey:    p.livekitAPIKey,
		livekitAPISecret: p.livekitAPISecret,
		greeting:         p.greeting,
		maxCallSecs:      p.maxCallSecs,
		maxUtteranceSecs:  p.maxUtteranceSecs,
		ttsVoiceID:        p.ttsVoiceID,
		sttTimeoutSecs:    p.sttTimeoutSecs,
		vadThresholdBytes: p.vadThresholdBytes,
		audioMgr:          p.audioMgr,
		sessStore:         p.sessStore,
		stopCh:           make(chan struct{}),
	}
}

// Name returns the channel instance name.
func (c *Channel) Name() string { return c.BaseChannel.Name() }

// Type returns the channel platform type.
func (c *Channel) Type() string { return channels.TypeWebCall }

// IsAllowed reports whether the given sender ID is permitted.
func (c *Channel) IsAllowed(senderID string) bool { return c.BaseChannel.IsAllowed(senderID) }

// IsRunning reports whether the channel is active.
func (c *Channel) IsRunning() bool { return c.BaseChannel.IsRunning() }

// Start marks the channel healthy. For webcall, there is no outbound connection
// to establish — the channel passively waits for inbound token requests.
func (c *Channel) Start(_ context.Context) error {
	c.MarkStarting("Registering webcall HTTP handlers")
	c.SetRunning(true)
	c.MarkHealthy("Ready to issue LiveKit tokens at /v1/webcall/token")
	slog.Info("webcall channel started", "channel", c.Name(), "build_marker", "WEBCALL_NEW_BINARY_MARKER_20260625")
	return nil
}

// Stop marks the channel stopped and disconnects all active LiveKit rooms.
func (c *Channel) Stop(_ context.Context) error {
	slog.Info("stopping webcall channel", "channel", c.Name())
	c.SetRunning(false)
	c.MarkStopped("Stopped")

	select {
	case <-c.stopCh:
	default:
		close(c.stopCh)
	}

	// Disconnect all active rooms.
	c.activeRooms.Range(func(_, v any) bool {
		if rs, ok := v.(*activeRoomState); ok {
			rs.room.Disconnect()
		}
		return true
	})
	return nil
}

// Send delivers a text reply into the active room as TTS audio.
// ChatID is the LiveKit room name for webcall sessions.
// The context may carry an AgentAudioSnapshot (set by the outbound dispatcher)
// that overrides the channel-level TTS voice with the agent's configured voice.
func (c *Channel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	v, ok := c.activeRooms.Load(msg.ChatID)
	if !ok {
		slog.Warn("webcall Send: no active room for chat_id, dropping reply",
			"channel", c.Name(), "chat_id", msg.ChatID)
		return nil
	}
	rs, ok := v.(*activeRoomState)
	if !ok {
		return fmt.Errorf("webcall: invalid room state type for chat_id %s", msg.ChatID)
	}

	slog.Info("webcall: Send called",
		"channel", c.Name(),
		"chat_id", msg.ChatID,
		"content_len", len(msg.Content),
		"content_preview", func() string {
			if len(msg.Content) > 120 {
				return msg.Content[:120] + "..."
			}
			return msg.Content
		}(),
	)

	// Build TTS options, preferring the agent's configured voice over the
	// channel-level default. The AgentAudioSnapshot is injected by the outbound
	// dispatcher (channels.dispatchOutbound) from the agent's OtherConfig JSON.
	ttsOpts := audio.TTSOptions{
		Voice:  c.ttsVoiceID,
		Format: "mp3",
	}
	if snap, snapOK := store.AgentAudioFromCtx(ctx); snapOK && len(snap.OtherConfig) > 0 {
		var agentCfg struct {
			TTSVoiceID string `json:"tts_voice_id,omitempty"`
			TTSModelID string `json:"tts_model_id,omitempty"`
		}
		if err := json.Unmarshal(snap.OtherConfig, &agentCfg); err == nil {
			if agentCfg.TTSVoiceID != "" {
				ttsOpts.Voice = agentCfg.TTSVoiceID
			}
			if agentCfg.TTSModelID != "" {
				ttsOpts.Model = agentCfg.TTSModelID
			}
		}
	}

	slog.Info("webcall: Send TTS options resolved",
		"channel", c.Name(),
		"chat_id", msg.ChatID,
		"tts_voice", ttsOpts.Voice,
		"tts_model", ttsOpts.Model,
		"tts_format", ttsOpts.Format,
	)

	select {
	case rs.replyCh <- agentReply{text: msg.Content, ttsOpts: ttsOpts}:
	default:
		slog.Warn("webcall Send: reply channel full, dropping", "channel", c.Name())
	}
	return nil
}

// WebhookHandler implements channels.WebhookChannel. Returns the HTTP handler
// that issues LiveKit JWT tokens for browser clients.
// Both POST and OPTIONS (CORS preflight) are registered to support
// external third-party clients calling from any origin.
func (c *Channel) WebhookHandler() (string, http.Handler) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/webcall/token", c.handleToken)
	mux.HandleFunc("OPTIONS /v1/webcall/token", c.handleToken)
	return "/v1/webcall/", mux
}

// tokenRequest is the JSON body expected at POST /v1/webcall/token.
type tokenRequest struct {
	RoomName string `json:"room_name"`
	// UserID is accepted for backward compatibility but is NOT used as the
	// participant identity. The actual identity is always resolved from the
	// authenticated request context (X-GoClaw-User-Id header), falling back
	// to this field only when the header is absent.
	UserID string `json:"user_id"`
	// SessionKey is the existing chat session key the browser client is already
	// using (e.g. "agent:default:ws:direct:<uuid>"). When provided the voice
	// call will reuse that session so conversation history is shared with the
	// text chat view. When absent a new webcall-scoped session is created.
	SessionKey string `json:"session_key"`
	// VADThreshold overrides the channel-level VAD silence threshold (in bytes)
	// for this specific call. Zero or absent means use the channel default.
	VADThreshold int `json:"vad_threshold,omitempty"`
}

// tokenResponse is the JSON body returned from POST /v1/webcall/token.
type tokenResponse struct {
	Token string `json:"token"`
	URL   string `json:"url"`
	Room  string `json:"room"`
}

// writeTokenError writes a structured JSON error response for the token endpoint.
func writeTokenError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if encErr := json.NewEncoder(w).Encode(map[string]string{"error": msg}); encErr != nil {
		slog.Warn("webcall: encode error response", "error", encErr)
	}
}

// setCORSHeaders sets the CORS headers required for external third-party clients
// to access the token endpoint from any origin.
func setCORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-GoClaw-User-Id")
}

// handleToken issues a LiveKit JWT token for a browser client and joins the
// room as the agent participant before returning the token to the browser.
// CORS headers are always set to support external third-party clients.
func (c *Channel) handleToken(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)

	// Handle OPTIONS preflight request.
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	var req tokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.Warn("webcall: invalid token request", "channel", c.Name(), "error", err)
		writeTokenError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.RoomName == "" {
		writeTokenError(w, http.StatusBadRequest, "room_name is required")
		return
	}

	slog.Info("webcall: handleToken request received",
		"channel", c.Name(),
		"room_name", req.RoomName,
		"session_key", req.SessionKey,
		"user_id_body", req.UserID,
		"tenant_id", c.TenantID(),
	)

	// Resolve participant identity: prefer the value injected into the request
	// context by the gateway auth middleware (X-GoClaw-User-Id header), which
	// is validated and may have owner overrides applied. Fall back to the
	// request body field only when the context value is absent (e.g. pairing
	// flows that do not set the header).
	participantID := store.UserIDFromContext(r.Context())
	if participantID == "" {
		participantID = r.Header.Get("X-GoClaw-User-Id")
		if participantID != "" {
			if err := store.ValidateUserID(participantID); err != nil {
				slog.Warn("security.webcall_token_invalid_user_id",
					"channel", c.Name(), "error", err)
				participantID = ""
			}
		}
	}
	if participantID == "" {
		participantID = req.UserID
	}
	if participantID == "" {
		writeTokenError(w, http.StatusBadRequest, "user identity could not be resolved — provide X-GoClaw-User-Id header")
		return
	}

	if !c.IsAllowed(participantID) {
		slog.Warn("security.webcall_token_rejected_allowlist",
			"channel", c.Name(), "user_id", participantID)
		writeTokenError(w, http.StatusForbidden, "forbidden")
		return
	}

	if c.livekitAPIKey == "" || c.livekitAPISecret == "" {
		slog.Error("webcall: LiveKit API credentials not configured", "channel", c.Name())
		writeTokenError(w, http.StatusServiceUnavailable, "server not configured")
		return
	}

	// Generate LiveKit JWT for the browser user.
	at := auth.NewAccessToken(c.livekitAPIKey, c.livekitAPISecret).
		SetIdentity(participantID).
		SetValidFor(tokenTTL).
		SetVideoGrant(&auth.VideoGrant{
			RoomJoin: true,
			Room:     req.RoomName,
		})
	token, err := at.ToJWT()
	if err != nil {
		slog.Error("webcall: generate LiveKit token", "channel", c.Name(), "error", err)
		writeTokenError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	// Resolve VAD threshold: use request value when provided, fall back to channel default.
	vadThreshold := req.VADThreshold
	if vadThreshold <= 0 {
		vadThreshold = c.vadThresholdBytes
	}

	// Join the room as the agent if not already in it.
	if err := c.ensureAgentInRoom(req.RoomName, participantID, req.SessionKey, vadThreshold); err != nil {
		slog.Error("webcall: join room as agent", "channel", c.Name(), "room", req.RoomName, "error", err)
		writeTokenError(w, http.StatusInternalServerError, "failed to join room")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(tokenResponse{
		Token: token,
		URL:   c.livekitURL,
		Room:  req.RoomName,
	}); encErr != nil {
		slog.Warn("webcall: encode token response", "error", encErr)
	}
}

// ensureAgentInRoom connects the gateway to the LiveKit room as an agent participant
// if it is not already connected. Idempotent — concurrent calls for the same
// room name are safe via sync.Map.LoadOrStore.
// sessionKey is the existing chat session key to reuse; pass "" to auto-generate.
// vadThreshold overrides the channel-level VAD threshold for this call; pass 0
// to use the channel default.
func (c *Channel) ensureAgentInRoom(roomName, userID, sessionKey string, vadThreshold int) error {
	// Fast path: already connected.
	if _, exists := c.activeRooms.Load(roomName); exists {
		return nil
	}

	// AgentID() returns the agent_key (slug) resolved by InstanceLoader from
	// channel_instances.agent_id — no config field needed.
	agentKey := c.AgentID()
	if agentKey == "" {
		slog.Warn("webcall: agent_key not resolved (channel_instances.agent_id may be unset), falling back to \"default\"",
			"channel", c.Name(),
		)
		agentKey = "default"
	}

	rtpCh := make(chan []byte, rtpBufferSize)
	disconnected := make(chan struct{}, 1)
	replyCh := make(chan agentReply, 1)

	cb := &lksdk.RoomCallback{
		OnDisconnected: func() {
			slog.Info("webcall: agent disconnected from room",
				"channel", c.Name(), "room", roomName)
			select {
			case disconnected <- struct{}{}:
			default:
			}
		},
		ParticipantCallback: lksdk.ParticipantCallback{
			OnTrackSubscribed: func(track *pionwebrtc.TrackRemote, _ *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
				if track.Kind() != pionwebrtc.RTPCodecTypeAudio {
					return
				}
				slog.Info("webcall: remote audio track subscribed",
					"channel", c.Name(), "room", roomName,
					"participant", rp.Identity(), "codec", track.Codec().MimeType)
				go func() {
					for {
						pkt, _, readErr := track.ReadRTP()
						if readErr != nil {
							select {
							case disconnected <- struct{}{}:
							default:
							}
							return
						}
						select {
						case rtpCh <- pkt.Payload:
						default:
							// Drop frame if buffer full — don't block RTP reader.
						}
					}
				}()
			},
		},
	}

	room, err := lksdk.ConnectToRoom(c.livekitURL, lksdk.ConnectInfo{
		APIKey:              c.livekitAPIKey,
		APISecret:           c.livekitAPISecret,
		RoomName:            roomName,
		ParticipantIdentity: agentParticipantIdentity,
		ParticipantName:     "GoClaw Agent",
		ParticipantKind:     lksdk.ParticipantAgent,
	}, cb)
	if err != nil {
		return fmt.Errorf("connect to room %s: %w", roomName, err)
	}

	rs := &activeRoomState{room: room, replyCh: replyCh}

	// Store unconditionally — if another goroutine raced and stored first, we
	// disconnect the duplicate and keep the existing one.
	if _, loaded := c.activeRooms.LoadOrStore(roomName, rs); loaded {
		room.Disconnect()
		return nil
	}

	slog.Info("webcall: agent joined room", "channel", c.Name(), "room", roomName)

	// Use the channel's own tenant ID so that session history lookups inside
	// runCallSession use the correct tenant scope.  The call uses a fresh
	// Background context (it outlives the HTTP request), so we attach the
	// tenant ID from the channel instance rather than from reqCtx — reqCtx
	// may carry the master-tenant ID when the request is routed through the
	// gateway without a proper tenant scope.
	baseCtx := store.WithTenantID(context.Background(), c.TenantID()) //nolint:contextcheck // intentional: call outlives HTTP request
	callCtx, cancel := context.WithTimeout(baseCtx, time.Duration(c.maxCallSecs)*time.Second)
	go func() {
		defer cancel()
		defer c.activeRooms.Delete(roomName)
		defer room.Disconnect()
		runCallSession(callCtx,
			slog.With("channel", c.Name(), "room", roomName),
			c, room, roomName, agentKey, userID, sessionKey, rtpCh, replyCh, disconnected,
			c.sessStore, vadThreshold)
		slog.Info("webcall: call session ended", "room", roomName)
	}()

	return nil
}
