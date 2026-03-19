#!/usr/bin/env python3
"""Build NetFerry tunnel sidecar (Go) and copy into Tauri binaries directory."""

from __future__ import annotations

import argparse
import os
import subprocess
import sys
from pathlib import Path


# Rust target triple → (GOOS, GOARCH, exe_suffix)
TARGET_MAP: dict[str, tuple[str, str, str]] = {
    "aarch64-apple-darwin":      ("darwin",   "arm64", ""),
    "x86_64-apple-darwin":       ("darwin",   "amd64", ""),
    "x86_64-unknown-linux-gnu":  ("linux",    "amd64", ""),
    "aarch64-unknown-linux-gnu": ("linux",    "arm64", ""),
    "x86_64-pc-windows-msvc":    ("windows",  "amd64", ".exe"),
}

# All remote-server cross-compilation targets (embedded in the tunnel binary).
SERVER_TARGETS: list[tuple[str, str]] = [
    ("linux",  "amd64"),
    ("linux",  "arm64"),
    ("linux",  "mipsle"),
    ("darwin", "amd64"),
    ("darwin", "arm64"),
]


def run(cmd: list[str], cwd: Path, env: dict[str, str] | None = None) -> None:
    print("+", " ".join(cmd))
    subprocess.run(cmd, cwd=cwd, check=True, env=env)


def detect_target() -> str:
    out = subprocess.check_output(["rustc", "-Vv"], text=True)
    for line in out.splitlines():
        if line.startswith("host:"):
            return line.split(":", 1)[1].strip()
    raise RuntimeError("Unable to parse host triple from rustc -Vv")


def get_version(relay_dir: Path) -> str:
    try:
        return subprocess.check_output(
            ["git", "-C", str(relay_dir.parent), "describe", "--tags", "--always", "--dirty"],
            text=True,
            stderr=subprocess.DEVNULL,
        ).strip()
    except subprocess.CalledProcessError:
        return "dev"


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--target", help="Rust target triple; defaults to current host")
    args = parser.parse_args()

    workspace = Path(__file__).resolve().parents[2]
    project = Path(__file__).resolve().parents[1]
    relay_dir = workspace / "netferry-relay"
    binaries_dir = project / "src-tauri" / "binaries"
    binaries_dir.mkdir(parents=True, exist_ok=True)

    target = args.target or detect_target()
    if target not in TARGET_MAP:
        raise RuntimeError(
            f"Unsupported target: {target}\n"
            f"Supported: {', '.join(TARGET_MAP)}"
        )

    goos_target, goarch_target, exe_suffix = TARGET_MAP[target]
    version = get_version(relay_dir)
    ldflags = f"-X main.Version={version} -s -w"

    base_env = {**os.environ, "CGO_ENABLED": "0"}

    # Step 1: Build all remote server binaries (embedded into the tunnel binary).
    # These always run on Linux/macOS remotes, so we never build Windows server binaries.
    # go:embed paths are relative to cmd/tunnel/, so binaries live under cmd/tunnel/binaries/.
    relay_binaries = relay_dir / "cmd" / "tunnel" / "binaries"
    relay_binaries.mkdir(parents=True, exist_ok=True)

    for goos, goarch in SERVER_TARGETS:
        out_name = f"server-{goos}-{goarch}"
        env = {**base_env, "GOOS": goos, "GOARCH": goarch}
        run(
            ["go", "build", f"-ldflags={ldflags}", "-o", str(relay_binaries / out_name), "./cmd/server"],
            cwd=relay_dir,
            env=env,
        )
        print(f"  built cmd/tunnel/binaries/{out_name}")

    # Step 2: Cross-compile the tunnel binary for the requested target.
    tunnel_name = f"netferry-tunnel-{target}{exe_suffix}"
    tunnel_out = binaries_dir / tunnel_name
    env = {**base_env, "GOOS": goos_target, "GOARCH": goarch_target}
    run(
        ["go", "build", f"-ldflags={ldflags}", "-o", str(tunnel_out), "./cmd/tunnel"],
        cwd=relay_dir,
        env=env,
    )
    if not exe_suffix:
        tunnel_out.chmod(0o755)
    print(f"Sidecar generated: {tunnel_out}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
