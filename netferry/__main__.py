from pathlib import Path
import sys


def _bootstrap_path() -> None:
    repo_root = Path(__file__).resolve().parents[1]
    upstream_root = repo_root / "third_party" / "sshuttle"
    if upstream_root.exists():
        sys.path.insert(0, str(upstream_root))


_bootstrap_path()

from sshuttle.cmdline import main


if __name__ == "__main__":
    raise SystemExit(main())
