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
last), or with --by one aggregate line per host / route / error. Timing is per
TCP connection: requests multiplexed on one keep-alive / HTTP/2 connection are
not split apart.

Start with --by host to see a whole time window in a few lines, then drill
into one host with --host, --sort and the other filters.

flags:
`)
		fs.PrintDefaults()
	}
	var (
		host     = fs.String("host", "", "only connections whose host or dst contains this (case-insensitive)")
		since    = fs.String("since", "", "drop connections that closed before this: a duration (10m) or unix ms")
		until    = fs.String("until", "", "drop connections opened after this: a duration (5m = 5 minutes ago) or unix ms")
		route    = fs.String("route", "", "only this route: tunnel, direct or blocked")
		minDur   = fs.Duration("min-dur", 0, "only connections that lasted at least this long (e.g. 5s)")
		minBytes = fs.Int64("min-bytes", 0, "only connections that moved at least this many bytes (up + down)")
		errs     = fs.Bool("errors", false, "only connections that ended with an error")
		order    = fs.String("sort", "open", "open (newest last), or dur, bytes, first-byte (largest first)")
		by       = fs.String("by", "", "aggregate by host, route or error instead of listing connections")
		limit    = fs.Int("limit", 200, "N active and N closed connections, or N groups with --by (0 = all)")
		asJSON   = fs.Bool("json", false, "print the raw JSON response")
		port     = fs.Int("port", 0, "stats server port (default: $NETFERRY_STATS_PORT, then the port cache)")
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
	if *until != "" {
		q.Set("until", *until)
	}
	if *route != "" {
		q.Set("route", *route)
	}
	if *minDur > 0 {
		q.Set("min_dur", minDur.String())
	}
	if *minBytes > 0 {
		q.Set("min_bytes", strconv.FormatInt(*minBytes, 10))
	}
	if *errs {
		q.Set("errors", "1")
	}
	if *order != "" && *order != "open" {
		q.Set("sort", *order)
	}
	if *by != "" {
		q.Set("group", *by)
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
	if *by != "" {
		var resp stats.ConnGroupsResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			fatalf("conns: decode response: %v", err)
		}
		if resp.GroupBy == "" {
			fatalf("conns: the running tunnel on port %d predates --by; restart it with a newer build", p)
		}
		printGroups(os.Stdout, resp)
		return
	}
	// Older tunnels ignore until/route/min_*/sort instead of rejecting them;
	// they are recognised by the missing closedMatched field.
	if *until != "" || *route != "" || *minDur > 0 || *minBytes > 0 || (*order != "" && *order != "open") {
		if !strings.Contains(string(body), `"closedMatched"`) {
			fatalf("conns: the running tunnel on port %d predates --until/--route/--min-dur/--min-bytes/--sort; restart it with a newer build", p)
		}
	}
	var resp stats.ConnectionsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		fatalf("conns: decode response: %v", err)
	}
	printConns(os.Stdout, resp)
}

func printConns(w io.Writer, resp stats.ConnectionsResponse) {
	printHistoryFrom(w, resp.HistoryFromMs)
	fmt.Fprintf(w, "# closed (%s)\n", shownOf(len(resp.Closed), resp.ClosedMatched))
	for _, r := range resp.Closed {
		fmt.Fprintln(w, formatConn(r))
	}
	fmt.Fprintf(w, "# active (%s)\n", shownOf(len(resp.Active), resp.ActiveMatched))
	for _, r := range resp.Active {
		fmt.Fprintln(w, formatConn(r))
	}
}

// printHistoryFrom tells the reader how far back the tunnel's history goes:
// it holds a bounded number of connections, so an empty result for an older
// incident is not proof of absence.
func printHistoryFrom(w io.Writer, fromMs int64) {
	if fromMs > 0 {
		fmt.Fprintf(w, "# history from %s\n", time.UnixMilli(fromMs).Format("2006-01-02 15:04:05"))
	}
}

// shownOf renders "12" or, when --limit cut the list, "12 of 640 matched".
func shownOf(shown, matched int) string {
	if matched > shown {
		return fmt.Sprintf("%d of %d matched; narrow the filters, use --by, or raise --limit", shown, matched)
	}
	return strconv.Itoa(shown)
}

// printGroups renders the --by aggregates, one line per group:
//
//	<conns> conns [active=] [err=] up= down= [no_reply=] [first_byte p50/p95] max_dur= last=<time> <key> [top_err="..."]
func printGroups(w io.Writer, resp stats.ConnGroupsResponse) {
	printHistoryFrom(w, resp.HistoryFromMs)
	fmt.Fprintf(w, "# %d connections in %d groups by %s", resp.Total, len(resp.Groups)+resp.Omitted, resp.GroupBy)
	if resp.Omitted > 0 {
		fmt.Fprintf(w, "; %d smallest groups omitted by --limit", resp.Omitted)
	}
	fmt.Fprintln(w)
	for _, g := range resp.Groups {
		var b strings.Builder
		fmt.Fprintf(&b, "%4d conns", g.Conns)
		if g.Active > 0 {
			fmt.Fprintf(&b, " active=%d", g.Active)
		}
		if g.Errors > 0 {
			fmt.Fprintf(&b, " err=%d", g.Errors)
		}
		fmt.Fprintf(&b, " up=%s down=%s", fmtBytes(g.TxBytes), fmtBytes(g.RxBytes))
		if g.NoReply > 0 {
			fmt.Fprintf(&b, " no_reply=%d", g.NoReply)
		}
		if g.FirstByteP50 > 0 {
			fmt.Fprintf(&b, " first_byte=%s/%s", fmtMs(g.FirstByteP50), fmtMs(g.FirstByteP95))
		}
		fmt.Fprintf(&b, " max_dur=%s last=%s %s", fmtMs(g.MaxDurMs), time.UnixMilli(g.LastSeenMs).Format("15:04:05"), g.Key)
		if g.TopError != "" && resp.GroupBy != "error" {
			fmt.Fprintf(&b, " top_err=%q", g.TopError)
		}
		fmt.Fprintln(w, b.String())
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
