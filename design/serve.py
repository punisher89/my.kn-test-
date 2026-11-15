#!/usr/bin/env python3
"""Local, read-only UI mockup. No packet engine, router access or real probes."""

import json
from datetime import datetime, timezone
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

HERE = Path(__file__).resolve().parent
WEB = HERE.parent / "internal" / "pilot" / "web"
DEMO_TOKEN = "mockup-only-no-router-connection-00000000"


class Handler(SimpleHTTPRequestHandler):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, directory=str(WEB), **kwargs)

    def send(self, body, kind="application/json", status=200):
        if not isinstance(body, bytes):
            body = json.dumps(body, ensure_ascii=False).encode()
        self.send_response(status)
        self.send_header("Content-Type", kind + "; charset=utf-8")
        self.send_header("Cache-Control", "no-store")
        self.send_header("X-Content-Type-Options", "nosniff")
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path == "/auth/status":
            return self.send({"configured": True, "authenticated": True, "pairing_available": False})
        if self.path in ("/", "/index.html"):
            html = (WEB / "index.html").read_text().replace(
                '<script src="/app.js" defer></script>',
                '<script src="/demo.js"></script><script src="/app.js" defer></script>',
            )
            return self.send(html.encode(), "text/html")
        if self.path == "/demo.js":
            return self.send(
                f"sessionStorage.setItem('pilot-token', '{DEMO_TOKEN}');".encode(),
                "application/javascript",
            )
        if self.path == "/api/diagnostics":
            adaptive = json.loads((HERE / "demo-adaptive.json").read_text())
            state = json.loads((HERE / "demo-state.json").read_text())
            return self.send({
                "schema_version": 1, "sample": True,
                "generated_at": datetime.now(timezone.utc).isoformat(),
                "version": state["version"], "service": {"mode": "mockup"},
                "current": adaptive["hosts"], "history": adaptive["history"],
                "discovered": adaptive["discovered"],
                "notes": ["МАКЕТ: пример отчёта, без подключения к роутеру."],
            })
        if self.path == "/api/adaptive":
            return self.send((HERE / "demo-adaptive.json").read_bytes())
        if self.path == "/api/status":
            return self.send((HERE / "demo-state.json").read_bytes())
        if self.path == "/api/logs":
            return self.send([
                "МАКЕТ: это примеры событий, не результаты работы роутера.",
                "[пример] Контроллер запущен; WAN=ppp0",
                "[пример] YouTube: проверка прямого соединения не пройдена",
                "[пример] YouTube: TLS 1.2 и TLS 1.3 — контрольные запросы успешны",
                "[пример] Стратегия split-sni подтверждена после перезапуска",
            ])
        if self.path.startswith("/api/"):
            return self.send({"error": "Это демонстрационный макет."}, status=404)
        return super().do_GET()

    def do_POST(self):
        self.send({"error": "Это макет. Действия с роутером здесь не выполняются."}, status=409)

    do_PUT = do_POST


if __name__ == "__main__":
    print("UI mockup on port 9091; demonstration data only", flush=True)
    ThreadingHTTPServer(("127.0.0.1", 9091), Handler).serve_forever()
