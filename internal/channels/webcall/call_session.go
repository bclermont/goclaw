package webcall

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"time"

	lksdk "github.com/livekit/server-sdk-go/v2"
	pionwebrtc "github.com/pion/webrtc/v4"

	"github.com/nextlevelbuilder/goclaw/internal/audio"
	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/sessions"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

const (
	// rtpBufferSize is the capacity of the RTP payload channel.
	rtpBufferSize = 256
	// channelTypeWebCall is a local copy of the channel type constant to
	// avoid an import cycle with the channels package.
	channelTypeWebCall = "webcall"
)

// runCallSession drives the half-duplex turn loop for a single LiveKit room session:
//
//  1. Receive remote Opus RTP payloads from rtpCh (fed by OnTrackSubscribed).
//  2. Buffer frames until energy-based VAD detects end-of-utterance.
//  3. Wrap Opus payloads in OGG container → decode via ffmpeg to 16 kHz PCM WAV → STT.
//  4. Publish transcript to the agent via bus.PublishInbound.
//  5. Wait for the agent reply on replyCh (populated by Channel.Send).
//  6. Synthesise TTS → publish OGG/Opus as a LiveKit audio track.
//  7. Repeat until context cancelled or room disconnects.
func runCallSession(
	ctx context.Context,
	log *slog.Logger,
	ch *Channel,
	room *lksdk.Room,
	roomName, agentKey, userID, sessionKey string,
	rtpCh <-chan []byte,
	replyCh <-chan agentReply,
	disconnected <-chan struct{},
	sessStore store.SessionStore,
	vadThreshold int,
) {
	// Reuse the caller-supplied session key when the browser passes one (e.g.
	// the existing WS chat session). Fall back to a webcall-scoped key so that
	// standalone voice calls still get their own persistent thread.
	if sessionKey == "" {
		sessionKey = sessions.BuildSessionKey(agentKey, channelTypeWebCall, sessions.PeerDirect, userID)
	}
	callStart := time.Now()

	log.Info("webcall: call session loop started",
		"room", roomName,
		"agent_key", agentKey,
		"user_id", userID,
		"session_key", sessionKey,
		"stt_timeout_secs", ch.sttTimeoutSecs,
		"agent_reply_timeout_secs", ch.sttTimeoutSecs*3,
		"has_greeting", ch.greeting != "",
		"has_audio_mgr", ch.audioMgr != nil,
	)

	// Greet caller if configured, but only on a fresh session (no prior messages).
	// Skipping the greeting on reconnect prevents the caller from hearing it
	// repeatedly when they reload the page or the browser reconnects.
	//
	// The ctx must carry the tenant ID (propagated from the HTTP request context
	// by ensureAgentInRoom) so that the store looks up history under the correct
	// tenant scope. Without this, sessions belonging to a real tenant would not
	// be found (the store filters by tenant_id) and the greeting would always play.
	if ch.greeting != "" && ch.audioMgr != nil {
		var hasHistory bool
		var historyLen int
		if sessStore != nil {
			log.Info("webcall: calling GetHistory",
				"session_key", sessionKey,
				"tenant_id", store.TenantIDFromContext(ctx),
			)
			history := sessStore.GetHistory(ctx, sessionKey)
			historyLen = len(history)
			hasHistory = historyLen > 0
		}
		log.Info("webcall: greeting eligibility check",
			"session_key", sessionKey,
			"history_messages", historyLen,
			"has_history", hasHistory,
			"will_greet", !hasHistory,
		)
		// TODO(testing): history check temporarily disabled so greeting always plays
		//                even on reconnect — re-enable once audio path is verified.
		// if hasHistory {
		// 	log.Info("webcall: skipping greeting — session already has history",
		// 		"session_key", sessionKey,
		// 		"history_messages", historyLen)
		// } else {
		if true { //nolint:staticcheck // always greet during audio-path testing
			greetStart := time.Now()
			log.Info("webcall: sending greeting TTS", "greeting", ch.greeting)
			greetOpts := audio.TTSOptions{Voice: ch.ttsVoiceID, Format: "mp3"}
			// Apply agent voice/model overrides (same logic as the speak phase in channel.go).
			if snap, snapOK := store.AgentAudioFromCtx(ctx); snapOK && len(snap.OtherConfig) > 0 {
				var agentCfg struct {
					TTSVoiceID string `json:"tts_voice_id,omitempty"`
					TTSModelID string `json:"tts_model_id,omitempty"`
				}
				if err := json.Unmarshal(snap.OtherConfig, &agentCfg); err == nil {
					if agentCfg.TTSVoiceID != "" {
						greetOpts.Voice = agentCfg.TTSVoiceID
					}
					if agentCfg.TTSModelID != "" {
						greetOpts.Model = agentCfg.TTSModelID
					}
				}
			}
			if err := publishTTSToRoom(ctx, log, ch, room, ch.greeting, greetOpts); err != nil {
				log.Warn("webcall: greeting failed", "error", err, "elapsed", time.Since(callStart))
			} else {
				log.Info("webcall: greeting done", "greeting_duration", time.Since(greetStart), "elapsed", time.Since(callStart))
				// Record greeting as assistant message so the agent has context of
				// what was said at call start.
				if sessStore != nil {
					sessStore.AddMessage(ctx, sessionKey, providers.Message{
						Role:    "assistant",
						Content: ch.greeting,
					})
					log.Info("webcall: greeting recorded to session history", "session_key", sessionKey)
				}
			}
		}
	}

	log.Info("webcall: entering utterance collection loop", "elapsed", time.Since(callStart))

	for {
		select {
		case <-ctx.Done():
			log.Info("webcall: call session ended — context cancelled",
				"reason", ctx.Err(),
				"elapsed", time.Since(callStart),
			)
			return
		case <-disconnected:
			log.Info("webcall: call session ended — peer disconnected", "elapsed", time.Since(callStart))
			return
		default:
		}

		// Drain any stale frames that accumulated while the previous collection
		// was processing (STT, TTS, agent reply). Without draining, old unvoiced
		// background-noise frames would fill the buffer and the next
		// collectUtterance call would hard-timeout again with voiced_frames=0,
		// causing RTP starvation — no fresh audio ever reaches the VAD.
		drained := 0
		for {
			select {
			case <-rtpCh:
				drained++
			default:
				goto doneDrain
			}
		}
	doneDrain:
		if drained > 0 {
			log.Debug("webcall: drained stale RTP frames before collection", "drained", drained, "elapsed", time.Since(callStart))
		}

		log.Debug("webcall: waiting for utterance",
			"elapsed", time.Since(callStart),
			"vad_threshold_bytes", vadThreshold,
		)
		collectStart := time.Now()
		opusFrames, stopReason := collectUtterance(ctx, log, rtpCh, disconnected, ch.maxUtteranceSecs, vadThreshold)
		if opusFrames == nil {
			switch stopReason {
			case collectStopCtxDone:
				log.Info("webcall: call session ended — context cancelled",
					"reason", ctx.Err(),
					"elapsed", time.Since(callStart),
				)
				return
			case collectStopPeerGone:
				log.Info("webcall: call session ended — peer disconnected", "elapsed", time.Since(callStart))
				return
			default:
				// collectStopSilence: no voiced frames, user hasn't spoken yet — retry.
				log.Debug("webcall: no voiced utterance collected (silence/noise), retrying",
					"collect_duration", time.Since(collectStart),
					"elapsed", time.Since(callStart),
				)
				continue
			}
		}
		if len(opusFrames) == 0 {
			log.Debug("webcall: empty utterance collected, looping", "elapsed", time.Since(callStart))
			continue
		}

		// Count voiced frames in collected set for diagnostics.
		voicedInBatch := 0
		for _, frame := range opusFrames {
			if isVoiced(frame, vadThreshold) {
				voicedInBatch++
			}
		}
		log.Info("webcall: utterance collected",
			"frames", len(opusFrames),
			"voiced_frames", voicedInBatch,
			"collect_duration", time.Since(collectStart),
			"elapsed", time.Since(callStart),
		)

		wavData, err := opusFramesToPCMWAV(ctx, opusFrames)
		if err != nil {
			log.Error("webcall: opus decode", "error", err)
			continue
		}
		log.Info("webcall: WAV ready for STT",
			"wav_bytes", len(wavData),
			"voiced_frames", voicedInBatch,
			"elapsed", time.Since(callStart),
		)

		transcript, err := transcribeWAV(ctx, log, ch, wavData)
		if err != nil {
			log.Warn("webcall: STT failed", "error", err)
			continue
		}
		if transcript == "" {
			continue
		}
		log.Info("webcall: transcribed utterance", "text", transcript)

		ch.Bus().PublishInbound(bus.InboundMessage{
			Channel:    ch.Name(),
			SenderID:   userID,
			ChatID:     roomName,
			Content:    transcript,
			PeerKind:   string(sessions.PeerDirect),
			UserID:     userID,
			SessionKey: sessionKey,
			AgentID:    agentKey,
			TenantID:   ch.TenantID(),
		})

		replyTimeoutSecs := ch.sttTimeoutSecs * 3
		log.Info("webcall: waiting for agent reply",
			"timeout_secs", replyTimeoutSecs,
			"elapsed", time.Since(callStart),
		)
		replyWaitStart := time.Now()
		var reply agentReply
		select {
		case reply = <-replyCh:
			log.Info("webcall: agent reply received",
				"reply_wait", time.Since(replyWaitStart),
				"elapsed", time.Since(callStart),
				"reply_text_len", len(reply.text),
				"reply_text_preview", func() string {
					if len(reply.text) > 120 {
						return reply.text[:120] + "..."
					}
					return reply.text
				}(),
				"tts_voice", reply.ttsOpts.Voice,
				"tts_model", reply.ttsOpts.Model,
				"tts_format", reply.ttsOpts.Format,
			)
		case <-time.After(time.Duration(replyTimeoutSecs) * time.Second):
			log.Warn("webcall: agent reply timeout",
				"timeout_secs", replyTimeoutSecs,
				"reply_wait", time.Since(replyWaitStart),
				"elapsed", time.Since(callStart),
			)
			continue
		case <-ctx.Done():
			log.Info("webcall: call session ended while waiting for agent reply — context cancelled",
				"reason", ctx.Err(),
				"reply_wait", time.Since(replyWaitStart),
				"elapsed", time.Since(callStart),
			)
			return
		case <-disconnected:
			log.Info("webcall: call session ended while waiting for agent reply — peer disconnected",
				"reply_wait", time.Since(replyWaitStart),
				"elapsed", time.Since(callStart),
			)
			return
		}

		if reply.text == "" {
			continue
		}

		if err := publishTTSToRoom(ctx, log, ch, room, reply.text, reply.ttsOpts); err != nil {
			log.Warn("webcall: TTS publish failed", "error", err)
		}
	}
}

