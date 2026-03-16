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

    # Use our netferry package as the entry point so that the frozen binary
    # gets the get_module_source() patch applied before main() is called.
    entry = workspace / "netferry" / "__main__.py"
    if not entry.exists():
        raise RuntimeError(f"netferry entrypoint not found: {entry}")

    # sshuttle's core mechanism is to read its own Python source files and
    # ship them over SSH to bootstrap the remote server.  PyInstaller normally
    # compiles .py to bytecode and omits the source text, so we must:
    #  (a) add all sshuttle .py files as data so they land in sys._MEIPASS
    #      at runtime (our patched get_module_source reads them from there).
    #  (b) list every module referenced only by string in get_module_source /
    #      empackage as a hidden import so PyInstaller includes them.
    sshuttle_pkg = workspace / "third_party" / "sshuttle" / "sshuttle"
    sep = ";" if sys.platform == "win32" else ":"
    add_data_args = ["--add-data", f"{sshuttle_pkg}{sep}sshuttle"]

    hidden_imports = [
        # Modules referenced only as strings in get_module_source()/empackage()
        # in ssh.py — PyInstaller's static analysis cannot find these.
        # Note: sshuttle.cmdline_options is intentionally omitted because
        # empackage() is always called with explicit optdata for it, bypassing
        # get_module_source().
        "sshuttle.assembler",
        "sshuttle.helpers",
        "sshuttle.ssnet",
        "sshuttle.hostwatch",
        "sshuttle.server",
        # Firewall method modules loaded dynamically via importlib
        "sshuttle.methods.nat",
        "sshuttle.methods.nft",
        "sshuttle.methods.pf",
        "sshuttle.methods.ipfw",
        "sshuttle.methods.tproxy",
        "sshuttle.methods.windivert",
    ]
    hidden_import_args: list[str] = []
    for hi in hidden_imports:
        hidden_import_args += ["--hidden-import", hi]

    cmd = [
        args.python,
        "-m",
        "PyInstaller",
        "--noconfirm",
        "--clean",
        "--onefile",
        "--name",
        "sshuttle",
        *add_data_args,
        *hidden_import_args,
        str(entry),
    ]
    env = dict(os.environ)
    # Both the netferry wrapper package and the upstream sshuttle must be on
    # the module search path so PyInstaller can trace all dependencies.
    env["PYTHONPATH"] = os.pathsep.join([
        str(workspace),
        str(workspace / "third_party" / "sshuttle"),
    ])
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
