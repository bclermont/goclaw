# WebCall Integration Guide for Third-Party Clients

## Overview

WebCall is goclaw's WebRTC voice call channel that enables real-time bidirectional conversation between a browser or mobile client and a goclaw agent. It combines:

- **LiveKit WebRTC** — Peer-to-peer audio transport with automatic NAT traversal
- **STT (Speech-to-Text)** — Real-time voice transcription via your configured STT provider
- **Agent Pipeline** — Full agent thinking, memory, and tool execution
- **TTS (Text-to-Speech)** — Agent reply synthesized back as audio
- **Session Integration** — Voice calls share the same session as text chat, so conversation history and memory are unified

The channel operates in **half-duplex** (push-to-talk) mode: the browser captures audio until silence is detected, sends it to goclaw for transcription, receives the agent's reply, synthesizes it as audio, and then listens for the next utterance. This prevents echo and keeps the interaction natural.

**No Telegram account, no external API keys, no special authentication** — just supply a user identity string and goclaw handles the rest.

---

## Endpoint Specification

### Token Request

**Path:** `POST /v1/webcall/token`

**Headers:**
- `Content-Type: application/json` (required)
- `X-GoClaw-User-Id` (optional) — Override the participant identity from the request body. Useful when the browser session is authenticated separately.

**CORS:** Enabled for all origins (wildcard `*`). OPTIONS preflight is supported.

**Request Body:**

