# WebRTC Voice Call Channel (`webcall`)

The `webcall` channel lets a browser-based client open a live WebRTC voice
call to a goclaw agent. Incoming speech is transcribed via STT, routed through
the agent pipeline, and the reply is spoken back via TTS — fully hands-free,
with no Telegram account or external credentials required.

---

## Table of Contents

1. [Architecture](#architecture)
2. [Dependencies](#dependencies)
3. [Signaling Flow](#signaling-flow)
4. [Configuration Reference](#configuration-reference)
5. [Half-Duplex Behavior](#half-duplex-behavior)
6. [Audio Transcoding Pipeline](#audio-transcoding-pipeline)
7. [VAD — Voice Activity Detection](#vad--voice-activity-detection)
8. [Transcript as Session Data](#transcript-as-session-data)
9. [NAT Traversal and TURN](#nat-traversal-and-turn)
10. [Known Limitations](#known-limitations)

---

## Architecture

```
Browser (caller)
        │
        │  HTTP POST /v1/webcall/offer  { sdp, agent_id, user_id }
        │  HTTP POST /v1/webcall/ice    { candidate, call_id }
        ▼
  ┌───────────────────────────────────────┐
  │  Channel.handleOffer()                │
  │  – creates pion PeerConnection        │
  │  – SetRemoteDescription (SDP offer)   │
  │  – CreateAnswer → SetLocalDescription │
  │  – returns SDP answer + call_id       │
  └──────────────┬────────────────────────┘
                 │  pion/webrtc v4 (ICE / DTLS / SRTP)
                 ▼
  ┌───────────────────────────────────────┐
  │  runCallSession() — half-duplex loop  │
  │                                       │
  │  pc.OnTrack → rtpCh (Opus payloads)  │
  │                                       │
  │  collectUtterance()                   │
  │    VAD on payload length (isVoiced)   │
  │    → [][]byte Opus payloads           │
  │                                       │
  │  opusFramesToPCMWAV()                 │
  │    ffmpeg: OGG/Opus → 16kHz PCM WAV  │
  │                                       │
  │  audio.Manager.Transcribe()           │
  │    → transcript string                │
  │                                       │
  │  bus.PublishInbound()                 │
  │    → agent pipeline                   │
  │                                       │
  │  ac.replyCh ← agent reply text        │
  │                                       │
  │  audio.Manager.Synthesize()           │
  │    → mp3 audio bytes                  │
  │                                       │
  │  audioToOpusRTP()                     │
  │    ffmpeg: mp3 → 48kHz mono OGG/Opus  │
  │    extractOggOpusPackets → RTP pkts   │
  │                                       │
  │  track.WriteRTP()                     │
  └───────────────────────────────────────┘
```

The channel mounts its HTTP handlers on the main gateway mux via the
`WebhookChannel` interface — no extra port is required.

---

## Dependencies

### Go (compile-time)

| Package | Purpose |
|---------|---------|
| `github.com/pion/webrtc/v4` | ICE / DTLS / SRTP media transport + peer connection |
| `github.com/pion/rtp` | RTP packet assembly |

### Runtime

| Binary | Purpose |
|--------|---------|
| `ffmpeg` (with `libopus`) | Audio transcoding — Opus↔PCM↔mp3 |

**ffmpeg must be present in `PATH`.** If it is missing, goclaw logs a single
`security.webcall_ffmpeg_missing` warning the first time a voice call is
attempted and degrades gracefully: TTS is skipped and STT receives an empty
WAV. The server does not crash.

For the standard Docker image, ffmpeg must be added to the Dockerfile.

---

## Signaling Flow

```
Browser                                goclaw /v1/webcall/
  │                                         │
  │  1. getUserMedia (audio)                │
  │  2. createOffer → localSDP              │
  │─────── POST /offer { sdp, user_id } ──►│
  │                                         │  creates PeerConnection
  │                                         │  SetRemoteDescription(offer)
  │                                         │  CreateAnswer
  │◄──────── 200 { sdp, call_id } ─────────│
  │  3. setRemoteDescription(answer)        │
  │  4. ICE gathering                       │
  │─────── POST /ice { candidate, call_id}─►│  AddICECandidate
  │         (repeat for each candidate)     │
  │                                         │  ICE connectivity checks
  │◄══════════ DTLS / SRTP audio ══════════►│
  │                                         │  runCallSession()
```

No WebSocket is needed for signaling — plain HTTP POST is sufficient for
half-duplex voice call establishment.

---

## Configuration Reference

### Credentials

The `webcall` channel requires **no credentials**. Leave the credentials field
empty or omit it entirely when creating the channel instance.

### Config (stored in `channel_instances.config`)

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `allow_from` | `[]string` | `[]` (accept all) | Allowlist of user IDs; empty = accept any caller |
| `greeting` | string | `""` | Text spoken to the caller immediately after connection |
| `max_call_seconds` | integer | `300` | Hard call duration cap in seconds |
| `tts_voice_id` | string | `""` | TTS provider voice override |
| `stt_timeout_seconds` | integer | `30` | Per-utterance STT timeout |
| `voice_agent_id` | string | `"default"` | Agent key used to route inbound transcripts |
| `turn_urls` | `[]string` | `[]` | TURN server URLs (e.g. `["turn:coturn.example.com:3478"]`) |
| `turn_username` | string | `""` | TURN credential username |
| `turn_password` | string | `""` | TURN credential password (stored in config, consider encrypting) |

---

## Half-Duplex Behavior

The channel operates in half-duplex (push-to-talk-style) mode:

1. **Listen phase:** Collect incoming Opus RTP frames until VAD detects
   end-of-utterance (600 ms of silence after at least 100 ms of speech).
2. **Transcribe phase:** Decode buffered Opus frames to PCM WAV via ffmpeg
   and send to the configured STT provider.
3. **Think phase:** Publish the transcript to the agent pipeline and wait for
   a reply (up to `stt_timeout_seconds × 3`).
4. **Speak phase:** Synthesize the reply via TTS, encode to Opus via ffmpeg,
   and write RTP packets to the call audio track.
5. Repeat from step 1.

During the speak phase, incoming RTP is still received and buffered. The
buffer is drained at the start of the next listen phase, so brief overlap does
not lose audio. There is no barge-in detection; the caller must wait for the
agent to finish speaking before their next utterance is picked up.

---

## Audio Transcoding Pipeline

### Outbound (TTS → Opus RTP)

```
TTS provider → mp3 bytes
    │
    └─ ffmpeg -i pipe:0 -c:a libopus -ar 48000 -ac 1 -b:a 32k -frame_duration 20 -f ogg pipe:1
                │
                └─ extractOggOpusPackets() — demux OGG pages → raw Opus frames
                        │
                        └─ packOpusRTP() — split to 20ms RTP packets (PT=111, TS+=960)
                                │
                                └─ track.WriteRTP()
```

### Inbound STT (Opus RTP → PCM WAV)

```
pc.OnTrack → RTP read loop → rtpCh ([]byte payloads)
    │
    └─ collectUtterance() — VAD-gated frame collection
            │
            └─ wrapOpusInOgg() — build minimal OGG container (ID header + comment + pages)
                    │
                    └─ ffmpeg -f ogg -i pipe:0 -ar 16000 -ac 1 -c:a pcm_s16le -f wav pipe:1
                            │
                            └─ audio.Manager.Transcribe() → transcript string
```

### ffmpeg Absence Degradation

| Function | ffmpeg absent behavior |
|----------|----------------------|
| `encodeOpus` | returns error → `speakText` logs error, skips TTS for this turn |
| `decodeOpusToPCM` | returns error → `opusFramesToPCMWAV` logs warning, returns empty PCM WAV |
| STT on empty WAV | provider returns empty transcript → turn skipped silently |

One `security.webcall_ffmpeg_missing` warning is emitted per process
lifetime (deduplicated with `sync.Once`).

---

## VAD — Voice Activity Detection

The VAD operates on the incoming Opus RTP payload stream without decoding
(decoding per frame via ffmpeg would be too expensive for real-time use):

- **`isVoiced(payload []byte) bool`** — returns `true` if the payload is
  ≥ 10 bytes. Silent/DTX frames sent by the Opus codec are typically 1–3
  bytes; voiced frames are substantially larger.
- **Utterance start:** `vadMinFrames = 5` consecutive voiced frames (100 ms).
- **Utterance end:** `vadSilenceFrames = 30` consecutive non-voiced polls
  (600 ms at 20 ms/poll).

---

## Transcript as Session Data

Each call is treated as a goclaw session keyed by:

```
sessions.BuildSessionKey(agentKey, "webcall", sessions.PeerDirect, userID)
```

where `userID` is the value passed in the `user_id` field of the offer request.
Conversation history accumulates in the session exactly like any other channel.
Memory consolidation and summarization apply normally.

The agent reply is delivered to the call via `Channel.Send()`, which routes the
text into the active call's `replyCh`. If the call ended before the agent
finished generating, the message is silently dropped.

---

## NAT Traversal and TURN

For production deployments where the browser and goclaw server are separated
by NAT (common in home or corporate networks), configure a TURN relay server:

```json
{
  "turn_urls": ["turn:coturn.example.com:3478"],
  "turn_username": "myuser",
  "turn_password": "mypassword"
}
```

**Self-hosted TURN** — [coturn](https://github.com/coturn/coturn) is the
standard open-source TURN server. Install it on a server with a public IP,
configure a shared secret, and point `turn_urls` at it.

When no TURN URLs are configured, goclaw falls back to Google's public STUN
server (`stun:stun.l.google.com:19302`) for reflexive candidate discovery
only. This works when at least one side has a publicly reachable IP but fails
with symmetric NAT on both sides.

---

## Known Limitations

### Half-duplex only

There is no barge-in detection. The caller must wait for the agent's TTS
reply to finish before their next utterance is collected. Full-duplex support
requires per-frame Opus decoding and a continuous speech recognizer, which is
out of scope for the current implementation.

### One active call per `call_id`

Each browser session gets a unique `call_id`. Multiple concurrent calls from
different browsers to the same channel instance are supported (each stored in
`activeCalls sync.Map`). However, the session key is derived from `user_id`,
so two concurrent calls from the same user will share the same agent session.

### No authentication

The current HTTP signaling handlers validate `user_id` against `allow_from`
but do not verify caller identity cryptographically. For production use, place
the webcall endpoint behind the gateway's token authentication middleware or
add an API key check appropriate to your deployment.
