"""Real local HTTP/Chromium toolchain probe. This is not product E2E."""
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
from threading import Thread

from playwright.sync_api import sync_playwright, expect


class ProbeHandler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_GET(self):
        body = b'''<!doctype html><html lang="en"><title>E2E runtime probe</title>
<button id="check">Check local HTTP</button><p id="result">pending</p>
<script>document.querySelector('#check').onclick=async()=>{
const r=await fetch('/probe',{method:'POST'});
document.querySelector('#result').textContent=(await r.json()).status;
};</script></html>'''
        self.send_response(200)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        body = b'{"status":"local HTTP verified"}'
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def main():
    output = Path(__file__).resolve().parents[1] / "artifacts/browser-check"
    output.mkdir(parents=True, exist_ok=True)
    server = ThreadingHTTPServer(("127.0.0.1", 0), ProbeHandler)
    thread = Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        with sync_playwright() as runtime:
            browser = runtime.chromium.launch()
            context = browser.new_context()
            context.tracing.start(screenshots=True, snapshots=True, sources=True)
            page = context.new_page()
            page.goto(f"http://127.0.0.1:{server.server_port}")
            page.get_by_role("button", name="Check local HTTP").click()
            expect(page.locator("#result")).to_have_text("local HTTP verified")
            page.screenshot(path=str(output / "browser.png"))
            context.tracing.stop(path=str(output / "trace.zip"))
            result = {"status": "ok", "browser": browser.version,
                      "scope": "real browser/local HTTP toolchain only; application E2E not implemented",
                      "artifacts": str(output)}
            context.close()
            browser.close()
            (output / "result.json").write_text(json.dumps(result, indent=2) + "\n")
            print(json.dumps(result))
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)


if __name__ == "__main__":
    main()
