package main

import (
	"io"
	"log"
	"os"

	"github.com/hoveychen/netferry/relay/internal/tui"
)

// runTUI launches the interactive terminal UI over the desktop's app-data
// store. Engine and remote-server output is captured into the UI's log ring
// so it cannot scribble over the alt-screen.
func runTUI(verbose bool) error {
	ring := tui.NewLogRing(1 << 20)

	restore, err := teeStderr(ring)
	if err != nil {
		return err
	}
	defer restore()
	prevLog := log.Writer()
	log.SetOutput(ring)
	defer log.SetOutput(prevLog)

	return tui.Run(tui.Options{
		Start:       tuiStarter(verbose),
		IsReconnect: isReconnectErr,
		Log:         ring,
		Version:     Version,
	})
}

// teeStderr points os.Stderr and the remote-server stderr writer at w (via a
// pipe, since os.Stderr must be an *os.File) and returns a restore func.
func teeStderr(w io.Writer) (func(), error) {
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	origErr, origServ := os.Stderr, serverStderr
	os.Stderr = pw
	serverStderr = w
	done := make(chan struct{})
	go func() {
		io.Copy(w, pr)
		close(done)
	}()
	return func() {
		os.Stderr = origErr
		serverStderr = origServ
		pw.Close()
		<-done
	}, nil
}
