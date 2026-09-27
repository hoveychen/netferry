#!/usr/bin/env python3
"""Build the offline service-scope suggestion catalog from a pinned DLC release."""

from __future__ import annotations

import argparse
import io
import json
import re
import tarfile
from pathlib import Path
from urllib.request import urlopen

REVISION = "bcea25493ed28c387660fe49ce1ceb242d2efca0"
SOURCE = "https://github.com/v2fly/domain-list-community"
SERVICES = (
    ("youtube", "YouTube"),
    ("openai", "OpenAI"),
    ("anthropic", "Anthropic"),
    ("github", "GitHub"),
    ("discord", "Discord"),
    ("telegram", "Telegram"),
    ("apple", "Apple"),
    ("microsoft", "Microsoft"),
    ("google", "Google"),
)
OUTPUT = Path(__file__).resolve().parents[1] / "netferry-desktop/src/data/serviceDomains.json"
VALID_HOST = re.compile(r"^[a-z0-9.-]+$")


def source_files(archive: tarfile.TarFile) -> dict[str, str]:
    files: dict[str, str] = {}
    for member in archive.getmembers():
        if not member.isfile() or "/data/" not in member.name:
            continue
        name = member.name.split("/data/", 1)[1]
        if "/" in name:
            continue
        raw = archive.extractfile(member)
        if raw:
            files[name] = raw.read().decode("utf-8")
    return files


def expand(name: str, files: dict[str, str], seen: set[str], skipped: dict[str, int]) -> set[str]:
    if name in seen:
        return set()
    if name not in files:
        raise ValueError(f"missing included list: {name}")
    seen.add(name)
    domains: set[str] = set()
    for line in files[name].splitlines():
        token = line.split("#", 1)[0].strip().split(" ", 1)[0].lower()
        if not token or "@ads" in line:
            continue
        if token.startswith("include:"):
            domains.update(expand(token[8:], files, seen, skipped))
            continue
        exact = token.startswith("full:")
        host = token[5:] if exact else token[7:] if token.startswith("domain:") else token
        if host.startswith(("regexp:", "keyword:")) or ":" in host:
            skipped["unsupported"] = skipped.get("unsupported", 0) + 1
            continue
        if "." not in host or not VALID_HOST.fullmatch(host) or ".." in host:
            skipped["invalid"] = skipped.get("invalid", 0) + 1
            continue
        domains.add(f"={host}" if exact else host)
    return domains


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--revision", default=REVISION, help="Git commit to pin")
    args = parser.parse_args()
    url = f"https://codeload.github.com/v2fly/domain-list-community/tar.gz/{args.revision}"
    with urlopen(url, timeout=30) as response:
        archive = tarfile.open(fileobj=io.BytesIO(response.read()), mode="r:gz")
    files = source_files(archive)
    if not files:
        raise RuntimeError("source archive has no data lists")
    services = []
    for key, label in SERVICES:
        skipped: dict[str, int] = {}
        domains = sorted(expand(key, files, set(), skipped))
        services.append({"id": key, "name": label, "domains": domains})
        print(f"{label}: {len(domains)} scopes, skipped {skipped}")
    OUTPUT.parent.mkdir(parents=True, exist_ok=True)
    OUTPUT.write_text(json.dumps({"source": SOURCE, "revision": args.revision, "license": "MIT", "services": services}, ensure_ascii=False, separators=(",", ":")) + "\n")
    print(f"Wrote {OUTPUT}")


if __name__ == "__main__":
    main()
