package baseproc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// The wire transport is a length-prefixed JSON stream (decision D2): every
// frame is a 4-byte big-endian unsigned payload length followed by exactly
// that many bytes of one JSON value. stdio carries no ports, no auth surface,
// and binds the subprocess lifetime to the parent — the same reasons the MCP
// stdio transport chose it. The prefix makes frames self-delimiting so binary
// tool output embedded in JSON strings can never desync a reader, unlike
// newline-delimited JSON.

const (
	// maxFrameSize bounds one frame payload. The control plane is low-frequency
	// (attach/tool calls/catalog); 32 MiB is generous for any single tool
	// result and stops a desynced or hostile peer from forcing an unbounded
	// allocation. Progress chunks (D3) exist precisely so no frame has to
	// carry a whole long-running tool's output.
	maxFrameSize = 32 << 20

	// lengthPrefixSize is the fixed big-endian uint32 header.
	lengthPrefixSize = 4
)

// errFrameTooLarge reports a header announcing more than maxFrameSize bytes.
// The stream cannot be resynchronised after this (the payload bytes are
// indistinguishable from future frames), so callers must drop the connection.
var errFrameTooLarge = errors.New("baseproc: frame exceeds size limit")

// WriteFrame writes one length-prefixed frame. It performs exactly one
// io.Writer.Write call per component (header, payload) and is safe to call
// concurrently only when the caller serialises writes (the conn and server
// types own a write mutex — D3 requires interleaved notifications and
// responses not to corrupt each other).
func WriteFrame(w io.Writer, payload []byte) error {
	if uint64(len(payload)) > maxFrameSize {
		return fmt.Errorf("%w: %d > %d", errFrameTooLarge, len(payload), maxFrameSize)
	}
	var header [lengthPrefixSize]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := w.Write(header[:]); err != nil {
		return fmt.Errorf("baseproc: write frame header: %w", err)
	}
	if _, err := w.Write(payload); err != nil {
		return fmt.Errorf("baseproc: write frame payload: %w", err)
	}
	return nil
}

// ReadFrame reads one length-prefixed frame and returns the payload bytes.
// It returns io.EOF (or the reader's error) when the header read fails at a
// boundary — the graceful shutdown path is simply the peer closing the pipe
// (design D4: on Windows a vanished parent surfaces as pipe EOF and the
// subprocess exits itself, which this reader provides for free).
// An oversized header returns errFrameTooLarge.
func ReadFrame(r io.Reader) ([]byte, error) {
	var header [lengthPrefixSize]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			// Partial header: the peer died mid-frame. Preserve the abruptness
			// in the message but keep io semantics clear for callers.
			return nil, fmt.Errorf("baseproc: read frame header: %w", io.ErrUnexpectedEOF)
		}
		return nil, err // includes clean io.EOF
	}
	size := binary.BigEndian.Uint32(header[:])
	if size > maxFrameSize {
		return nil, fmt.Errorf("%w: %d > %d", errFrameTooLarge, size, maxFrameSize)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, fmt.Errorf("baseproc: read frame payload (%d bytes): %w", size, err)
	}
	return payload, nil
}
