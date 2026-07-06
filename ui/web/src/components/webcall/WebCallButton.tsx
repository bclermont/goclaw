/**
 * WebCallButton — launches a browser voice call via LiveKit.
 *
 * Fetches a LiveKit JWT token from POST /v1/webcall/token, then renders a
 * LiveKit room with audio-only mode. Shows a "Call" button when idle and
 * an "End Call" button when connected.
 *
 * Requires packages: livekit-client @livekit/components-react @livekit/components-styles
 */
import { useState, useCallback, useEffect } from "react";
import { LiveKitRoom, RoomAudioRenderer, DisconnectButton, useLocalParticipant } from "@livekit/components-react";
import "@livekit/components-styles";
import { Phone, PhoneOff, Loader2, Mic, MicOff } from "lucide-react";
import { useHttp } from "@/hooks/use-ws";
import { useAuthStore } from "@/stores/use-auth-store";

interface WebCallButtonProps {
  /** Channel key identifying which webcall channel to use. Used as room name prefix. */
  channelKey: string;
  /** Optional agent key — passed as a label/context but room scoping is server-side. */
  agentId?: string;
  /** Current chat session key — when provided, the voice call continues in the same session. */
  sessionKey?: string;
  className?: string;
}

interface TokenResponse {
  token: string;
  url: string;
}

type CallState = "idle" | "connecting" | "connected" | "error";

function ConnectedControls() {
  const { localParticipant, isMicrophoneEnabled } = useLocalParticipant();
  const toggleMute = () => localParticipant.setMicrophoneEnabled(!isMicrophoneEnabled);

  return (
    <div className="flex items-center gap-2">
      <button
        type="button"
        onClick={toggleMute}
        title={isMicrophoneEnabled ? "Mute" : "Unmute"}
        className="flex items-center justify-center rounded-md border border-border bg-background p-1.5 hover:bg-accent hover:text-accent-foreground"
      >
        {isMicrophoneEnabled ? (
          <Mic className="h-3.5 w-3.5" />
        ) : (
          <MicOff className="h-3.5 w-3.5 text-destructive" />
        )}
      </button>
      <DisconnectButton>
        <button
          type="button"
          className="flex items-center gap-1.5 rounded-md bg-destructive px-3 py-1.5 text-xs font-medium text-destructive-foreground hover:bg-destructive/90"
        >
          <PhoneOff className="h-3.5 w-3.5" />
          <span>End Call</span>
        </button>
      </DisconnectButton>
    </div>
  );
}

export function WebCallButton({ channelKey, agentId, sessionKey, className }: WebCallButtonProps) {
  const http = useHttp();
  const userId = useAuthStore((s) => s.userId);

  const [callState, setCallState] = useState<CallState>("idle");
  const [livekitToken, setLivekitToken] = useState<string>("");
  const [livekitUrl, setLivekitUrl] = useState<string>("");
  const [errorMsg, setErrorMsg] = useState<string>("");

  const VAD_STORAGE_KEY = "webcall_vad_threshold";
  const VAD_DEFAULT = 80;
  const [vadThreshold, setVadThreshold] = useState<number>(() => {
    const stored = localStorage.getItem(VAD_STORAGE_KEY);
    if (stored !== null) {
      const parsed = parseInt(stored, 10);
      if (!isNaN(parsed)) return parsed;
    }
    return VAD_DEFAULT;
  });

  useEffect(() => {
    localStorage.setItem(VAD_STORAGE_KEY, String(vadThreshold));
  }, [vadThreshold]);

  // Room name: scoped per channel + agent so each agent chat has its own room.
  const roomName = agentId
    ? `${channelKey}:${agentId}:${userId}`
    : `${channelKey}:${userId}`;

  const startCall = useCallback(async () => {
    setCallState("connecting");
    setErrorMsg("");
    try {
      const body: Record<string, string | number> = { room_name: roomName, vad_threshold: vadThreshold };
      if (sessionKey) {
        body.session_key = sessionKey;
      }
const resp = await http.post<TokenResponse>("/v1/webcall/token", body);
      setLivekitToken(resp.token);
      setLivekitUrl(resp.url);
      setCallState("connected");
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : "Failed to start call";
      setErrorMsg(message);
      setCallState("error");
    }
  }, [http, roomName, sessionKey, userId, vadThreshold]);

  const handleDisconnected = useCallback(() => {
    setCallState("idle");
    setLivekitToken("");
    setLivekitUrl("");
    setErrorMsg("");
  }, []);

  if (callState === "connected" && livekitToken && livekitUrl) {
    return (
      <div className={className}>
        <LiveKitRoom
          serverUrl={livekitUrl}
          token={livekitToken}
          audio={true}
          video={false}
          onDisconnected={handleDisconnected}
          style={{ display: "contents" }}
        >
          <RoomAudioRenderer />
          <ConnectedControls />
        </LiveKitRoom>
      </div>
    );
  }

  return (
    <div className={className}>
      {callState === "error" && (
        <span className="mr-2 text-xs text-destructive" title={errorMsg}>
          Call failed
        </span>
      )}
      <div className="flex items-center gap-2">
        {callState === "idle" && (
          <div className="flex items-center gap-1.5">
            <Mic className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
            <input
              type="range"
              min={40}
              max={200}
              value={vadThreshold}
              onChange={(e) => setVadThreshold(Number(e.target.value))}
              className="w-20 accent-primary"
              aria-label="Microphone sensitivity"
            />
            <span className="w-6 text-right text-[10px] text-muted-foreground">{vadThreshold}</span>
          </div>
        )}
        <button
          type="button"
          disabled={callState === "connecting"}
          onClick={startCall}
          className="flex items-center gap-1.5 rounded-md border border-border bg-background px-3 py-1.5 text-xs font-medium hover:bg-accent hover:text-accent-foreground disabled:opacity-50"
        >
          {callState === "connecting" ? (
            <>
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
              <span>Connecting…</span>
            </>
          ) : (
            <>
              <Phone className="h-3.5 w-3.5" />
              <span>Call</span>
            </>
          )}
        </button>
      </div>
    </div>
  );
}
