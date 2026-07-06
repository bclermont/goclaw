package webcall

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"os/exec"
	"sync"
	"time"
)

// ffmpeg detection state. Resolved once at first use (lazy singleton) so
// the package can be imported without requiring ffmpeg at startup.
var (
	ffmpegOnce sync.Once
	ffmpegPath string // empty if not found
	ffmpegWarn sync.Once
)

func resolveFFmpeg() string {
	ffmpegOnce.Do(func() {
		p, err := exec.LookPath("ffmpeg")
		if err == nil {
			ffmpegPath = p
		}
	})
	return ffmpegPath
}

// warnFFmpegMissing emits a single ops warning if ffmpeg is absent.
func warnFFmpegMissing() {
	ffmpegWarn.Do(func() {
		slog.Warn("security.webcall_ffmpeg_missing",
			"msg", "ffmpeg not found in PATH — webcall audio transcoding disabled. "+
				"Install ffmpeg and restart goclaw to enable STT/TTS for voice calls.")
	})
}

// ffmpegTimeout is the per-invocation deadline for ffmpeg transcoding.
const ffmpegTimeout = 30 * time.Second

// encodeToOgg converts arbitrary audio (mp3, wav, ogg, etc.) to an OGG/Opus
// stream at 48 kHz mono using ffmpeg. Returns OGG bytes suitable for
// lksdk.NewLocalReaderTrack with MimeTypeOpus.
//
// Fallback: if ffmpeg is absent, returns an error that the caller must handle
// gracefully (log + skip TTS rather than crash).
func encodeToOgg(ctx context.Context, audioBytes []byte) ([]byte, error) {
	fp := resolveFFmpeg()
	if fp == "" {
		warnFFmpegMissing()
		return nil, fmt.Errorf("ffmpeg not available for opus encoding")
	}

	tctx, cancel := context.WithTimeout(ctx, ffmpegTimeout)
	defer cancel()

	cmd := exec.CommandContext(tctx, fp, //nolint:gosec // fp is from exec.LookPath, safe
		"-hide_banner", "-loglevel", "error",
		"-i", "pipe:0",
		"-c:a", "libopus",
		"-ar", "48000",
		"-ac", "1",
		"-b:a", "32k",
		"-frame_duration", "20", // 20 ms frames = 960 samples
		"-f", "ogg",
		"pipe:1",
	)
	cmd.Stdin = bytes.NewReader(audioBytes)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg encode ogg/opus: %w (stderr: %s)", err, errBuf.String())
	}
	return outBuf.Bytes(), nil
}

// decodeOpusToPCM converts a sequence of raw Opus payloads (as received from
// RTP) to 16 kHz mono signed 16-bit PCM WAV via ffmpeg. The resulting WAV is
// suitable for all STT providers (OpenAI Whisper, ElevenLabs Scribe, etc.).
func decodeOpusToPCM(ctx context.Context, opusPayloads [][]byte) ([]byte, error) {
	fp := resolveFFmpeg()
	if fp == "" {
		warnFFmpegMissing()
		return nil, fmt.Errorf("ffmpeg not available for opus decoding")
	}

	oggData, err := wrapOpusInOgg(opusPayloads)
	if err != nil {
		return nil, fmt.Errorf("wrap opus in ogg: %w", err)
	}

	tctx, cancel := context.WithTimeout(ctx, ffmpegTimeout)
	defer cancel()

	cmd := exec.CommandContext(tctx, fp, //nolint:gosec // fp is from exec.LookPath, safe
		"-hide_banner", "-loglevel", "error",
		"-f", "ogg",
		"-i", "pipe:0",
		"-ar", "16000",
		"-ac", "1",
		"-c:a", "pcm_s16le",
		"-f", "wav",
		"pipe:1",
	)
	cmd.Stdin = bytes.NewReader(oggData)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg decode opus: %w (stderr: %s)", err, errBuf.String())
	}
	return outBuf.Bytes(), nil
}

// opusFramesToPCMWAV decodes raw Opus RTP payloads to 16 kHz PCM WAV via ffmpeg.
func opusFramesToPCMWAV(ctx context.Context, frames [][]byte) ([]byte, error) {
	if len(frames) == 0 {
		return emptyPCMWAV(), nil
	}

	wav, err := decodeOpusToPCM(ctx, frames)
	if err != nil {
		slog.Warn("webcall: opus→pcm fallback to empty WAV", "error", err)
		return emptyPCMWAV(), nil
	}
	return wav, nil
}

