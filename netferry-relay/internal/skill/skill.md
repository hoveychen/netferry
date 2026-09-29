---
name: netferry-tunnel
version: 1
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

Lists active and recently closed connections of the running tunnel (newest
last). The tunnel keeps the last 4096 closed connections, including ones that
were blocked or failed to open. Does not need root.

Flags:

- `--host <s>` — only connections whose host or dst contains `s` (case-insensitive).
- `--since <d>` — drop connections that closed before this: a duration (`10m`) or unix ms.
- `--errors` — only connections that ended with an error.
- `--limit <n>` — newest n active and n closed (default 200, 0 = all).
- `--json` — raw JSON (`{"active":[...],"closed":[...]}`), for filtering with jq.
- `--port <n>` — stats server port; normally found automatically ($NETFERRY_STATS_PORT, then the port cache).

```bash
netferry-tunnel conns --host github --since 10m
netferry-tunnel conns --errors --since 30m
netferry-tunnel conns --json --since 5m | jq '.closed[] | select(.route=="direct")'
```

Line format:

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
  connection are not split apart.

The same data is served at
`GET http://127.0.0.1:<stats-port>/connections?host=&since=&errors=&limit=`.

### `netferry-tunnel install-claude-skill`

Installs or updates this skill into `~/.claude/skills/netferry-tunnel/SKILL.md`,
recording the binary's current path. `conns` also re-runs it automatically when
the installed copy is older than the embedded `version:`.

## Diagnosing a failing request

1. Reproduce the request (or note when it failed).
2. `netferry-tunnel conns --host <hostname> --since 5m` — did the connection go
   through the tunnel or direct, did it get a first byte, how did it end?
3. No line for the host at all: the traffic did not reach NetFerry (excluded
   subnet, IPv6, UDP other than DNS, or the app uses its own proxy).
4. `netferry-tunnel conns --errors --since 10m` to see whether failures are
   host-specific or everything through the tunnel is failing.

## Notes

- The source of this file is `netferry-relay/internal/skill/skill.md` in the
  netferry repo. The installed copy is overwritten by `install-claude-skill`;
  edit the source and bump `version:` instead.