// defaultMaxUtteranceSecs is the hard cap on utterance collection when no
// explicit max is configured. Background noise can otherwise keep the VAD
// "alive" indefinitely.
const defaultMaxUtteranceSecs = 30

// collectStopReason describes why collectUtterance returned without a complete utterance.
type collectStopReason int

const (
	// collectStopSilence means no voiced frames were collected (background noise
	// or user has not spoken yet) — the caller should retry.
	collectStopSilence collectStopReason = iota
	// collectStopCtxDone means the context was cancelled — the caller must exit.
	collectStopCtxDone
	// collectStopPeerGone means the remote peer disconnected — the caller must exit.
	collectStopPeerGone
)

// collectUtterance reads Opus RTP payloads from rtpCh until an energy-based
// VAD detects end-of-utterance or the hard timeout fires.
// maxSecs controls the hard cap; pass 0 to use defaultMaxUtteranceSecs.
//
// Silence is detected by a wall-clock timer that resets every time a voiced
// frame arrives. Once the user has spoken at least vadMinFrames and then goes
// quiet for silenceGap, the utterance is considered complete. This is more
// reliable than frame-counting because background noise can produce small
// non-voiced frames at any rate.
//
// The second return value is a collectStopReason that is only meaningful when
// the first return value (frames) is nil. Callers must exit immediately on
// collectStopCtxDone or collectStopPeerGone.
func collectUtterance(ctx context.Context, log *slog.Logger, rtpCh <-chan []byte, disconnected <-chan struct{}, maxSecs, vadThreshold int) ([][]byte, collectStopReason) {
	const (
		vadMinFrames = 5    // at least 100 ms of voiced audio before ending
		silenceGap   = 1000 // ms of silence after speech ends utterance
	)

	if maxSecs <= 0 {
		maxSecs = defaultMaxUtteranceSecs
	}

	var (
		frames      [][]byte
		voicedCount int
	)

	hardDeadline := time.NewTimer(time.Duration(maxSecs) * time.Second)
	defer hardDeadline.Stop()

	// silenceTimer fires when the user has been quiet long enough to end the
	// utterance. It is created lazily once the first voiced frame arrives.
	var silenceTimer *time.Timer
	silenceTimerCh := make(<-chan time.Time) // nil channel — never fires until assigned

	// VAD summary: emit one debug log per second summarising frame counts and
	// average payload size so operators can tune minVoicedPayloadBytes without
	// flooding the log at ~50 lines/second.
	var (
		vadSummaryTicker           = time.NewTicker(time.Second)
		vadSummaryFrames           int
		vadSummaryVoiced           int
		vadSummaryTotalPayloadBytes int
	)
	defer vadSummaryTicker.Stop()

	collectStart := time.Now()
	for {
		select {
		case <-ctx.Done():
			log.Debug("webcall: collectUtterance — context cancelled",
				"reason", ctx.Err(),
				"frames_so_far", len(frames),
				"voiced_frames", voicedCount,
				"collect_duration", time.Since(collectStart),
			)
			return nil, collectStopCtxDone
		case <-disconnected:
			log.Info("webcall: collectUtterance — peer disconnected",
				"frames_so_far", len(frames),
				"voiced_frames", voicedCount,
				"collect_duration", time.Since(collectStart),
			)
			return nil, collectStopPeerGone
		case <-hardDeadline.C:
			log.Warn("webcall: collectUtterance — hard timeout reached, flushing",
				"max_secs", maxSecs,
				"frames_so_far", len(frames),
				"voiced_frames", voicedCount,
				"collect_duration", time.Since(collectStart),
			)
			if voicedCount < vadMinFrames {
				return nil, collectStopSilence
			}
			return frames, collectStopSilence

		case <-silenceTimerCh:
			log.Debug("webcall: collectUtterance — silence gap reached, flushing",
				"silence_gap_ms", silenceGap,
				"frames_so_far", len(frames),
				"voiced_frames", voicedCount,
				"collect_duration", time.Since(collectStart),
			)
			if voicedCount < vadMinFrames {
				return nil, collectStopSilence
			}
			return frames, collectStopSilence

		case <-vadSummaryTicker.C:
			if vadSummaryFrames > 0 {
				avgBytes := vadSummaryTotalPayloadBytes / vadSummaryFrames
				log.Debug("webcall: vad summary",
					"frames", vadSummaryFrames,
					"voiced", vadSummaryVoiced,
					"avg_payload_bytes", avgBytes,
					"threshold", vadThreshold,
				)
				vadSummaryFrames = 0
				vadSummaryVoiced = 0
				vadSummaryTotalPayloadBytes = 0
			}

		case payload := <-rtpCh:
			frames = append(frames, payload)
			vadSummaryFrames++
			vadSummaryTotalPayloadBytes += len(payload)
			if isVoiced(payload, vadThreshold) {
				vadSummaryVoiced++
				voicedCount++
				// Reset silence countdown on every voiced frame.
				if silenceTimer == nil {
					silenceTimer = time.NewTimer(silenceGap * time.Millisecond)
					silenceTimerCh = silenceTimer.C
				} else {
					if !silenceTimer.Stop() {
						select {
						case <-silenceTimer.C:
						default:
						}
					}
					silenceTimer.Reset(silenceGap * time.Millisecond)
				}
			}
		}
	}
}

