"""Worker configuration and profile validation."""
import json
import os
from pathlib import Path
from typing import Any
from urllib.parse import urlparse


class WorkerConfig:
    def __init__(
        self,
        data_dir: Path,
        server_url: str,
        worker_token: str,
        profile: dict[str, Any],
        worker_id: str = "worker-langgraph-1"
    ):
        self.data_dir = data_dir
        self.server_url = server_url.rstrip("/")
        self.worker_token = worker_token
        self.profile = profile
        self.worker_id = worker_id

        # Profile sections
        self.config_version = profile.get("config_version", "generation-v2")
        self.mode = profile.get("mode", "fixture")
        self.quality_policy_version = profile.get("quality_policy_version", "quality-v1")
        self.adapter = profile.get("production_adapter", "chat-completions-json-v1")

        self.generator_cfg = profile.get("generator", {})
        self.evaluator_cfg = profile.get("evaluator", {})

        # Resolve API keys
        gen_env = self.generator_cfg.get("api_key_env", "AVOCAB_GENERATOR_API_KEY")
        self.generator_api_key = os.environ.get(gen_env) or self.generator_cfg.get("api_key", "test-key")

        eval_env = self.evaluator_cfg.get("api_key_env", "AVOCAB_EVALUATOR_API_KEY")
        self.evaluator_api_key = os.environ.get(eval_env) or self.evaluator_cfg.get("api_key", "test-key")

    @classmethod
    def load(
        cls,
        data_dir: str | Path,
        server_url: str = "http://127.0.0.1:8080",
        profile_path: str | Path | None = None,
        worker_id: str = "worker-langgraph-1"
    ) -> "WorkerConfig":
        d_dir = Path(data_dir).resolve()
        token_file = d_dir / "worker.token"
        if not token_file.is_file():
            raise FileNotFoundError(f"Worker token file not found at {token_file}")
        worker_token = token_file.read_text(encoding="utf-8").strip()

        profile: dict[str, Any]
        if profile_path and Path(profile_path).is_file():
            profile = json.loads(Path(profile_path).read_text(encoding="utf-8"))
        else:
            profile = {
                "config_version": "generation-v2",
                "budget_policy_version": "budget-v2",
                "mode": "fixture",
                "quality_policy_version": "quality-v1",
                "production_adapter": "chat-completions-json-v1",
                "generator": {
                    "base_url": "http://127.0.0.1:9999",
                    "model": "test-generator",
                    "api_key": "test-key",
                    "max_output_tokens": 1024
                },
                "evaluator": {
                    "base_url": "http://127.0.0.1:9999",
                    "model": "test-evaluator",
                    "api_key": "test-key",
                    "max_output_tokens": 512
                }
            }

        validate_profile(profile)
        return cls(d_dir, server_url, worker_token, profile, worker_id)


def validate_profile(profile: dict[str, Any]):
    mode = profile.get("mode", "fixture")
    gen = profile.get("generator", {})
    ev = profile.get("evaluator", {})

    if mode == "production":
        for name, cfg in [("generator", gen), ("evaluator", ev)]:
            url = cfg.get("base_url")
            if not url or not url.startswith("https://"):
                raise ValueError(f"Production profile requires HTTPS base_url for {name}, got {url}")
            parsed = urlparse(url)
            host = parsed.hostname or ""
            if host in ("127.0.0.1", "localhost", "::1") or host.startswith("127."):
                raise ValueError(f"Production profile cannot use loopback address for {name}: {url}")
            if not cfg.get("model"):
                raise ValueError(f"Production profile requires explicit model identifier for {name}")
    elif mode in ("test", "fixture"):
        # Loopback permitted for test/fixture
        for name, cfg in [("generator", gen), ("evaluator", ev)]:
            url = cfg.get("base_url")
            if url:
                parsed = urlparse(url)
                host = parsed.hostname or ""
                # Test profile must only use loopback
                if host not in ("127.0.0.1", "localhost", "::1"):
                    raise ValueError(f"Test profile must use loopback address, got {url}")
