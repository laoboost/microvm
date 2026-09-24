import json
import threading
import unittest
from http.server import BaseHTTPRequestHandler, HTTPServer

from microvm.client import MicroVM


class _SilentHandler(BaseHTTPRequestHandler):
    def log_message(self, format, *args):  # noqa: A002 - stdlib signature
        pass


def _start_server(handler_cls):
    server = HTTPServer(("127.0.0.1", 0), handler_cls)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    return server


def _target_handler(record):
    class TargetHandler(_SilentHandler):
        def do_GET(self):
            self._record()

        def do_POST(self):
            self._record()

        def _record(self):
            length = int(self.headers.get("Content-Length", "0"))
            body = self.rfile.read(length) if length else b""
            record.append(
                {
                    "path": self.path,
                    "headers": {key.lower(): value for key, value in self.headers.items()},
                    "body": body,
                }
            )
            payload = json.dumps({"status": "ok"}).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)

    return TargetHandler


def _redirect_handler(location):
    class RedirectHandler(_SilentHandler):
        def do_GET(self):
            self._redirect()

        def do_POST(self):
            self._redirect()

        def _redirect(self):
            self.send_response(302)
            self.send_header("Location", location)
            self.send_header("Content-Length", "0")
            self.end_headers()

    return RedirectHandler


class CrossOriginRedirectTests(unittest.TestCase):
    def test_cross_origin_redirect_strips_authorization(self):
        record = []
        target = _start_server(_target_handler(record))
        redirector = _start_server(
            _redirect_handler("http://%s:%d/health" % target.server_address)
        )
        try:
            client = MicroVM(
                api_url="http://%s:%d" % redirector.server_address,
                pat_token="pat-token",
            )
            client.health()
        finally:
            redirector.shutdown()
            target.shutdown()

        self.assertEqual(len(record), 1, "redirect should be followed")
        headers = record[0]["headers"]
        self.assertNotIn(
            "authorization",
            headers,
            "cross-origin redirect leaked Authorization: %r" % headers.get("authorization"),
        )

    def test_cross_origin_redirect_strips_registry_headers_on_push(self):
        record = []
        target = _start_server(_target_handler(record))
        redirector = _start_server(
            _redirect_handler("http://%s:%d/v1/wasm-modules/push" % target.server_address)
        )
        try:
            client = MicroVM(
                api_url="http://%s:%d" % redirector.server_address,
                pat_token="pat-token",
            )
            client.push_wasm_module(
                {
                    "name": "mod",
                    "tag": "latest",
                    "module": b"wasm-bytes",
                    "registryToken": "registry-token-secret",
                    "registryUsername": "registry-user",
                }
            )
        finally:
            redirector.shutdown()
            target.shutdown()

        self.assertEqual(len(record), 1, "redirect should be followed")
        headers = record[0]["headers"]
        self.assertNotIn(
            "authorization",
            headers,
            "cross-origin redirect leaked Authorization: %r" % headers.get("authorization"),
        )
        self.assertNotIn(
            "x-registry-token",
            headers,
            "cross-origin redirect leaked X-Registry-Token: %r" % headers.get("x-registry-token"),
        )
        self.assertNotIn(
            "x-registry-username",
            headers,
            "cross-origin redirect leaked X-Registry-Username: %r"
            % headers.get("x-registry-username"),
        )

    def test_same_origin_redirect_keeps_authorization(self):
        record = []

        class SameOriginHandler(_SilentHandler):
            def do_GET(self):
                if self.path == "/health":
                    self.send_response(302)
                    self.send_header("Location", "/health2")
                    self.send_header("Content-Length", "0")
                    self.end_headers()
                    return
                record.append(
                    {"headers": {key.lower(): value for key, value in self.headers.items()}}
                )
                payload = json.dumps({"status": "ok"}).encode("utf-8")
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(payload)))
                self.end_headers()
                self.wfile.write(payload)

        server = _start_server(SameOriginHandler)
        try:
            client = MicroVM(
                api_url="http://%s:%d" % server.server_address,
                pat_token="pat-token",
            )
            client.health()
        finally:
            server.shutdown()

        self.assertEqual(len(record), 1)
        headers = record[0]["headers"]
        self.assertEqual(headers.get("authorization"), "Bearer pat-token")


if __name__ == "__main__":
    unittest.main()
