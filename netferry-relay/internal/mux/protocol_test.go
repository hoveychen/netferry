package mux

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestErrorFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	writeMsg(&buf, []byte("hello"))
	WriteErrorMsg(&buf, "dial tcp: lookup x.invalid: no such host")

	payload, err := readMsg(&buf)
	if err != nil || string(payload) != "hello" {
		t.Fatalf("data frame: %q, %v", payload, err)
	}
	_, err = readMsg(&buf)
	var re *RemoteError
	if !errors.As(err, &re) || re.Msg != "dial tcp: lookup x.invalid: no such host" {
		t.Fatalf("error frame: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("%d bytes left unread", buf.Len())
	}
}

func TestErrorFrameTruncatesLongMessage(t *testing.T) {
	var buf bytes.Buffer
	WriteErrorMsg(&buf, strings.Repeat("x", maxErrMsg*2))
	_, err := readMsg(&buf)
	var re *RemoteError
	if !errors.As(err, &re) || len(re.Msg) != maxErrMsg {
		t.Fatalf("got %v", err)
	}
}
