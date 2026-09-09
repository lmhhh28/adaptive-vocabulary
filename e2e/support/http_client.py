"""HTTP client helper for E2E tests, handling session cookies and CSRF tokens."""
import httpx


class APIClient:
    def __init__(self, base_url: str):
        self.base_url = base_url.rstrip("/")
        self.client = httpx.Client(base_url=self.base_url, timeout=10.0)
        self.csrf_token: str | None = None

    def initialize_client_context(self) -> str:
        """Call /client-context to obtain session cookie and CSRF token."""
        resp = self.client.get("/client-context")
        resp.raise_for_status()
        data = resp.json()
        self.csrf_token = data.get("csrf_token")
        return self.csrf_token

    def get(self, path: str, **kwargs) -> httpx.Response:
        return self.client.get(path, **kwargs)

    def post(self, path: str, json: dict | None = None, **kwargs) -> httpx.Response:
        headers = kwargs.pop("headers", {})
        if self.csrf_token and "X-CSRF-Token" not in headers:
            headers["X-CSRF-Token"] = self.csrf_token
        return self.client.post(path, json=json, headers=headers, **kwargs)

    def close(self):
        self.client.close()
