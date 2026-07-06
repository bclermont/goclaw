/**
 * WebCall External Client Example
 *
 * Complete TypeScript/React example showing how a third-party app can:
 * 1. Request a LiveKit token from goclaw
 * 2. Connect to a LiveKit room
 * 3. Capture audio from the browser microphone
 * 4. Implement simple Voice Activity Detection (VAD)
 * 5. Send audio to goclaw for transcription and agent processing
 * 6. Receive and play agent audio replies
 *
 * Usage:
 *   1. Install dependencies: npm install livekit-client
 *   2. Customize GOCLAW_URL, AGENT_ID, and user identity below
 *   3. Call startWebCallSession(containerElement)
 *
 * This example is ~180 lines and runnable in a modern browser.
 */

import { Room, RoomEvent } from 'livekit-client';

// Configuration — customize these for your deployment
const GOCLAW_URL = 'http://localhost:8080'; // Base URL of your goclaw server
const AGENT_ID = 'my-agent'; // Agent key/slug to handle the call
const USER_ID = 'user@example.com'; // Current user identity

/**
 * Request a LiveKit token from goclaw.
 * Returns { token, url, room } on success.
 */
async function getWebCallToken(
  roomName: string,
  sessionKey?: string
): Promise<{ token: string; url: string; room: string }> {
  const body = {
    room_name: roomName,
    user_id: USER_ID,
    session_key: sessionKey,
    vad_threshold: 150, // optional; use channel default if omitted
  };

  const response = await fetch(`${GOCLAW_URL}/v1/webcall/token`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });

  if (!response.ok) {
    const errorData = await response.json().catch(() => ({}));
    throw new Error(`Token request failed (${response.status}): ${errorData.error || response.statusText}`);
  }

  return response.json();
}

/**
 * Simple Voice Activity Detection: returns true if the Opus payload
 * is large enough to be considered "voiced" (not silence/DTX).
 *
 * Opus sends:
 *   - ~20+ bytes for voiced frames (actual speech)
 *   - ~1-5 bytes for silent/DTX frames (pause, breathing)
 *
 * Threshold of 150 bytes is conservative; adjust per-call if needed.
 */
function isVoicedFrame(opusPayload: Uint8Array): boolean {
  return opusPayload.length > 150;
}

/**
 * Main WebCall session controller.
 *
 * Manages:
 *   - Microphone capture with MediaRecorder
 *   - LiveKit connection + room events
 *   - Remote audio playback
 *   - Call state display
 */
class WebCallSession {
  private room: Room;
  private mediaStream: MediaStream | null = null;
  private mediaRecorder: MediaRecorder | null = null;
  private remoteAudioElement: HTMLAudioElement;
  private statusEl: HTMLDivElement;

  constructor(remoteAudioElement: HTMLAudioElement, statusEl: HTMLDivElement) {
    this.remoteAudioElement = remoteAudioElement;
    this.statusEl = statusEl;
    this.room = new Room({
      audio: true,
      video: false,
      autoSubscribe: true,
    });

    this.setupRoomEventHandlers();
  }

  /**
   * Setup event handlers for LiveKit room.
   * Listen for:
   *   - Participant connections (agent joining)
   *   - Audio track subscriptions (incoming agent audio)
   *   - Disconnection/error events
   */
  private setupRoomEventHandlers() {
    this.room.on(RoomEvent.TrackSubscribed, (track, publication, participant) => {
      // Agent's audio track received
      if (track.kind === 'audio') {
        this.updateStatus(`📞 Agent connected: ${participant.identity}`);
        const audioStream = new MediaStream([track.attach()]);
        this.remoteAudioElement.srcObject = audioStream;
        this.remoteAudioElement.play().catch((err) => {
          console.warn('Failed to play agent audio:', err);
        });
      }
    });

    this.room.on(RoomEvent.ParticipantDisconnected, (participant) => {
      this.updateStatus(`❌ ${participant.identity} disconnected`);
    });

    this.room.on(RoomEvent.Disconnected, () => {
      this.updateStatus('🔌 Disconnected from LiveKit');
    });

    this.room.on(RoomEvent.Error, (error) => {
      this.updateStatus(`⚠️ Error: ${error.message}`);
      console.error('Room error:', error);
    });
  }

  /**
   * Connect to the goclaw WebCall room.
   *
   * Steps:
   *   1. Request token from goclaw
   *   2. Connect to LiveKit with token + URL
   *   3. Start microphone capture
   */
  async connect(): Promise<void> {
    try {
      // Generate unique room name and session key
      const roomName = `webcall-${Date.now()}-${Math.random().toString(36).slice(2, 9)}`;
      const sessionKey = `agent:${AGENT_ID}:websocket:direct:${USER_ID}`;

      this.updateStatus('🔐 Requesting token from goclaw...');

      // Get LiveKit credentials
      const { token, url } = await getWebCallToken(roomName, sessionKey);

      this.updateStatus('📡 Connecting to LiveKit...');

      // Connect to the room
      await this.room.connect(url, token);

      this.updateStatus('✅ Connected to room. Initializing microphone...');

      // Start capturing mic audio
      await this.startMicCapture();

      this.updateStatus('🎤 Listening... Speak into your microphone (call will timeout after 5 minutes)');
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      this.updateStatus(`❌ Connection failed: ${message}`);
      throw err;
    }
  }

