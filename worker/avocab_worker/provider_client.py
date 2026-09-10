"""HTTP client for external model providers implementing chat-completions-json-v1."""
import hashlib
import json
import re
from typing import Any
import httpx


class ProviderError(Exception):
    pass


class ProviderNetworkError(ProviderError):
    pass


class ProviderResponseError(ProviderError):
    def __init__(self, status_code: int, message: str, body: str = ""):
        super().__init__(f"[{status_code}] {message}: {body}")
        self.status_code = status_code
        self.message = message
        self.body = body


class ProviderCallResult:
    def __init__(
        self,
        content: dict[str, Any],
        provider_request_id: str | None,
        request_sha256: str,
        response_sha256: str,
        usage: dict[str, int] | None
    ):
        self.content = content
        self.provider_request_id = provider_request_id
        self.request_sha256 = request_sha256
        self.response_sha256 = response_sha256
        self.usage = usage


class ProviderClient:
    def __init__(self, base_url: str, api_key: str, default_model: str = "test-model"):
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key
        self.default_model = default_model
        # STRICT REQUIREMENT: No implicit retries or auto redirects by client
        transport = httpx.HTTPTransport(retries=0)
        self._client = httpx.Client(
            base_url=self.base_url,
            transport=transport,
            timeout=45.0,
            headers={
                "Authorization": f"Bearer {api_key}",
                "Content-Type": "application/json; charset=utf-8"
            }
        )

    def close(self):
        self._client.close()

    def _parse_json_robust(self, raw_content: str) -> dict[str, Any]:
        s = raw_content.strip()
        if s.startswith("```"):
            lines = s.splitlines()
            if lines[0].startswith("```"):
                lines = lines[1:]
            if lines and lines[-1].startswith("```"):
                lines = lines[:-1]
            s = "\n".join(lines).strip()

        # Attempt 1: strict=False to allow unescaped newlines/control chars
        try:
            res = json.loads(s, strict=False)
            if isinstance(res, dict):
                return res
        except Exception:
            pass

        # Attempt 2: find outermost JSON block
        m = re.search(r"\{.*\}", s, re.DOTALL)
        if m:
            try:
                res = json.loads(m.group(0), strict=False)
                if isinstance(res, dict):
                    return res
            except Exception:
                pass

        # Attempt 3: regex field extraction for evaluation response schema
        try:
            result: dict[str, Any] = {}
            ratings_m = re.search(r'"ratings"\s*:\s*\{([^}]+)\}', s)
            if ratings_m:
                ratings = {}
                for item in re.finditer(r'"([a-zA-Z0-9_]+)"\s*:\s*"([a-zA-Z0-9_]+)"', ratings_m.group(1)):
                    ratings[item.group(1)] = item.group(2).lower()
                result["ratings"] = ratings

            rat_m = re.search(r'"rationale"\s*:\s*"?(.*?)"?\s*(?:,\s*"[a-zA-Z0-9_]+"|\}$|$)', s, re.DOTALL)
            if rat_m:
                result["rationale"] = rat_m.group(1).strip().strip('"')

            mod_m = re.search(r'"model"\s*:\s*"([^"]+)"', s)
            if mod_m:
                result["model"] = mod_m.group(1)

            result.setdefault("ratings", {})
            result.setdefault("rationale", "")
            if result["ratings"]:
                return result
        except Exception:
            pass

        # Attempt 4: auto-close truncated JSON
        try:
            fixed_s = s.rstrip()
            unescaped_quotes = len(re.findall(r'(?<!\\)"', fixed_s))
            if unescaped_quotes % 2 != 0:
                fixed_s += '"'
            open_braces = fixed_s.count("{") - fixed_s.count("}")
            if open_braces > 0:
                fixed_s += "}" * open_braces
            parsed = json.loads(fixed_s, strict=False)
            if isinstance(parsed, dict) and parsed:
                return parsed
        except Exception:
            pass

        parsed = json.loads(s)
        if isinstance(parsed, dict):
            return parsed
        raise ProviderError(f"Provider returned non-dict JSON content: {type(parsed).__name__}")

    def call_chat_completion(
        self,
        messages: list[dict[str, str]],
        model: str | None = None,
        max_tokens: int = 1024,
        temperature: float = 0.2
    ) -> ProviderCallResult:
        chosen_model = model or self.default_model
        payload = {
            "model": chosen_model,
            "messages": messages,
            "temperature": temperature,
            "max_tokens": max_tokens,
            "response_format": {"type": "json_object"}
        }
        body_bytes = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        req_sha = hashlib.sha256(body_bytes).hexdigest()

        # Handle both /v1/chat/completions and /chat/completions paths
        path = "/v1/chat/completions" if not self.base_url.endswith("/v1") else "/chat/completions"

        try:
            resp = self._client.post(path, content=body_bytes)
        except (httpx.ConnectError, httpx.TimeoutException, httpx.NetworkError) as exc:
            raise ProviderNetworkError(f"Network failure calling provider at {self.base_url}: {exc}") from exc

        resp_sha = hashlib.sha256(resp.content).hexdigest()

        if resp.status_code != 200:
            raise ProviderResponseError(resp.status_code, "Provider call returned non-200", resp.text)

        try:
            data = resp.json()
            provider_req_id = data.get("id")
            choices = data.get("choices", [])
            if not choices:
                raise ValueError("No choices in provider response")
            raw_content = choices[0].get("message", {}).get("content", "{}")
            parsed_content = self._parse_json_robust(raw_content)

            usage_raw = data.get("usage")
            usage_dict = None
            if usage_raw and isinstance(usage_raw, dict):
                in_tok = usage_raw.get("prompt_tokens", 0)
                out_tok = usage_raw.get("completion_tokens", 0)
                usage_dict = {"input_tokens": in_tok, "output_tokens": out_tok}

            return ProviderCallResult(
                content=parsed_content,
                provider_request_id=provider_req_id,
                request_sha256=req_sha,
                response_sha256=resp_sha,
                usage=usage_dict
            )
        except Exception as exc:
            if isinstance(exc, ProviderError):
                raise
            raise ProviderError(f"Failed to parse provider response: {exc}") from exc
