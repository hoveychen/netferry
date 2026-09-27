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
    ("youtube", "YouTube", None, False),
    ("openai", "OpenAI", None, False),
    ("anthropic", "Anthropic", None, False),
    ("github", "GitHub", None, False),
    ("discord", "Discord", None, False),
    ("telegram", "Telegram", None, False),
    ("lark", "Lark", None, False),
    ("whatsapp", "WhatsApp", None, False),
    ("stripe", "Stripe", None, False),
    ("pinterest", "Pinterest", None, False),
    ("binance", "Binance", None, False),
    ("pixiv", "Pixiv", None, False),
    ("reddit", "Reddit", None, False),
    ("zoom", "Zoom", None, False),
    ("shopify", "Shopify", None, False),
    ("deepseek", "DeepSeek", None, False),
    ("apple", "Apple", None, False),
    ("microsoft", "Microsoft", None, False),
    ("google", "Google", None, False),
    ("tencent", "Tencent", "腾讯", False),
    ("bytedance", "ByteDance", "字节跳动", False),
    ("meta", "Meta", None, False),
    ("amazon", "Amazon", None, False),
    ("baidu", "Baidu", "百度", False),
    ("category-ads-all", "Ads & tracking", "广告与跟踪", True),
    ("category-finance", "Finance", "金融服务", False),
    ("category-dev", "Developer tools", "开发工具", False),
    ("category-social-media-!cn", "Social media", "社交媒体", False),
    ("category-entertainment", "Entertainment", "娱乐与影音", False),
    ("category-ecommerce", "E-commerce", "电商", False),
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


def expand(
    name: str,
    files: dict[str, str],
    seen: set[tuple[str, frozenset[str]]],
    skipped: dict[str, int],
    required_tags: frozenset[str] = frozenset(),
    include_ads: bool = False,
) -> set[str]:
    visit = (name, required_tags)
    if visit in seen:
        return set()
    if name not in files:
        raise ValueError(f"missing included list: {name}")
    seen.add(visit)
    domains: set[str] = set()
    for line in files[name].splitlines():
        parts = line.split("#", 1)[0].strip().lower().split()
        if not parts:
            continue
        token = parts[0]
        tags = frozenset(part[1:] for part in parts[1:] if part.startswith("@"))
        if "ads" in tags and not include_ads:
            continue
        if token.startswith("include:"):
            domains.update(expand(token[8:], files, seen, skipped, required_tags | tags, include_ads))
            continue
        if not required_tags.issubset(tags):
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
    for key, label, label_zh, include_ads in SERVICES:
        skipped: dict[str, int] = {}
        domains = sorted(expand(key, files, set(), skipped, include_ads=include_ads))
        service = {"id": key, "name": label, "domains": domains}
        if label_zh:
            service["nameZh"] = label_zh
        services.append(service)
        print(f"{label}: {len(domains)} scopes, skipped {skipped}")
    OUTPUT.parent.mkdir(parents=True, exist_ok=True)
    OUTPUT.write_text(json.dumps({"source": SOURCE, "revision": args.revision, "license": "MIT", "services": services}, ensure_ascii=False, separators=(",", ":")) + "\n")
    print(f"Wrote {OUTPUT}")


if __name__ == "__main__":
    main()