// defaultVADThresholdBytes is the default minimum Opus RTP payload size to
// classify a frame as voiced. Silent/DTX frames are typically 1–3 bytes;
// background noise encodes in the 20–80 byte range. Genuine voiced speech
// frames are typically 100–300+ bytes. Operators can override this via
// vad_threshold_bytes in the channel instance config.
const defaultVADThresholdBytes = 80

func isVoiced(payload []byte, threshold int) bool {
	return len(payload) >= threshold
}

// transcribeWAV calls audio.Manager.Transcribe on the given WAV bytes.
func transcribeWAV(ctx context.Context, log *slog.Logger, ch *Channel, wavData []byte) (string, error) {
	if ch.audioMgr == nil {
		log.Warn("webcall: no audio manager configured, STT unavailable")
		return "", nil
	}

	sttChainNames := ch.audioMgr.STTProviderNames(channelTypeWebCall)
	log.Debug("webcall: STT provider chain",
		"channel_context", channelTypeWebCall,
		"chain", sttChainNames,
		"chain_len", len(sttChainNames),
		"wav_bytes", len(wavData),
	)

	tctx, cancel := context.WithTimeout(ctx, time.Duration(ch.sttTimeoutSecs)*time.Second)
	defer cancel()

	result, err := ch.audioMgr.Transcribe(audio.WithChannel(tctx, channelTypeWebCall), audio.STTInput{
		Bytes:    wavData,
		MimeType: "audio/wav",
		Filename: "utterance.wav",
	}, audio.STTOptions{})
	if err != nil {
		log.Warn("webcall: STT transcribe error",
			"error", err,
			"chain", sttChainNames,
			"wav_bytes", len(wavData),
		)
		return "", fmt.Errorf("transcribe: %w", err)
	}
	return result.Text, nil
}

