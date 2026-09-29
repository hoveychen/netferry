---
name: netferry-tunnel
version: 2
description: Use when a website, API, git remote, package registry or other network request is slow, hanging, timing out, reset or failing on a machine running the NetFerry tunnel (desktop app or netferry-tunnel CLI), or when asked which connections went through the tunnel, whether a host was routed direct or via the tunnel, or why a connection closed.
---

# netferry-tunnel — inspect the running NetFerry tunnel's connections

NetFerry is a transparent proxy: while it runs, outbound TCP is routed either
through an SSH/mux tunnel or directly, per the user's route rules. The running
tunnel keeps a history of its connections that you can query to diagnose
network problems instead of guessing.

## Binary

`netferry-tunnel` is usually NOT on PATH (the desktop app bundles it). Use:

```bash
{{BIN}}
```

That path was recorded when this skill was installed. If it no longer exists,
find the binary of the tunnel that is actually running:

```bash
pgrep -fl netferry-tunnel        # macOS / Linux; first field is the pid, then the binary path
```

No running `netferry-tunnel` process means the tunnel is off and there is
nothing to query.

## Subcommands

### `netferry-tunnel conns`

Queries the running tunnel's connection history. Does not need root. The
history is in memory only: it starts when the tunnel (re)started and keeps the
last 4096 closed connections, including ones that were blocked or failed to
open. Every output starts with `# history from <time>` — nothing older than
that can be found, so a missing host before that time is not evidence.

**Keep the output small: aggregate first, then drill in.** A busy machine opens
~100 connections a minute, so listing raw connections over more than a few
minutes floods your context and gets cut by `--limit`. When a list was cut the
header says `# closed (200 of 640 matched; ...)`: narrow the filters or switch
to `--by` rather than raising `--limit`.

Filters (combine freely; they apply to `--by` too):

- `--since <d>` — drop connections that closed before this: a duration (`10m` = 10 minutes ago) or unix ms.
- `--until <d>` — drop connections opened after this. With `--since` it selects a window, e.g. `--since 20m --until 10m`.
- `--host <s>` — host or dst contains `s` (case-insensitive).
- `--route tunnel|direct|blocked`
- `--errors` — only connections that ended with an error.
- `--min-dur <d>` — lasted at least this long (`5s`).
- `--min-bytes <n>` — moved at least n bytes, up + down.

Output shape:

- `--by host|route|error` — one aggregate line per group, most connections first; `--limit` caps the number of groups.
- `--sort open|dur|bytes|first-byte` — `open` (default) is the newest `--limit` connections, oldest first; the others are the top `--limit` by that key, largest first.
- `--limit <n>` — n active and n closed connections, or n groups (default 200, 0 = all).
- `--json` — raw JSON, for jq. Connections: `{"historyFromMs","activeMatched","closedMatched","active":[...],"closed":[...]}`; groups: `{"historyFromMs","groupBy","total","groups":[...],"omitted"}`.
- `--port <n>` — stats server port; normally found automatically ($NETFERRY_STATS_PORT, then the port cache).

```bash
netferry-tunnel conns --by host --since 30m                  # what talked to what, one line per host
netferry-tunnel conns --by error --since 30m                 # which failure modes, how often
netferry-tunnel conns --host anthropic --since 10m --sort first-byte --limit 10   # slowest to answer
netferry-tunnel conns --since 1h --min-dur 30s --sort dur --limit 20              # long-lived / hung
netferry-tunnel conns --route direct --by host --since 1h    # what bypassed the tunnel
```

Group line format (`--by`):

```
<n> conns [active=<n>] [err=<n>] up=<tx> down=<rx> [no_reply=<n>] [first_byte=<p50>/<p95>] max_dur=<d> last=<time> <key> [top_err="..."]
```

- `no_reply`: closed connections that sent bytes but received none.
- `first_byte`: median / 95th percentile time to the first received byte.
- `last`: open time of the newest connection in the group; `top_err`: its most frequent error.

Connection line format:

```
<opened> dur=<d> <route>[#tunnel] up=<tx> down=<rx> [first_byte=<d>] [last_rx=<t>] <host> (<ip:port>) [err="..."]
```

- `route`: `tunnel` (with `#N` when several tunnels run), `direct`, or `blocked`.
- `first_byte`: time from open to the first byte received — high values mean a slow server or tunnel.
- `up=... down=0B` with a long `dur`: the request went out but nothing came back.
- `err`: why it closed. Errors reported by the remote end (e.g. the server could
  not dial the destination: DNS failure, connection refused, timeout) show up
  here too, so you can tell a remote dial failure from a local one.
- Timing is per TCP connection. Requests sharing one keep-alive / HTTP/2
  connection are not split apart, and URL paths are never visible (TLS): a
  host's traffic cannot be split by endpoint.

If the command says the running tunnel "predates" a flag, the tunnel binary is
older than this CLI; fall back to `--since`/`--host`/`--errors` or ask the
user to reconnect.

The same data is served at
`GET http://127.0.0.1:<stats-port>/connections?host=&since=&until=&route=&min_dur=&min_bytes=&errors=&sort=&group=&limit=`
(`group=` is the CLI's `--by`, `min_dur=` takes a duration).

### `netferry-tunnel install-claude-skill`

Installs or updates this skill into `~/.claude/skills/netferry-tunnel/SKILL.md`,
recording the binary's current path. `conns` also re-runs it automatically when
the installed copy is older than the embedded `version:`.

## Diagnosing a failing request

1. Reproduce the request (or note when it failed), and check `# history from`
   covers that time.
2. `netferry-tunnel conns --by host --since 10m --host <hostname>` — how many
   connections, errors, no-reply, first-byte latency. Drop `--host` to compare
   with everything else.
3. No group for the host at all: the traffic did not reach NetFerry (excluded
   subnet, IPv6, UDP other than DNS, or the app uses its own proxy).
4. `netferry-tunnel conns --by error --since 10m` — are failures specific to one
   host, or is everything through the tunnel failing?
5. Only then list individual connections, narrowly:
   `--host <hostname> --errors --since 10m`, or `--sort first-byte --limit 10`.

## Notes

- The source of this file is `netferry-relay/internal/skill/skill.md` in the
  netferry repo. The installed copy is overwritten by `install-claude-skill`;
  edit the source and bump `version:` instead.
