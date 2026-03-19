package main

import (
	"embed"

	"github.com/hoveychen/netferry/relay/internal/deploy"
)

// serverBinaries holds all cross-compiled server binaries.
// They are embedded at build time after running `make build-servers`.
//
// If a binary for a platform is missing (e.g. during development before the
// first `make build-servers`), the embed directive will fail at compile time.
// Run `make build-servers` (or create stub files) before building the tunnel.
//
//go:embed binaries/server-linux-amd64
//go:embed binaries/server-linux-arm64
//go:embed binaries/server-linux-mipsle
//go:embed binaries/server-darwin-amd64
//go:embed binaries/server-darwin-arm64
var serverBinaries embed.FS

func init() {
	deploy.ServerBinaries = serverBinaries
}
