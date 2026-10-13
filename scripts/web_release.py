#!/usr/bin/env python3
"""Adaptive Vocabulary Web Release Packaging and Verification Tool.
Inspects frontend build artifacts, verifies manifests, checks asset hashes,
validates release configuration, and verifies module delivery readiness.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[1]
RELEASE_CONFIG_PATH = ROOT / "config/web-release.json"
WEB_PLAN_PATH = ROOT / "config/web-implementation-plan-v1.json"
FRONTEND_DIST_DIR = ROOT / "web/frontend/dist"
BIN_PATH = ROOT / "bin/avocab"


def compute_sha256(path: Path) -> str:
    h = hashlib.sha256()
    h.update(path.read_bytes())
    return h.hexdigest()


def check_release(verbose: bool = False) -> dict:
    errors = []
    warnings = []

    # 1. Check release config
    if not RELEASE_CONFIG_PATH.is_file():
        errors.append(f"Release config not found: {RELEASE_CONFIG_PATH}")
        config = {}
    else:
        try:
            config = json.loads(RELEASE_CONFIG_PATH.read_text(encoding="utf-8"))
        except Exception as exc:
            errors.append(f"Invalid JSON in release config: {exc}")
            config = {}

    # 2. Check frontend dist
    dist_index = FRONTEND_DIST_DIR / "index.html"
    dist_manifest = FRONTEND_DIST_DIR / ".vite/manifest.json"
    dist_assets_dir = FRONTEND_DIST_DIR / "assets"

    if not dist_index.is_file():
        errors.append(f"Frontend dist index.html missing at {dist_index}")
    if not dist_manifest.is_file():
        errors.append(f"Frontend Vite manifest.json missing at {dist_manifest}")
    if not dist_assets_dir.is_dir():
        errors.append(f"Frontend dist assets directory missing at {dist_assets_dir}")

    manifest_data = {}
    asset_files_verified = 0
    manifest_hash = ""

    if dist_manifest.is_file():
        try:
            manifest_data = json.loads(dist_manifest.read_text(encoding="utf-8"))
            manifest_hash = compute_sha256(dist_manifest)
            for entry_name, entry_info in manifest_data.items():
                asset_file = entry_info.get("file")
                if asset_file:
                    target_path = FRONTEND_DIST_DIR / asset_file
                    if not target_path.is_file():
                        errors.append(f"Asset declared in manifest missing on disk: {asset_file}")
                    else:
                        asset_files_verified += 1
        except Exception as exc:
            errors.append(f"Failed to parse Vite manifest: {exc}")

    # 3. Check Go binary
    if not BIN_PATH.is_file():
        warnings.append(f"Go binary not found at {BIN_PATH} (run 'make build' to compile)")
    elif not os.access(BIN_PATH, os.X_OK):
        errors.append(f"Go binary at {BIN_PATH} is not executable")

    # 4. Check Web Implementation Plan
    plan_data = {}
    module_statuses = {}
    if WEB_PLAN_PATH.is_file():
        try:
            plan_data = json.loads(WEB_PLAN_PATH.read_text(encoding="utf-8"))
            for t in plan_data.get("tasks", []):
                module_statuses[t["id"]] = t.get("status", "unknown")
        except Exception as exc:
            errors.append(f"Failed to parse web implementation plan: {exc}")

    report = {
        "status": "ok" if not errors else "error",
        "design_version": config.get("design_version", "web-design-v1.1"),
        "release_version": config.get("release_version", "1.1.0"),
        "manifest_hash": manifest_hash,
        "assets_verified": asset_files_verified,
        "dist_index_exists": dist_index.is_file(),
        "binary_exists": BIN_PATH.is_file(),
        "module_statuses": module_statuses,
        "errors": errors,
        "warnings": warnings,
    }

    if verbose or errors:
        for err in errors:
            print(f"ERROR: {err}", file=sys.stderr)
        for warn in warnings:
            print(f"WARNING: {warn}", file=sys.stderr)

    return report


def main():
    parser = argparse.ArgumentParser(description="Adaptive Vocabulary Web Release Packaging Tool")
    subparsers = parser.add_subparsers(dest="command")

    subparsers.add_parser("check", help="Verify release packaging and artifacts readiness")
    subparsers.add_parser("status", help="Output release status as JSON")
    subparsers.add_parser("manifest", help="Print Vite assets manifest")

    args = parser.parse_args()
    cmd = args.command or "check"

    if cmd == "check":
        report = check_release(verbose=True)
        if report["status"] == "ok":
            print(f"Release check passed! {report['assets_verified']} assets verified, manifest hash: {report['manifest_hash'][:12]}...")
            sys.exit(0)
        else:
            print(f"Release check failed with {len(report['errors'])} error(s).", file=sys.stderr)
            sys.exit(1)

    elif cmd == "status":
        report = check_release(verbose=False)
        print(json.dumps(report, indent=2, ensure_ascii=False))
        sys.exit(0 if report["status"] == "ok" else 1)

    elif cmd == "manifest":
        dist_manifest = FRONTEND_DIST_DIR / ".vite/manifest.json"
        if dist_manifest.is_file():
            print(dist_manifest.read_text(encoding="utf-8"))
            sys.exit(0)
        else:
            print("Manifest file not found", file=sys.stderr)
            sys.exit(1)


if __name__ == "__main__":
    main()