```json
{
  "room_name": "unique-room-id-string",
  "user_id": "user@example.com",
  "session_key": "agent:my-agent:websocket:direct:user@example.com",
  "vad_threshold": 150
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `room_name` | string | Yes | Unique identifier for the LiveKit room. Must not contain spaces or special chars (use alphanumeric + `-` + `_`). |
| `user_id` | string | No* | Participant identity (email, user ID, etc.). Must be non-empty after resolving header + body. Falls back to the `X-GoClaw-User-Id` header if present. |
| `session_key` | string | No | Existing goclaw chat session key to reuse. When provided, the voice call will share conversation history with the text chat view. When omitted, a new auto-generated session is created. |
| `vad_threshold` | integer | No | VAD (Voice Activity Detection) silence threshold in bytes. Default: channel-level config (typically 10–50 bytes). Increase to skip short utterances (e.g., breathing), decrease to capture speech earlier. |

**Response (200 OK):**

```json
{
  "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "url": "wss://livekit.example.com",
  "room": "unique-room-id-string"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `token` | string | LiveKit JWT token. Valid for 30 minutes. Include in your LiveKit client `connect()` call. |
| `url` | string | LiveKit server WebSocket URL. Pass directly to your LiveKit client. |
| `room` | string | Room name (echoed back for confirmation). |

**Error Responses:**

- `400 Bad Request` — Missing or invalid `room_name`, or identity could not be resolved.
- `403 Forbidden` — User is not in the channel's allowlist.
- `503 Service Unavailable` — LiveKit credentials not configured in the channel instance.

### Example cURL

```bash
curl -X POST http://localhost:8080/v1/webcall/token \
  -H "Content-Type: application/json" \
  -H "X-GoClaw-User-Id: alice@example.com" \
  -d '{
    "room_name": "call-2024-06-26-001",
    "user_id": "alice@example.com",
    "session_key": "agent:my-agent:websocket:direct:alice@example.com"
  }'
```

---

## Session Key Format for Third-Party Clients

Session keys link the voice call to the existing chat session, enabling shared history and memory.

### Canonical Format

```
agent:{agentId}:{channelType}:{peerKind}:{identifier}
```

**For webcall (third-party clients), use:**

```
agent:{agentId}:websocket:direct:{identifier}
```

- **agentId:** The slug/key of the agent handling the call (e.g., `my-agent`, `default`). Must match an agent in the system.
- **channelType:** Use `websocket` for third-party voice integrations. This identifies the source channel for routing and history.
- **peerKind:** Always `direct` (one-to-one conversation).
- **identifier:** Unique identifier for the user or session context (e.g., email, user ID, UUID, session UUID).

### Examples

**Web app user (email-based):**
```
agent:my-agent:websocket:direct:user@example.com
```

**HTMX integration (session-based):**
```
agent:chatbot:websocket:htmx:session-123
```

**Mobile app (UUID-based):**
```
agent:voice-assistant:websocket:direct:550e8400-e29b-41d4-a716-446655440000
```

### Session Key Visibility

- **Chat Page:** Lists only sessions where the channel type is `websocket` (i.e., keys matching `agent:%:websocket:%`). This keeps the UI clean and separate from Telegram/Telegram/Discord conversations.
- **Sessions Page (Admin):** Lists all session keys regardless of format. Useful for debugging and management.

---

## How to Request a Token from Your External App

### Step 1: Prepare Request Parameters

```typescript
const roomName = `call-${Date.now()}`; // or any unique ID
const userId = "user@example.com"; // your app's user ID
const sessionKey = `agent:my-agent:websocket:direct:${userId}`;
const vadThreshold = 150; // optional; use channel default if omitted
```

### Step 2: POST to Token Endpoint

```typescript
async function getWebCallToken(
  goclawUrl: string,
  roomName: string,
  userId: string,
  sessionKey?: string,
  vadThreshold?: number
) {
  const response = await fetch(`${goclawUrl}/v1/webcall/token`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      room_name: roomName,
      user_id: userId,
      session_key: sessionKey,
      vad_threshold: vadThreshold,
    }),
  });

  if (!response.ok) {
    throw new Error(`Token request failed: ${response.statusText}`);
  }

  return response.json(); // { token, url, room }
}
```

### Step 3: Pass Token to LiveKit Client

```typescript
const { token, url, room } = await getWebCallToken(
  'http://localhost:8080',
  'unique-room-123',
  'alice@example.com',
  'agent:my-agent:websocket:direct:alice@example.com'
);

// Connect to LiveKit with the token
await room.connect(url, token);
```

---

## LiveKit Client Setup

The token endpoint integrates with [LiveKit](https://livekit.io/), an open-source WebRTC platform. To use WebCall, you need a LiveKit client library in your application.

### Supported Runtimes

- **JavaScript/TypeScript:** `npm install livekit-client`
- **React:** `npm install @livekit/react`
- **Flutter:** `flutter pub add livekit_client`
- **iOS/Swift:** See [LiveKit iOS SDK](https://docs.livekit.io/client-sdk-swift/)
- **Android/Kotlin:** See [LiveKit Android SDK](https://docs.livekit.io/client-sdk-android/)

### Basic Connect Example (JavaScript)

```typescript
import { Room } from 'livekit-client';

const room = new Room({
  audio: true,
  video: false, // voice call only
});

// Fetch token from goclaw
const { token, url } = await getWebCallToken(...);

// Connect
await room.connect(url, token);

// Listen for agent audio
room.on('trackSubscribed', (track) => {
  if (track.kind === 'audio') {
    // Create audio element and attach track
    const audio = document.createElement('audio');
    audio.srcObject = new MediaStream([track.attach()]);
    audio.play();
  }
});
```

See [LiveKit Client Documentation](https://docs.livekit.io/client-sdk-js/interfaces/RoomOptions.html) for full API details.

---

## Call Flow

### Sequence Diagram

```
┌──────────────┐                      ┌──────────────┐
│   Browser    │                      │    GoClaw    │
└──────────────┘                      └──────────────┘
       │                                     │
       │  1. POST /v1/webcall/token          │
       ├────────────────────────────────────►│
       │                                     │
       │  2. {token, url, room}              │
       │◄────────────────────────────────────┤
       │                                     │
       │  3. Connect to LiveKit @ url        │
       ├────────────────────────────────────►│ (LiveKit server)
       │                                     │
       │  4. WebRTC offer/answer ────────────┤
       │  5. DTLS / SRTP encrypted media     │
       │  6. ICE connectivity established    │
       │                                     │
       │  7. Browser captures mic audio      │
       │     (Opus RTP frames)               │
       ├────────────────────────────────────►│ RTP payload
       │                                     │
       │                                     │ 8. VAD + collect frames
       │                                     │    until silence detected
       │                                     │
       │                                     │ 9. ffmpeg: Opus→PCM
       │                                     │
       │                                     │ 10. STT: PCM→transcript
       │                                     │
       │                                     │ 11. Publish to agent
       │                                     │     (bus.PublishInbound)
       │                                     │
       │                                     │ 12. Agent loop:
       │                                     │     think→act→observe
       │                                     │
       │                                     │ 13. TTS: reply→mp3
       │                                     │
       │                                     │ 14. ffmpeg: mp3→Opus
       │                                     │
       │  15. Agent audio (Opus RTP) ───────┤
       │◄────────────────────────────────────┤
       │
       │  16. Browser plays audio
       │
       │  17. Back to step 7 (listen again)
       │
       │  ... (repeat until call ends)
       │
       │  N. Browser stops listening
       │     or timeout (max 5 min)
       │
       ├──WebRTC close──────────────────────►│
       │                                     │
       │                                     │ Room cleanup
       │
      END                                   END
```

### Key Phases

1. **Token Exchange** — Browser calls `/v1/webcall/token` to get LiveKit credentials.
2. **WebRTC Handshake** — Browser and goclaw negotiate ICE candidates and establish encrypted media path.
3. **Listen Phase** — Browser captures local mic audio as Opus RTP frames and sends to goclaw.
4. **VAD Detection** — goclaw detects end-of-speech when 600ms of silence follows at least 100ms of voice.
5. **Transcription** — goclaw converts buffered Opus frames to PCM WAV and sends to STT provider.
6. **Agent Pipeline** — Transcript is published to the agent, which thinks, acts, and generates a reply.
7. **Synthesis** — Agent reply is synthesized to mp3 via TTS.
8. **Encoding** — mp3 is transcoded to Opus RTP and sent back to browser.
9. **Playback** — Browser receives and plays agent audio.
10. **Loop** — Repeat from step 3.

### Duration Constraints

- **Per-utterance STT timeout:** Configurable (default 30s). If transcription takes longer, it's retried.
- **Hard call cap:** Configurable (default 300s / 5 minutes). Calls longer than this are forcibly disconnected.
- **Silence timeout:** 600ms after detecting voice marks end-of-utterance. If silence is detected prematurely, adjust `vad_threshold` per-call.

---

## VAD Tuning

Voice Activity Detection (VAD) identifies when the user stops speaking so goclaw knows when to transcribe.

### How VAD Works

- **Voiced frame:** Opus payload ≥ 150 bytes (or your configured `vad_threshold`). Indicates actual speech.
- **Silent frame:** Opus payload < 150 bytes. Indicates silence, background noise, or pause.
- **Utterance start:** 5 consecutive voiced frames (100ms of continuous speech).
- **Utterance end:** 30 consecutive silent frames (600ms of silence after voice detected).

### Adjusting Threshold

If VAD is too aggressive (cutting off speech early):
- **Increase `vad_threshold`** in the token request (e.g., `200` instead of `150`). This requires longer silence between words.
- Or raise the channel-level default in the webcall config.

If VAD is too loose (including breath sounds or background noise):
- **Decrease `vad_threshold`** (e.g., `100`). This captures lighter speech but may pick up noise.
- Consider enabling a speech-confidence filter in your STT provider (Anthropic, OpenAI, etc.) instead.

### Per-Call Override

Always pass `vad_threshold` when creating a token if you need to tune per-user or per-context:

```json
{
  "room_name": "call-user-123",
  "user_id": "user@example.com",
  "vad_threshold": 200  // Use 200 bytes instead of default
}
```

---

## Important Notes

### Half-Duplex / No Barge-In

The channel operates in half-duplex (one-way at a time). The browser must wait for the agent to finish speaking before its next utterance is captured. There is no automatic barge-in detection.

If you need full-duplex (simultaneous speak/listen), you will need:
1. A continuous STT provider (e.g., Deepgram live transcription).
2. A way to detect agent speech in real-time to mute the mic.
3. Integration work to route partial transcripts before end-of-utterance.

### Session Persistence

Each call reuses the same session key across multiple utterances. This means:
- Conversation history accumulates in the session exactly like any other channel.
- Memory consolidation applies normally (episodic summaries, semantic embeddings).
- The agent retains context across the entire call.

If you want a fresh session per call, generate a new `session_key` UUID for each request:
```typescript
const sessionKey = `agent:my-agent:websocket:direct:${userId}-${Date.now()}`;
```

### TTS Voice Selection

The channel respects agent-level TTS voice configuration:
- When an agent has a custom voice set in `agent.other_config.tts_voice_id`, that voice is used for replies.
- The channel-level `tts_voice_id` config is the fallback when the agent has no override.
- Ensure the selected voice is compatible with your TTS provider (Anthropic, OpenAI, ElevenLabs, etc.).

### TURN Server for NAT Traversal

If the browser and goclaw are separated by NAT (corporate firewall, home network), you may need a TURN relay server. Configure in the webcall channel instance:

```json
{
  "turn_urls": ["turn:coturn.example.com:3478"],
  "turn_username": "username",
  "turn_password": "password"
}
```

Without TURN, calls will fail if both sides are behind symmetric NAT. See [coturn docs](https://github.com/coturn/coturn) for self-hosted TURN setup.

### Allowlist

The channel can restrict who can initiate calls via the `allow_from` config (list of user IDs). If configured and the incoming `user_id` is not in the list, the token request is rejected with 403 Forbidden.

---

## Troubleshooting

| Issue | Cause | Fix |
|-------|-------|-----|
| 400 Bad Request on token endpoint | Missing `room_name` or identity unresolvable | Provide `X-GoClaw-User-Id` header or `user_id` in body |
| 403 Forbidden | User is not in channel allowlist | Add user to `allow_from` config or remove allowlist |
| 503 Service Unavailable | LiveKit API credentials not configured | Configure `livekit_api_key` and `livekit_api_secret` in channel instance |
| WebRTC connection fails | NAT traversal issue | Configure `turn_urls` in channel instance |
| Agent audio not playing | STT provider down or missing | Check STT provider status and API keys in agent/channel config |
| VAD cutting off speech | Threshold too high | Decrease `vad_threshold` in token request |
| VAD capturing breath/noise | Threshold too low | Increase `vad_threshold` in token request |

---

## Reference

- **WebRTC Docs:** https://developer.mozilla.org/en-US/docs/Web/API/WebRTC_API
- **LiveKit Docs:** https://docs.livekit.io/
- **goclaw Webcall Docs:** `docs/21-webcall.md`
- **goclaw API:** http://your-goclaw-url/docs (auto-generated OpenAPI)
