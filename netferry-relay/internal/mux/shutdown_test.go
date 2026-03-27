package mux

import (
	"testing"
	"time"
)

func TestClientConnClose_SendsStopSendingAndEOF(t *testing.T) {
	serverPipe, clientPipe := newPipePair()

	opened := make(chan uint16, 1)
	srv := NewMuxServer(serverPipe.r, serverPipe.w, ServerHandlers{
		NewTCP: func(channel uint16, family int, dstIP string, dstPort int) {
			opened <- channel
		},
	})
	cli := NewMuxClient(clientPipe.r, clientPipe.w)

	srvErr := make(chan error, 1)
	go func() { srvErr <- srv.Run() }()
	cliErr := make(chan error, 1)
	go func() { cliErr <- cli.Run() }()

	conn, err := cli.OpenTCP(2, "10.0.0.1", 443)
	if err != nil {
		t.Fatalf("OpenTCP: %v", err)
	}

	var channel uint16
	select {
	case channel = <-opened:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for server channel open")
	}

	inbox := srv.InboxFor(channel)
	if inbox == nil {
		t.Fatal("expected server inbox after TCP connect")
	}

	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	want := []uint16{CMD_TCP_STOP_SENDING, CMD_TCP_EOF}
	for i, cmd := range want {
		select {
		case f, ok := <-inbox:
			if !ok {
				t.Fatalf("frame %d: inbox closed early", i)
			}
			if f.Cmd != cmd {
				t.Fatalf("frame %d: got cmd=%04x want=%04x", i, f.Cmd, cmd)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timeout waiting for frame %d", i)
		}
	}

	cli.shutdown("test cleanup")
	srv.shutdown("test cleanup")
	_ = clientPipe.w.Close()
	_ = clientPipe.r.Close()
	_ = serverPipe.r.Close()
	_ = serverPipe.w.Close()
}

func TestClientConnCloseWrite_UnblocksOnShutdown(t *testing.T) {
	cli := NewMuxClient(nil, nil)
	for i := 0; i < cap(cli.out); i++ {
		cli.out <- Frame{Channel: uint16(i), Cmd: CMD_TCP_DATA}
	}

	cc := &ClientConn{client: cli, channel: 7}
	done := make(chan error, 1)
	go func() {
		done <- cc.CloseWrite()
	}()

	select {
	case err := <-done:
		t.Fatalf("CloseWrite returned before shutdown: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	cli.shutdown("test shutdown")

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected CloseWrite to return an error after shutdown")
		}
	case <-time.After(time.Second):
		t.Fatal("CloseWrite remained blocked after shutdown")
	}
}

func TestMuxServerSendTo_UnblocksOnShutdown(t *testing.T) {
	srv := NewMuxServer(nil, nil, ServerHandlers{})
	// Fill the fair scheduler to capacity by draining all capacity tokens.
	for len(srv.sched.tokens) > 0 {
		<-srv.sched.tokens
	}

	done := make(chan struct{})
	go func() {
		srv.SendTo(42, CMD_TCP_DATA, []byte("payload"))
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("SendTo returned before shutdown")
	case <-time.After(100 * time.Millisecond):
	}

	srv.shutdown("test shutdown")

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SendTo remained blocked after shutdown")
	}
}
