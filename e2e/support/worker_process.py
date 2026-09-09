"""Helper to spawn the real Avocab Python/LangGraph worker subprocess."""
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[2]
UV_BIN = ROOT / ".tools" / "uv" / "bin" / "uv"
WORKER_DIR = ROOT / "worker"


def run_worker(
    data_dir: Path,
    server_url: str,
    profile_path: Path,
    worker_id: str = "worker-1",
    max_items: int = 1,
    crash_after_gen: bool = False,
    timeout: float = 30.0
) -> subprocess.CompletedProcess:
    cmd = [
        str(UV_BIN), "run", "--directory", str(WORKER_DIR), "--locked",
        "python", "-m", "avocab_worker", "run",
        "--data-dir", str(data_dir),
        "--server", server_url,
        "--profile", str(profile_path),
        "--worker-id", worker_id,
        "--max-items", str(max_items)
    ]
    if crash_after_gen:
        cmd.append("--crash-after-gen")

    return subprocess.run(cmd, capture_output=True, text=True, timeout=timeout)
