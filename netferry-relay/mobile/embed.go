package mobile

import (
	"embed"

	"github.com/hoveychen/netferry/relay/internal/deploy"
)

// version is set at build time via ldflags:
//
//	gomobile bind -ldflags="-X github.com/hoveychen/netferry/relay/mobile.version=1.0.0" ...
var version = "dev"

// GetVersion returns the engine version string set at build time.
func GetVersion() string { return version }

// serverBinaries holds cross-compiled server binaries for deployment to
// remote hosts. Embedded at build time — run `make build-servers` first.
//
//go:embed binaries/server-linux-amd64
//go:embed binaries/server-linux-arm64
var serverBinaries embed.FS

func init() {
	deploy.ServerBinaries = serverBinaries
}
