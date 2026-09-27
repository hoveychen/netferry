#!/usr/bin/env python3
"""Build offline routing-purpose scopes from pinned geographic and GFW lists."""

from __future__ import annotations

import base64
import io
import json
import re
import tarfile
from pathlib import Path
from urllib.request import urlopen

from build_service_catalog import REVISION as DLC_REVISION, expand, source_files

GFW_REVISION = "3e23962592b28d64fdd7cc76505bc22af6ac75c8"
GFW_SOURCE = "https://github.com/gfwlist/gfwlist"
OUTPUT = Path(__file__).resolve().parents[1] / "netferry-desktop/src/data/routingDomains.json"
DOMAIN_RULE = re.compile(r"^(?:[a-z0-9-]+\.)+[a-z0-9-]+\^?$", re.IGNORECASE)


def gfw_domains(lines: list[str]) -> set[str]:
    """Keep domain-anchored rules; drop URL patterns and exception overlaps."""
    domains: set[str] = set()
    exceptions: set[str] = set()
    for line in lines:
        line = line.strip().lower()
        if line.startswith("@@||"):
            candidate = line[4:].removesuffix("^")
            if candidate.startswith("*."):
                candidate = candidate[2:]
            if DOMAIN_RULE.fullmatch(candidate):
                exceptions.add(candidate)
        elif line.startswith("||"):
            candidate = line[2:].removesuffix("^")
            if DOMAIN_RULE.fullmatch(candidate):
                domains.add(candidate)
    # An allow rule for one host within a broad suffix cannot be represented
    # by a single positive scope. Omit the broad scope rather than mislabel it.
    return {
        domain for domain in domains
        if not any(exc == domain or exc.endswith("." + domain) or domain.endswith("." + exc) for exc in exceptions)
    }


def build() -> dict:
    dlc_url = f"https://codeload.github.com/v2fly/domain-list-community/tar.gz/{DLC_REVISION}"
    with urlopen(dlc_url, timeout=30) as response:
        files = source_files(tarfile.open(fileobj=io.BytesIO(response.read()), mode="r:gz"))
    gfw_url = f"https://raw.githubusercontent.com/gfwlist/gfwlist/{GFW_REVISION}/gfwlist.txt"
    with urlopen(gfw_url, timeout=30) as response:
        lines = base64.b64decode(b"".join(response.read().split()), validate=True).decode("utf-8").splitlines()
    return {
        "sources": [
            {"name": "V2Fly domain-list-community", "url": "https://github.com/v2fly/domain-list-community", "revision": DLC_REVISION, "license": "MIT"},
            {"name": "GFWList", "url": GFW_SOURCE, "revision": GFW_REVISION, "license": "LGPL-2.1"},
            {"name": "OpenAI API supported countries", "url": "https://developers.openai.com/api/docs/supported-countries", "revision": "2026-09-28", "license": "link-only"},
        ],
        "scopes": [
            {"id": "mainland-access", "name": "Mainland access point", "nameZh": "内地有接入点", "source": "V2Fly geolocation-cn", "suggestedRoute": "direct", "domains": sorted(expand("geolocation-cn", files, set(), {}))},
            {"id": "no-mainland-access", "name": "No mainland access point", "nameZh": "内地无接入点", "source": "V2Fly geolocation-!cn", "suggestedRoute": "default", "domains": sorted(expand("geolocation-!cn", files, set(), {}))},
            {"id": "gfwlist", "name": "Listed by GFWList", "nameZh": "GFWList 收录", "source": "GFWList", "suggestedRoute": "default", "domains": sorted(gfw_domains(lines))},
            {"id": "region-limited", "name": "Verified regional restriction", "nameZh": "已核实的服务地区限制", "source": "OpenAI API supported countries", "suggestedRoute": "default", "domains": ["=api.openai.com"]},
        ],
    }


if __name__ == "__main__":
    OUTPUT.parent.mkdir(parents=True, exist_ok=True)
    data = build()
    OUTPUT.write_text(json.dumps(data, ensure_ascii=False, separators=(",", ":")) + "\n")
    for scope in data["scopes"]:
        print(f"{scope['id']}: {len(scope['domains'])} domains")
    print(f"Wrote {OUTPUT}")
