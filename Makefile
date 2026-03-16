# Detect host Rust target triple automatically
RUST_TARGET ?= $(shell rustc -Vv 2>/dev/null | awk '/^host:/{print $$2}')

VENV_DIR     := .venv-build
PYTHON       := python3
VENV_PYTHON  := $(VENV_DIR)/bin/python3
DESKTOP_DIR  := netferry-desktop
BUNDLE_DIR   := $(DESKTOP_DIR)/src-tauri/target/$(RUST_TARGET)/release/bundle

.PHONY: all build deps sidecar bundle clean help

all: build

## build: Full build pipeline (deps → sidecar → bundle)
build: deps sidecar bundle

## deps: Install npm packages and create Python venv with PyInstaller
deps: $(VENV_PYTHON) $(DESKTOP_DIR)/node_modules

$(VENV_PYTHON):
	$(PYTHON) -m venv $(VENV_DIR)
	$(VENV_PYTHON) -m pip install --quiet --upgrade pip
	$(VENV_PYTHON) -m pip install --quiet pyinstaller

$(DESKTOP_DIR)/node_modules: $(DESKTOP_DIR)/package-lock.json
	cd $(DESKTOP_DIR) && npm ci

## sidecar: Build sshuttle Python binary via PyInstaller
sidecar: $(VENV_PYTHON)
	cd $(DESKTOP_DIR) && $(CURDIR)/$(VENV_PYTHON) scripts/build_sidecar.py \
		--target $(RUST_TARGET) \
		--python $(CURDIR)/$(VENV_PYTHON)

## bundle: Compile Tauri app and produce .app / .dmg
bundle: $(DESKTOP_DIR)/node_modules
	cd $(DESKTOP_DIR) && CI=false npm run tauri build -- --target $(RUST_TARGET)
	@echo ""
	@echo "Build artifacts:"
	@ls -1 $(BUNDLE_DIR)/macos/ 2>/dev/null | sed 's/^/  macos\//'
	@ls -1 $(BUNDLE_DIR)/dmg/   2>/dev/null | sed 's/^/  dmg\//'

## clean: Remove build artifacts and caches
clean:
	rm -rf $(VENV_DIR)
	rm -rf $(DESKTOP_DIR)/.sidecar-build
	rm -rf $(DESKTOP_DIR)/src-tauri/binaries
	rm -rf $(DESKTOP_DIR)/src-tauri/target
	rm -rf $(DESKTOP_DIR)/dist

## help: Show available targets
help:
	@grep -E '^## ' Makefile | sed 's/^## /  make /'
