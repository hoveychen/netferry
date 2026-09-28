package stats

import (
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSubscribeReceivesBroadcastAndCloseStopsServer(t *testing.T) {
	c := NewCounters()
	port, err := c.ListenAndServe(0)
	if err != nil {
		t.Fatal(err)
	}
	ch, cancel := c.Subscribe()
	defer cancel()
	c.PushConnEvent("127.0.0.1:1", "1.2.3.4:443")
	deadline := time.After(3 * time.Second)
	for got := false; !got; {
		select {
		case msg := <-ch:
			got = strings.HasPrefix(msg, "event: stats\n") || strings.HasPrefix(msg, "event: connection\n")
		case <-deadline:
			t.Fatal("no SSE message delivered to in-process subscriber")
		}
	}

	c.Close()
	c.Close() // idempotent
	if conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), time.Second); err == nil {
		conn.Close()
		t.Fatal("stats server still accepting after Close")
	}
}
