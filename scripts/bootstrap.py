"""Install pinned tooling inside .tools. Run with the system Python (3.9+)."""
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import tarfile
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
TOOLS = ROOT / ".tools"
GO_VERSION = "1.27.1"
UV_VERSION = "0.12.10"


def main():
    target_os = {"Darwin": "darwin", "Linux": "linux"}.get(platform.system())
    arch = {"arm64": "arm64", "aarch64": "arm64", "x86_64": "amd64"}.get(platform.machine())
    if not target_os or not arch:
        raise SystemExit("Bootstrap supports macOS/Linux arm64/amd64 only.")
    TOOLS.mkdir(exist_ok=True)
    go = TOOLS / "go/bin/go"
    if not go.exists():
        with urllib.request.urlopen("https://go.dev/dl/?mode=json&include=all", timeout=60) as response:
            releases = json.load(response)
        release = next(r for r in releases if r["version"] == "go" + GO_VERSION)
        item = next(f for f in release["files"] if f["os"] == target_os and f["arch"] == arch and f["kind"] == "archive")
        archive = TOOLS / item["filename"]
        print("Downloading", item["filename"], flush=True)
        with urllib.request.urlopen("https://go.dev/dl/" + item["filename"], timeout=60) as response, archive.open("wb") as output:
            shutil.copyfileobj(response, output)
        digest = hashlib.sha256(archive.read_bytes()).hexdigest()
        if digest != item["sha256"]:
            raise SystemExit("Go archive checksum mismatch")
        with tarfile.open(archive) as package:
            for member in package.getmembers():
                if (member.name != "go" and not member.name.startswith("go/")) or ".." in Path(member.name).parts or member.issym() or member.islnk():
                    raise SystemExit("Unexpected Go archive entry")
            package.extractall(TOOLS)
        archive.unlink()
    go_actual = subprocess.check_output([str(go), "version"], text=True).strip()
    if go_actual.split()[2] != "go" + GO_VERSION:
        raise SystemExit("Unexpected Go version: " + go_actual)
    print(go_actual)
    uv = TOOLS / "uv/bin/uv"
    if not uv.exists():
        subprocess.run([os.sys.executable, "-m", "pip", "install", "--disable-pip-version-check", "--target", str(TOOLS / "uv"), "uv==" + UV_VERSION], check=True)
    actual = subprocess.check_output([str(uv), "--version"], text=True).strip()
    if actual.split()[:2] != ["uv", UV_VERSION]:
        raise SystemExit("Unexpected uv version: " + actual)
    print(actual)
    print("Tooling installed. See docs/07-dependencies-and-development.md for sync/check commands.")


if __name__ == "__main__":
    main()
