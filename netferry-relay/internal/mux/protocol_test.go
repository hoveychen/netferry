package mux

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// TestWriteReadFrame_RoundTrip verifies that a frame written by WriteFrame
// is read back identically by ReadFrame.
func TestWriteReadFrame_RoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		channel uint16
		cmd     uint16
		data    []byte
	}{
		{"simple", 1, CMD_TCP_DATA, []byte("hello world")},
		{"zero channel", 0, CMD_PING, nil},
		{"max channel", 65535, CMD_TCP_EOF, nil},
		{"binary data", 42, CMD_TCP_CONNECT, []byte{0x00, 0x01, 0xFF, 0xFE}},
		{"empty data", 7, CMD_TCP_EOF, []byte{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			f := Frame{Channel: tc.channel, Cmd: tc.cmd, Data: tc.data}
			if err := WriteFrame(&buf, f); err != nil {
				t.Fatalf("WriteFrame: %v", err)
			}

			got, err := ReadFrame(&buf)
			if err != nil {
				t.Fatalf("ReadFrame: %v", err)
			}
			if got.Channel != tc.channel {
				t.Errorf("Channel: got %d, want %d", got.Channel, tc.channel)
			}
			if got.Cmd != tc.cmd {
				t.Errorf("Cmd: got %04x, want %04x", got.Cmd, tc.cmd)
			}
			// nil and empty slice should both decode to an empty/nil Data.
			wantLen := len(tc.data)
			if len(got.Data) != wantLen {
				t.Errorf("Data len: got %d, want %d", len(got.Data), wantLen)
			}
			if wantLen > 0 && !bytes.Equal(got.Data, tc.data) {
				t.Errorf("Data mismatch: got %v, want %v", got.Data, tc.data)
			}
		})
	}
}

// TestWriteReadFrame_LargeData checks that a 64 KB payload survives the round-trip.
func TestWriteReadFrame_LargeData(t *testing.T) {
	data := make([]byte, 65535)
	for i := range data {
		data[i] = byte(i)
	}

	var buf bytes.Buffer
	f := Frame{Channel: 1, Cmd: CMD_TCP_DATA, Data: data}
	if err := WriteFrame(&buf, f); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	got, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if !bytes.Equal(got.Data, data) {
		t.Errorf("Large data mismatch at %d bytes", len(data))
	}
}

// TestWriteFrame_TooLarge checks that WriteFrame rejects data exceeding 65535 bytes.
func TestWriteFrame_TooLarge(t *testing.T) {
	data := make([]byte, 65536) // one byte over the limit
	var buf bytes.Buffer
	err := WriteFrame(&buf, Frame{Channel: 0, Cmd: CMD_TCP_DATA, Data: data})
	if err == nil {
		t.Fatal("expected error for oversized data, got nil")
	}
}

// TestReadFrame_BadMagic checks that ReadFrame returns an error on bad magic bytes.
func TestReadFrame_BadMagic(t *testing.T) {
	// First two bytes must be 'S','S'; use 'X','X' instead.
	raw := []byte{'X', 'X', 0, 1, 0x42, 0x06, 0, 0}
	_, err := ReadFrame(bytes.NewReader(raw))
	if err == nil {
		t.Fatal("expected error for bad magic bytes, got nil")
	}
	if !strings.Contains(err.Error(), "bad magic") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestReadFrame_UnexpectedEOF checks that ReadFrame handles truncated input.
func TestReadFrame_UnexpectedEOF(t *testing.T) {
	// Write a valid header claiming 10 bytes of data, but provide none.
	hdr := []byte{'S', 'S', 0, 1, 0x42, 0x06, 0, 10}
	_, err := ReadFrame(bytes.NewReader(hdr))
	if err == nil {
		t.Fatal("expected error for truncated data, got nil")
	}
}

// TestReadFrame_EmptyReader checks behaviour on an empty reader.
func TestReadFrame_EmptyReader(t *testing.T) {
	_, err := ReadFrame(bytes.NewReader(nil))
	if err == nil {
		t.Fatal("expected EOF error, got nil")
	}
}

// TestWriteReadSyncHeader verifies the sync header round-trip.
func TestWriteReadSyncHeader(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteSyncHeader(&buf); err != nil {
		t.Fatalf("WriteSyncHeader: %v", err)
	}
	if buf.Len() != SYNC_HDR_N {
		t.Fatalf("expected %d bytes, got %d", SYNC_HDR_N, buf.Len())
	}
	if err := ReadSyncHeader(&buf); err != nil {
		t.Fatalf("ReadSyncHeader: %v", err)
	}
}

// TestReadSyncHeader_Bad checks that a wrong header is rejected.
func TestReadSyncHeader_Bad(t *testing.T) {
	bad := strings.Repeat("X", SYNC_HDR_N)
	err := ReadSyncHeader(strings.NewReader(bad))
	if err == nil {
		t.Fatal("expected error for bad sync header, got nil")
	}
}

// TestReadSyncHeader_Short checks handling of a truncated sync header.
func TestReadSyncHeader_Short(t *testing.T) {
	err := ReadSyncHeader(strings.NewReader("\x00\x00SSH"))
	if err == nil {
		t.Fatal("expected error for truncated sync header, got nil")
	}
}

// TestWriteFrame_ZeroLengthData verifies that a zero-length Data field is
// encoded as a frame with datalen=0 and no trailing bytes.
func TestWriteFrame_ZeroLengthData(t *testing.T) {
	var buf bytes.Buffer
	f := Frame{Channel: 3, Cmd: CMD_PING, Data: nil}
	if err := WriteFrame(&buf, f); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	if buf.Len() != HDR_LEN {
		t.Fatalf("expected exactly %d bytes for zero-data frame, got %d", HDR_LEN, buf.Len())
	}

	got, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if len(got.Data) != 0 {
		t.Errorf("expected empty Data, got %v", got.Data)
	}
}

// TestMultipleFrames verifies sequential write/read of several frames.
func TestMultipleFrames(t *testing.T) {
	var buf bytes.Buffer

	frames := []Frame{
		{Channel: 1, Cmd: CMD_TCP_CONNECT, Data: []byte("2,10.0.0.1,80")},
		{Channel: 1, Cmd: CMD_TCP_DATA, Data: []byte("GET / HTTP/1.0\r\n\r\n")},
		{Channel: 1, Cmd: CMD_TCP_EOF, Data: nil},
	}

	for _, f := range frames {
		if err := WriteFrame(&buf, f); err != nil {
			t.Fatalf("WriteFrame: %v", err)
		}
	}

	for i, want := range frames {
		got, err := ReadFrame(&buf)
		if err != nil {
			t.Fatalf("frame %d: ReadFrame: %v", i, err)
		}
		if got.Channel != want.Channel || got.Cmd != want.Cmd {
			t.Errorf("frame %d: got {%d,%04x}, want {%d,%04x}",
				i, got.Channel, got.Cmd, want.Channel, want.Cmd)
		}
		if !bytes.Equal(got.Data, want.Data) {
			t.Errorf("frame %d: data mismatch", i)
		}
	}

	// Buffer should now be empty.
	extra := make([]byte, 1)
	n, err := buf.Read(extra)
	if n != 0 || err != io.EOF {
		t.Errorf("unexpected trailing bytes in buffer: n=%d err=%v", n, err)
	}
}
