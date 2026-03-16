#!/usr/bin/env python3
"""Build NetFerry tunnel sidecar and copy into Tauri binaries directory."""

from __future__ import annotations

import argparse
import os
import shutil
import subprocess
import sys
from pathlib import Path


TARGET_MAP = {
    "aarch64-apple-darwin": "netferry-tunnel-aarch64-apple-darwin",
    "x86_64-apple-darwin": "netferry-tunnel-x86_64-apple-darwin",
    "x86_64-unknown-linux-gnu": "netferry-tunnel-x86_64-unknown-linux-gnu",
    "aarch64-unknown-linux-gnu": "netferry-tunnel-aarch64-unknown-linux-gnu",
    "x86_64-pc-windows-msvc": "netferry-tunnel-x86_64-pc-windows-msvc.exe",
}


def run(cmd: list[str], cwd: Path) -> None:
    print("+", " ".join(cmd))
    subprocess.run(cmd, cwd=cwd, check=True)


def detect_target() -> str:
    out = subprocess.check_output(["rustc", "-Vv"], text=True)
    for line in out.splitlines():
        if line.startswith("host:"):
            return line.split(":", 1)[1].strip()
    raise RuntimeError("Unable to parse host triple from rustc -Vv")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--target", help="Rust target triple; defaults to current host")
    parser.add_argument(
        "--python",
        default=sys.executable,
        help="Python executable used to run PyInstaller",
    )
    args = parser.parse_args()

    workspace = Path(__file__).resolve().parents[2]
    project = Path(__file__).resolve().parents[1]
    binaries_dir = project / "src-tauri" / "binaries"
    binaries_dir.mkdir(parents=True, exist_ok=True)

    target = args.target or detect_target()
    if target not in TARGET_MAP:
        raise RuntimeError(f"Unsupported target: {target}")

    output_name = TARGET_MAP[target]
    build_dir = project / ".sidecar-build"
    if build_dir.exists():
        shutil.rmtree(build_dir)
    build_dir.mkdir(parents=True, exist_ok=True)

    entry = workspace / "third_party" / "sshuttle" / "sshuttle" / "__main__.py"
    if not entry.exists():
        raise RuntimeError(f"sshuttle entrypoint not found: {entry}")

    cmd = [
        args.python,
        "-m",
        "PyInstaller",
        "--noconfirm",
        "--clean",
        "--onefile",
        "--name",
        "sshuttle",
        str(entry),
    ]
    env = dict(os.environ)
    env["PYTHONPATH"] = str(workspace / "third_party" / "sshuttle")
    print("+", " ".join(cmd))
    subprocess.run(cmd, cwd=build_dir, env=env, check=True)

    dist_name = "sshuttle.exe" if sys.platform == "win32" else "sshuttle"
    built = build_dir / "dist" / dist_name
    if not built.exists():
        raise RuntimeError(f"PyInstaller output not found: {built}")

    target_bin = binaries_dir / output_name
    shutil.copy2(built, target_bin)
    if not target_bin.name.endswith(".exe"):
        target_bin.chmod(0o755)
    print(f"Sidecar generated: {target_bin}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
