"""Seal/verify the declared design artifacts, not unimplemented application features."""
import argparse
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
MANIFEST = ROOT / "config/design-freeze-v1.json"


def snapshot():
    paths = [ROOT / "README.md", *sorted((ROOT / "docs").glob("0[0-9]-*.md")),
             *(ROOT / "docs").glob("10-*.md"),
             ROOT / "config/policy-v1.json", ROOT / "config/policy-v2.json",
             ROOT / "config/content-sources.json", ROOT / "config/generation-profile.example.json",
             ROOT / "config/implementation-plan-v1.json",
             *sorted((ROOT / "fixtures").glob("*.json")),
             ROOT / "go.mod", ROOT / "go.sum", ROOT / "worker/pyproject.toml", ROOT / "worker/uv.lock",
             ROOT / "e2e/pyproject.toml", ROOT / "e2e/uv.lock"]
    files = []
    for path in sorted(paths):
        raw = path.read_bytes()
        files.append({"path": path.relative_to(ROOT).as_posix(), "bytes": len(raw), "sha256": hashlib.sha256(raw).hexdigest()})
    policy = json.loads((ROOT / "config/policy-v2.json").read_text())
    return {"format_version": 1, "design_version": policy["design_version"], "frozen_on": "2026-09-08",
            "active_policy": "config/policy-v2.json", "implementation_plan": "config/implementation-plan-v1.json",
            "scope": "specification, fixtures and dependency locks; application implementation not complete",
            "files": files}


def main():
    parser = argparse.ArgumentParser()
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument("--write", action="store_true")
    group.add_argument("--check", action="store_true")
    args = parser.parse_args()
    current = snapshot()
    if args.write:
        temporary = MANIFEST.with_suffix(".json.tmp")
        temporary.write_text(json.dumps(current, indent=2) + "\n")
        temporary.replace(MANIFEST)
    elif not MANIFEST.is_file() or json.loads(MANIFEST.read_text()) != current:
        raise SystemExit("Frozen design differs. Review changes/version rules before explicitly resealing with --write.")
    print(json.dumps({"status": "ok", "operation": "seal" if args.write else "verify", "design_version": current["design_version"], "files": len(current["files"])}))


if __name__ == "__main__":
    main()
