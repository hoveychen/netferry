# Detect host Rust target triple automatically
RUST_TARGET ?= $(shell rustc -Vv 2>/dev/null | awk '/^host:/{print $$2}')

DESKTOP_DIR  := netferry-desktop
RELAY_DIR    := netferry-relay
BUNDLE_DIR   := $(DESKTOP_DIR)/src-tauri/target/$(RUST_TARGET)/release/bundle

.PHONY: all build deps sidecar bundle clean help

all: build

## build: Full build pipeline (deps → sidecar → bundle)
build: deps sidecar bundle

## deps: Install npm packages
deps: $(DESKTOP_DIR)/node_modules

$(DESKTOP_DIR)/node_modules: $(DESKTOP_DIR)/package-lock.json
	cd $(DESKTOP_DIR) && npm ci

## sidecar: Build netferry-tunnel Go sidecar
sidecar:
	python3 scripts/build_sidecar.py --target $(RUST_TARGET)

## bundle: Compile Tauri app and produce .app bundle
bundle: $(DESKTOP_DIR)/node_modules
	cd $(DESKTOP_DIR) && CI=false npm run tauri build -- --target $(RUST_TARGET)
	@echo ""
	@echo "Build artifacts:"
	@ls -1 $(BUNDLE_DIR)/macos/ 2>/dev/null | sed 's/^/  macos\//'
	@ls -1 $(BUNDLE_DIR)/pkg/   2>/dev/null | sed 's/^/  pkg\//'

## relay: Build relay binaries only (server + tunnel)
relay:
	cd $(RELAY_DIR) && $(MAKE) build-tunnel

## clean: Remove build artifacts and caches
clean:
	rm -rf $(DESKTOP_DIR)/src-tauri/binaries
	rm -rf $(DESKTOP_DIR)/src-tauri/target
	rm -rf $(DESKTOP_DIR)/dist
	cd $(RELAY_DIR) && $(MAKE) clean

## help: Show available targets
help:
	@grep -E '^## ' Makefile | sed 's/^## /  make /'