// emptyPCMWAV returns a minimal valid 16 kHz mono PCM WAV with no audio data.
// Used as fallback when decoding fails.
func emptyPCMWAV() []byte {
	const (
		sampleRate    = uint32(16000)
		numChannels   = uint16(1)
		bitsPerSample = uint16(16)
		blockAlign    = uint16(2) // numChannels * bitsPerSample / 8
		byteRate      = uint32(32000)
		pcmCodecTag   = uint16(1) // PCM
	)

	var buf bytes.Buffer
	buf.WriteString("RIFF")
	writeUint32LE(&buf, 36) // total size = header only
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	writeUint32LE(&buf, 16) // PCM fmt chunk size
	writeUint16LE(&buf, pcmCodecTag)
	writeUint16LE(&buf, numChannels)
	writeUint32LE(&buf, sampleRate)
	writeUint32LE(&buf, byteRate)
	writeUint16LE(&buf, blockAlign)
	writeUint16LE(&buf, bitsPerSample)
	buf.WriteString("data")
	writeUint32LE(&buf, 0)
	return buf.Bytes()
}

// --- OGG/Opus helpers -------------------------------------------------------
//
// Minimal OGG framing used to wrap raw Opus RTP packets for ffmpeg input.
// Spec: https://xiph.org/ogg/doc/framing.html

const (
	oggCapturePattern = "OggS"
	oggPageHeaderSize = 27 // bytes before segment table
)

// wrapOpusInOgg constructs a minimal valid OGG stream containing the Opus
// packets for ffmpeg to decode.
func wrapOpusInOgg(opusPayloads [][]byte) ([]byte, error) {
	if len(opusPayloads) == 0 {
		return nil, nil
	}

	var out bytes.Buffer
	const serialNo = uint64(1)

	idHeader := buildOpusIDHeader()
	writeOggPage(&out, idHeader, serialNo, 0, 0, true)

	commentHeader := buildOpusCommentHeader()
	writeOggPage(&out, commentHeader, serialNo, 0, 1, false)

	var granule uint64
	const samplesPerPacket = 960 // 48 kHz * 20 ms
	for idx, pkt := range opusPayloads {
		granule += samplesPerPacket
		isLast := idx == len(opusPayloads)-1
		writeOggPage(&out, pkt, serialNo, granule, uint32(idx+2), isLast)
	}

	return out.Bytes(), nil
}

// buildOpusIDHeader builds an Opus ID header page payload (RFC 7845 §5.1).
func buildOpusIDHeader() []byte {
	var b bytes.Buffer
	b.WriteString("OpusHead")
	b.WriteByte(1)            // version
	b.WriteByte(1)            // channel count (mono)
	writeUint16LE(&b, 0)     // pre-skip
	writeUint32LE(&b, 48000) // input sample rate
	writeUint16LE(&b, 0)     // output gain
	b.WriteByte(0)            // channel mapping family (RTP mapping)
	return b.Bytes()
}

// buildOpusCommentHeader builds a minimal Opus comment header (RFC 7845 §5.2).
func buildOpusCommentHeader() []byte {
	var b bytes.Buffer
	b.WriteString("OpusTags")
	vendor := "goclaw-webcall"
	writeUint32LE(&b, uint32(len(vendor)))
	b.WriteString(vendor)
	writeUint32LE(&b, 0) // user comment list length = 0
	return b.Bytes()
}

// writeOggPage writes one OGG page to out.
func writeOggPage(out *bytes.Buffer, payload []byte, serial, granule uint64, seqno uint32, isLast bool) {
	var segTable []byte
	remaining := len(payload)
	for remaining >= 255 {
		segTable = append(segTable, 255)
		remaining -= 255
	}
	segTable = append(segTable, byte(remaining))

	var flags byte
	if seqno == 0 {
		flags = 0x02 // BOS
	}
	if isLast {
		flags |= 0x04 // EOS
	}

	var header bytes.Buffer
	header.WriteString(oggCapturePattern)
	header.WriteByte(0) // stream structure version
	header.WriteByte(flags)
	writeUint64LE(&header, granule)
	writeUint32LE(&header, uint32(serial))
	writeUint32LE(&header, seqno)
	writeUint32LE(&header, 0) // checksum placeholder
	header.WriteByte(byte(len(segTable)))
	header.Write(segTable)

	fullPage := append(header.Bytes(), payload...)
	checksum := oggCRC32(fullPage)

	// Patch checksum at offset 22.
	binary.LittleEndian.PutUint32(fullPage[22:26], checksum)
	out.Write(fullPage)
}

// writeUint64LE writes a uint64 in little-endian to buf.
func writeUint64LE(buf *bytes.Buffer, v uint64) {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	buf.Write(b[:])
}

// oggCRC32 computes the OGG CRC-32 (polynomial 0x04C11DB7).
func oggCRC32(data []byte) uint32 {
	var crc uint32
	for _, bb := range data {
		crc ^= uint32(bb) << 24
		for range 8 {
			if crc&0x80000000 != 0 {
				crc = (crc << 1) ^ 0x04C11DB7
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// --- WAV helpers ------------------------------------------------------------

func writeUint16LE(buf *bytes.Buffer, v uint16) {
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], v)
	buf.Write(b[:])
}

func writeUint32LE(buf *bytes.Buffer, v uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	buf.Write(b[:])
}
