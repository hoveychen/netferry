VERSION := $(shell date +'%y.%-m.%-d-%s')
LDFLAGS := -ldflags "-X main.Version=$(VERSION) -s -w"

# Server cross-compilation targets (remote side, zero CGO)
SERVER_TARGETS := \
    linux/amd64 \
    linux/arm64

HOST_GOOS   := $(shell go env GOOS)
HOST_GOARCH := $(shell go env GOARCH)

.PHONY: all build-servers build-tunnel package-sidecar clean tidy

all: build-tunnel

build-servers:
	@mkdir -p cmd/tunnel/binaries
	$(foreach T,$(SERVER_TARGETS), \
		GOOS=$(word 1,$(subst /, ,$(T))) \
		GOARCH=$(word 2,$(subst /, ,$(T))) \
		CGO_ENABLED=0 \
		go build $(LDFLAGS) \
		    -o cmd/tunnel/binaries/server-$(subst /,-,$(T)) \
		    ./cmd/server && \
		echo "  built cmd/tunnel/binaries/server-$(subst /,-,$(T))"; \
	)

build-tunnel: build-servers
	@mkdir -p dist
	GOOS=$(HOST_GOOS) GOARCH=$(HOST_GOARCH) CGO_ENABLED=0 \
	    go build $(LDFLAGS) -o dist/netferry-tunnel ./cmd/tunnel
	@echo "  built dist/netferry-tunnel"

# Copy built tunnel binary into Tauri binaries dir with the right triple name.
# Usage: make package-sidecar TAURI_TARGET=aarch64-apple-darwin
package-sidecar: build-tunnel
	@test -n "$(TAURI_TARGET)" || (echo "TAURI_TARGET not set"; exit 1)
	cp dist/netferry-tunnel \
	   ../netferry-desktop/src-tauri/binaries/netferry-tunnel-$(TAURI_TARGET)
	@echo "  copied to netferry-tunnel-$(TAURI_TARGET)"

tidy:
	go mod tidy

clean:
	rm -rf cmd/tunnel/binaries dist
