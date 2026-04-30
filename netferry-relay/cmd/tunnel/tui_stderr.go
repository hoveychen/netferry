package main

import (
	"io"
	"os"
)

// teeStderrCloser holds the resources allocated by teeStderr so the caller can
// release them on shutdown.
type teeStderrCloser struct {
	pw       *os.File
	origErr  *os.File
	origServ io.Writer
}

func (t *teeStderrCloser) Close() error {
	os.Stderr = t.origErr
	serverStderr = t.origServ
	return t.pw.Close()
}

// teeStderr redirects os.Stderr (and the package-global serverStderr used for
// remote SSH session stderr) into the given writer via an os.Pipe. The
// returned closer restores the originals.
//
// This is needed so engine + ssh remote stderr writes don't corrupt the
// alt-screen TUI display.
func teeStderr(w io.Writer) (io.Closer, error) {
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	closer := &teeStderrCloser{
		pw:       pw,
		origErr:  os.Stderr,
		origServ: serverStderr,
	}
	os.Stderr = pw
	serverStderr = w
	go io.Copy(w, pr)
	return closer, nil
}
