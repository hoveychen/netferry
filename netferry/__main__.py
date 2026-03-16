from pathlib import Path
import os
import sys


def _bootstrap_path() -> None:
    repo_root = Path(__file__).resolve().parents[1]
    upstream_root = repo_root / "third_party" / "sshuttle"
    if upstream_root.exists():
        sys.path.insert(0, str(upstream_root))


def _patch_frozen_get_module_source() -> None:
    """Patch sshuttle.ssh.get_module_source for PyInstaller frozen binaries.

    sshuttle reads its own Python source files and ships them over SSH to
    bootstrap the remote server side.  PyInstaller compiles source to bytecode
    and does not put .py text files on disk, so importlib.util.find_spec()
    returns None (or a spec with origin=None) for frozen modules.

    We work around this by:
    1. Copying sshuttle's .py sources into the PyInstaller bundle as data
       files (via --add-data in build_sidecar.py).  At runtime they are
       extracted to sys._MEIPASS/sshuttle/.
    2. Replacing get_module_source() with a version that reads from
       sys._MEIPASS when the normal spec-based lookup fails.
    """
    if not getattr(sys, "frozen", False):
        return  # only needed inside a PyInstaller bundle

    import importlib.util
    import sshuttle.ssh as ssh_mod

    meipass: str = getattr(sys, "_MEIPASS", "")

    def _patched_get_module_source(name: str) -> bytes:
        # Try the normal path first (works when spec.origin is a readable file).
        spec = importlib.util.find_spec(name)
        if spec is not None and spec.origin is not None:
            try:
                with open(spec.origin, "rt", encoding="utf-8") as fh:
                    return fh.read().encode("utf-8")
            except OSError:
                pass

        # Frozen fallback: look for the .py text file in _MEIPASS.
        # Module 'sshuttle.helpers'  → <_MEIPASS>/sshuttle/helpers.py
        # Package 'sshuttle'         → <_MEIPASS>/sshuttle/__init__.py
        rel = name.replace(".", os.sep)
        for candidate in (
            os.path.join(meipass, rel + ".py"),           # regular module
            os.path.join(meipass, rel, "__init__.py"),    # package init
        ):
            if os.path.isfile(candidate):
                with open(candidate, "rt", encoding="utf-8") as fh:
                    return fh.read().encode("utf-8")

        raise RuntimeError(
            f"Cannot find source for module {name!r} in frozen binary. "
            f"Expected one of:\n"
            f"  {os.path.join(meipass, rel + '.py')}\n"
            f"  {os.path.join(meipass, rel, '__init__.py')}"
        )

    ssh_mod.get_module_source = _patched_get_module_source


_bootstrap_path()
_patch_frozen_get_module_source()

from sshuttle.cmdline import main  # noqa: E402


if __name__ == "__main__":
    raise SystemExit(main())