// publishTTSToRoom synthesises replyText to OGG/Opus via ffmpeg, then publishes
// it as a LiveKit audio track in the given room. Degrades gracefully if ffmpeg
// or TTS is absent.
// opts carries the TTS voice/model preferences resolved per-reply (agent config
// takes priority over the channel-level default voice).
func publishTTSToRoom(ctx context.Context, log *slog.Logger, ch *Channel, room *lksdk.Room, replyText string, opts audio.TTSOptions) error {
	if ch.audioMgr == nil {
		log.Warn("webcall: no audio manager, TTS skipped")
		return nil
	}

	synth, err := ch.audioMgr.Synthesize(ctx, replyText, opts)
	if err != nil {
		return fmt.Errorf("synthesize TTS: %w", err)
	}
	if len(synth.Audio) == 0 {
		return nil
	}

	// Encode TTS audio to OGG/Opus via ffmpeg.
	oggData, err := encodeToOgg(ctx, synth.Audio)
	if err != nil {
		return fmt.Errorf("encode to ogg: %w", err)
	}
	if len(oggData) == 0 {
		return nil
	}

	// Create a local audio track backed by an OGG reader.
	doneCh := make(chan struct{})
	track, err := lksdk.NewLocalReaderTrack(
		io.NopCloser(bytes.NewReader(oggData)),
		pionwebrtc.MimeTypeOpus,
		lksdk.ReaderTrackWithOnWriteComplete(func() {
			close(doneCh)
		}),
	)
	if err != nil {
		return fmt.Errorf("create local ogg track: %w", err)
	}

	pub, err := room.LocalParticipant.PublishTrack(track, &lksdk.TrackPublicationOptions{
		Name: "agent-voice",
	})
	if err != nil {
		return fmt.Errorf("publish audio track: %w", err)
	}
	log.Info("webcall: published TTS audio track", "track_sid", pub.SID())

	// Wait for the track to finish writing (OGG EOF reached) before unpublishing.
	select {
	case <-doneCh:
	case <-ctx.Done():
	}

	if err := room.LocalParticipant.UnpublishTrack(pub.SID()); err != nil {
		log.Warn("webcall: unpublish TTS track", "error", err)
	}
	return nil
}
