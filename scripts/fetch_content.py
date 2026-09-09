"""Fetch the frozen upstream candidate snapshot; does not publish words or senses."""
import csv
import hashlib
import json
from pathlib import Path
import tempfile
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


def download(url, target, expected=None):
    if expected and target.is_file():
        with target.open("rb") as existing:
            value = hashlib.file_digest(existing, "sha256").hexdigest()
        if value != expected:
            raise ValueError("Local candidate snapshot differs from its frozen checksum: " + str(target))
        return {"sha256": value, "bytes": target.stat().st_size}
    digest = hashlib.sha256()
    size = 0
    with tempfile.NamedTemporaryFile(dir=target.parent, delete=False) as output:
        temporary = Path(output.name)
        try:
            with urllib.request.urlopen(url, timeout=60) as response:
                while chunk := response.read(1024 * 1024):
                    size += len(chunk)
                    if size > 512 * 1024 * 1024:
                        raise ValueError("Upstream file exceeds 512 MiB limit")
                    output.write(chunk)
                    digest.update(chunk)
            output.close()
            value = digest.hexdigest()
            if expected and value != expected:
                raise ValueError("Upstream checksum differs from frozen snapshot")
            temporary.replace(target)
            return {"sha256": value, "bytes": size}
        finally:
            temporary.unlink(missing_ok=True)


def main():
    sources = json.loads((ROOT / "config/content-sources.json").read_text())
    source = next(s for s in sources["sources"] if s["id"] == "ecdict-candidate-v1")
    revision = source["revision"]
    target = ROOT / "data/sources/ecdict" / revision
    target.mkdir(parents=True, exist_ok=True)
    base = "https://raw.githubusercontent.com/skywind3000/ECDICT/" + revision + "/"
    print("Downloading frozen ECDICT candidate snapshot", flush=True)
    data_info = download(base + "ecdict.csv", target / "ecdict.csv", source["data_sha256"])
    license_info = download(base + "LICENSE", target / "LICENSE", source.get("license_sha256"))
    csv.field_size_limit(10 * 1024 * 1024)
    seen = set()
    cet6_words = set()
    rows = translations = duplicate_words = cet6_rows = 0
    with (target / "ecdict.csv").open(encoding="utf-8-sig", newline="") as input_file:
        reader = csv.DictReader(input_file)
        required = {"word", "translation", "tag"}
        if not required <= set(reader.fieldnames or []):
            raise ValueError("Required upstream columns missing")
        columns = reader.fieldnames
        for row in reader:
            rows += 1
            word = row["word"]
            duplicate_words += word in seen
            seen.add(word)
            translations += bool(row["translation"].strip())
            if "cet6" in row["tag"].split():
                cet6_rows += 1
                cet6_words.add(word)
    manifest = {"source_id": source["id"], "revision": revision, "data": data_info,
                "license": license_info, "columns": columns, "raw_row_count": rows,
                "raw_unique_word_count": len(seen), "raw_duplicate_word_rows": duplicate_words,
                "nonempty_translation_rows": translations, "cet6_row_count": cet6_rows,
                "cet6_unique_word_count": len(cet6_words), "status": "candidate_snapshot_only"}
    for field in ("raw_row_count", "raw_unique_word_count", "raw_duplicate_word_rows", "nonempty_translation_rows", "cet6_row_count", "cet6_unique_word_count"):
        if source.get(field) is not None and manifest[field] != source[field]:
            raise ValueError("Candidate snapshot statistics differ: " + field)
    (target / "manifest.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps(manifest, ensure_ascii=False))


if __name__ == "__main__":
    main()
