#!/usr/bin/env python3
"""Build NetFerry tunnel sidecar (Go) and copy into Tauri binaries directory."""

from __future__ import annotations

import argparse
import io
import os
import subprocess
import sys
import zipfile
from pathlib import Path
from urllib.request import urlopen


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
    from datetime import datetime
    d = datetime.now()
    return f"{d.year % 100}.{d.month}.{d.day}-{int(d.timestamp())}"


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--target", help="Rust target triple; defaults to current host")
    parser.add_argument("--version", help="Override version string (e.g. passed from build_mac_local.sh)")
    args = parser.parse_args()

    workspace = Path(__file__).resolve().parents[1]
    project = workspace / "netferry-desktop"
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
    version = args.version if args.version else get_version(relay_dir)
    ldflags = f"-X main.Version={version} -s -w"

    # Mirror netferry-desktop/src-tauri/build.rs: fall back to reading
    # netferry-desktop/.env when NETFERRY_EXPORT_KEY is not already exported.
    export_key = os.environ.get("NETFERRY_EXPORT_KEY", "").strip()
    if not export_key:
        env_path = project / ".env"
        if env_path.is_file():
            for line in env_path.read_text().splitlines():
                line = line.strip()
                if not line or line.startswith("#"):
                    continue
                k, _, v = line.partition("=")
                if k.strip() == "NETFERRY_EXPORT_KEY":
                    export_key = v.strip()
                    break

    tunnel_ldflags = ldflags
    if export_key:
        tunnel_ldflags = (
            f"-X main.Version={version} "
            f"-X github.com/hoveychen/netferry/relay/internal/profile.ExportKey={export_key} "
            f"-s -w"
        )
        print("  NETFERRY_EXPORT_KEY detected — tunnel binary will support .nfprofile files")
    else:
        print("  NETFERRY_EXPORT_KEY not set — tunnel --profile will be disabled")

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
        ["go", "build", f"-ldflags={tunnel_ldflags}", "-o", str(tunnel_out), "./cmd/tunnel"],
        cwd=relay_dir,
        env=env,
    )
    if not exe_suffix:
        tunnel_out.chmod(0o755)
    print(f"Sidecar generated: {tunnel_out}")

    # Step 3: WinDivert DLL and driver.
    # Download to cmd/tunnel/windivert/ for Go embed — the tunnel binary
    # extracts them at runtime to a fixed versioned directory under
    # %LOCALAPPDATA%. They are NOT shipped in the Tauri installer bundle
    # (removing them from the install directory avoids kernel driver file
    # locking issues during upgrades).
    tunnel_windivert_dir = relay_dir / "cmd" / "tunnel" / "windivert"
    tunnel_windivert_dir.mkdir(parents=True, exist_ok=True)
    fetch_windivert(tunnel_windivert_dir)

    return 0


# WinDivert version to bundle with the Windows sidecar.
WINDIVERT_VERSION = "2.2.2"
WINDIVERT_URL = (
    f"https://github.com/basil00/Divert/releases/download/"
    f"v{WINDIVERT_VERSION}/WinDivert-{WINDIVERT_VERSION}-A.zip"
)


def fetch_windivert(binaries_dir: Path) -> None:
    """Download WinDivert and extract WinDivert.dll + WinDivert64.sys."""
    dll_path = binaries_dir / "WinDivert.dll"
    sys_path = binaries_dir / "WinDivert64.sys"

    if dll_path.exists() and sys_path.exists():
        print(f"WinDivert already present in {binaries_dir}")
        return

    print(f"Downloading WinDivert {WINDIVERT_VERSION}...")
    resp = urlopen(WINDIVERT_URL)
    data = resp.read()

    prefix = f"WinDivert-{WINDIVERT_VERSION}-A/"
    with zipfile.ZipFile(io.BytesIO(data)) as zf:
        for name, dest in [
            (f"{prefix}x64/WinDivert.dll", dll_path),
            (f"{prefix}x64/WinDivert64.sys", sys_path),
        ]:
            print(f"  extracting {name} -> {dest}")
            dest.write_bytes(zf.read(name))

    print("WinDivert files ready.")


if __name__ == "__main__":
    raise SystemExit(main())