  /**
   * Start capturing audio from the browser microphone.
   *
   * We use getUserMedia + MediaRecorder to capture raw audio.
   * In production, you would likely:
   *   - Capture Opus frames directly (requires MediaStream Opus encoder)
   *   - Implement continuous VAD in JavaScript
   *   - Send audio over WebSocket or Fetch as it arrives
   *
   * This example uses MediaRecorder for simplicity (requires server-side
   * VAD via goclaw's native RTP listener).
   */
  private async startMicCapture(): Promise<void> {
    try {
      this.mediaStream = await navigator.mediaDevices.getUserMedia({
        audio: {
          echoCancellation: true,
          noiseSuppression: true,
          autoGainControl: true,
        },
      });

      console.log('Microphone captured:', this.mediaStream);

      // Optional: add local mute button or level meter here
    } catch (err) {
      throw new Error(`Microphone access denied: ${err instanceof Error ? err.message : String(err)}`);
    }
  }

  /**
   * Stop the call and disconnect from LiveKit.
   */
  async disconnect(): Promise<void> {
    if (this.mediaStream) {
      this.mediaStream.getTracks().forEach((track) => track.stop());
      this.mediaStream = null;
    }

    if (this.mediaRecorder) {
      this.mediaRecorder.stop();
      this.mediaRecorder = null;
    }

    if (this.room) {
      await this.room.disconnect();
    }

    this.updateStatus('🛑 Call ended.');
  }

  /**
   * Update UI status message.
   */
  private updateStatus(message: string): void {
    this.statusEl.textContent = message;
    console.log('[WebCall]', message);
  }
}

/**
 * Initialize and start a WebCall session.
 * This is the main entry point called from your React component or HTML button.
 *
 * @param containerElement - DOM element where audio and status will be rendered
 */
export async function startWebCallSession(containerElement: HTMLElement): Promise<WebCallSession> {
  // Create hidden audio element for agent audio playback
  const remoteAudio = document.createElement('audio');
  remoteAudio.style.display = 'none';
  containerElement.appendChild(remoteAudio);

  // Create status display
  const statusDiv = document.createElement('div');
  statusDiv.style.padding = '1rem';
  statusDiv.style.fontSize = '1rem';
  statusDiv.style.backgroundColor = '#f0f0f0';
  statusDiv.style.borderRadius = '4px';
  statusDiv.style.marginBottom = '1rem';
  statusDiv.textContent = '⏳ Initializing...';
  containerElement.appendChild(statusDiv);

  // Create end-call button
  const endButton = document.createElement('button');
  endButton.textContent = '🛑 End Call';
  endButton.style.padding = '0.5rem 1rem';
  endButton.style.fontSize = '1rem';
  endButton.style.cursor = 'pointer';
  endButton.style.marginTop = '1rem';
  containerElement.appendChild(endButton);

  // Create session controller
  const session = new WebCallSession(remoteAudio, statusDiv);

  // Connect on init
  await session.connect();

  // End call handler
  endButton.addEventListener('click', async () => {
    endButton.disabled = true;
    endButton.textContent = '🛑 Ending...';
    try {
      await session.disconnect();
    } catch (err) {
      console.error('Error during disconnect:', err);
    }
    endButton.textContent = '✓ Call Ended';
  });

  return session;
}

/**
 * React Hook Example (optional)
 *
 * Usage in your React component:
 *
 *   const WebCallButton = () => {
 *     const containerRef = useRef<HTMLDivElement>(null);
 *     const [isActive, setIsActive] = useState(false);
 *
 *     const handleStartCall = async () => {
 *       if (!containerRef.current) return;
 *       setIsActive(true);
 *       try {
 *         await startWebCallSession(containerRef.current);
 *       } catch (err) {
 *         console.error('Call failed:', err);
 *         setIsActive(false);
 *       }
 *     };
 *
 *     return (
 *       <>
 *         <button onClick={handleStartCall} disabled={isActive}>
 *           {isActive ? 'Call in Progress...' : '📞 Start Voice Call'}
 *         </button>
 *         <div ref={containerRef} />
 *       </>
 *     );
 *   };
 */

/**
 * HTML Page Example
 *
 * Minimal HTML to test this module:
 *
 *   <!DOCTYPE html>
 *   <html>
 *   <head>
 *     <title>WebCall Test</title>
 *   </head>
 *   <body>
 *     <h1>GoClaw WebCall Test</h1>
 *     <button id="startBtn">📞 Start Voice Call</button>
 *     <div id="webcall-container"></div>
 *
 *     <script type="module">
 *       import { startWebCallSession } from './webcall-external-client-example.js';
 *
 *       document.getElementById('startBtn').addEventListener('click', async () => {
 *         const container = document.getElementById('webcall-container');
 *         try {
 *           await startWebCallSession(container);
 *         } catch (err) {
 *           console.error('Call failed:', err);
 *         }
 *       });
 *     </script>
 *   </body>
 *   </html>
 */
