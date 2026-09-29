package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hoveychen/netferry/relay/internal/stats"
)

// runConns implements `netferry-tunnel conns`: query the running tunnel's
// stats server for active and recently closed connections.
func runConns(args []string) {
	fs := flag.NewFlagSet("netferry-tunnel conns", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), `usage: netferry-tunnel conns [flags]

List active and recently closed connections of the running tunnel (newest
last). Timing is per TCP connection: requests multiplexed on one keep-alive /
HTTP/2 connection are not split apart.

flags:
`)
		fs.PrintDefaults()
	}
	var (
		host   = fs.String("host", "", "only connections whose host or dst contains this (case-insensitive)")
		since  = fs.String("since", "", "drop connections that closed before this: a duration (10m) or unix ms")
		errs   = fs.Bool("errors", false, "only connections that ended with an error")
		limit  = fs.Int("limit", 200, "newest N active and N closed connections (0 = all)")
		asJSON = fs.Bool("json", false, "print the raw JSON response")
		port   = fs.Int("port", 0, "stats server port (default: $NETFERRY_STATS_PORT, then the port cache)")
	)
	fs.Parse(args)

	p := *port
	if p == 0 {
		if s := os.Getenv("NETFERRY_STATS_PORT"); s != "" {
			p, _ = strconv.Atoi(s)
		}
	}
	if p == 0 {
		p = loadPortCache().StatsPort
	}
	if p == 0 {
		fatalf("conns: stats port unknown (no %s); pass --port", portCachePath())
	}

	q := url.Values{}
	if *host != "" {
		q.Set("host", *host)
	}
	if *since != "" {
		q.Set("since", *since)
	}
	if *errs {
		q.Set("errors", "1")
	}
	q.Set("limit", strconv.Itoa(*limit))
	u := fmt.Sprintf("http://127.0.0.1:%d/connections?%s", p, q.Encode())

	hc := http.Client{Timeout: 5 * time.Second}
	res, err := hc.Get(u)
	if err != nil {
		fatalf("conns: is the tunnel running? %v", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		fatalf("conns: read response: %v", err)
	}
	if res.StatusCode == http.StatusNotFound {
		fatalf("conns: the running tunnel on port %d predates /connections; restart it with a newer build", p)
	}
	if res.StatusCode != http.StatusOK {
		fatalf("conns: %s: %s", res.Status, strings.TrimSpace(string(body)))
	}
	if *asJSON {
		os.Stdout.Write(body)
		return
	}
	var resp stats.ConnectionsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		fatalf("conns: decode response: %v", err)
	}
	printConns(os.Stdout, resp)
}

func printConns(w io.Writer, resp stats.ConnectionsResponse) {
	fmt.Fprintf(w, "# closed (%d)\n", len(resp.Closed))
	for _, r := range resp.Closed {
		fmt.Fprintln(w, formatConn(r))
	}
	fmt.Fprintf(w, "# active (%d)\n", len(resp.Active))
	for _, r := range resp.Active {
		fmt.Fprintln(w, formatConn(r))
	}
}

// formatConn renders one connection as a single line:
//
//	<opened> <duration> <route> up=<tx> down=<rx> [first_byte=] [last_rx=] <host> (<dst>) [err=...]
func formatConn(r stats.ConnRecord) string {
	var b strings.Builder
	b.WriteString(time.UnixMilli(r.OpenedMs).Format("2006-01-02 15:04:05.000"))
	fmt.Fprintf(&b, " dur=%s", fmtMs(r.DurationMs))
	route := r.Route
	if r.TunnelIndex > 0 {
		route = fmt.Sprintf("%s#%d", route, r.TunnelIndex)
	}
	fmt.Fprintf(&b, " %s up=%s down=%s", route, fmtBytes(r.TxBytes), fmtBytes(r.RxBytes))
	if r.FirstRxMs > 0 {
		fmt.Fprintf(&b, " first_byte=%s", fmtMs(r.FirstRxMs))
	}
	if r.LastRxMs > 0 {
		fmt.Fprintf(&b, " last_rx=%s", time.UnixMilli(r.LastRxMs).Format("15:04:05.000"))
	}
	if r.Host != "" {
		fmt.Fprintf(&b, " %s (%s)", r.Host, r.DstAddr)
	} else {
		fmt.Fprintf(&b, " %s", r.DstAddr)
	}
	if r.Error != "" {
		fmt.Fprintf(&b, " err=%q", r.Error)
	}
	return b.String()
}

func fmtMs(ms int64) string {
	return (time.Duration(ms) * time.Millisecond).String()
}

func fmtBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%dB", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(n)/(1024*1024))
	}
}
