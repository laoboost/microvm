import unittest

from microvm import client as client_module
from microvm.client import MicroVM


class RequestCapturingMicroVM(MicroVM):
    def __init__(self) -> None:
        super().__init__(api_url="https://sandbox.example.com", pat_token="pat-token")
        self.requests = []

    def _request(self, method, url, body=None, content_type=None, extra_headers=None):  # type: ignore[override]
        self.requests.append((method, url, body, content_type, extra_headers))
        return b""


class MultipartInjectionTests(unittest.TestCase):
    """Hand-rolled multipart must reject CR/LF and quotes in names/filenames.

    targetPath is written raw into the part body and its basename is spliced
    into `filename="..."`; CRLF or a quote there injects parts/headers.
    """

    def test_upload_file_rejects_crlf_in_target_path(self):
        client = RequestCapturingMicroVM()
        hostile = "/workspace/ok.txt\r\n--deadbeef\r\nContent-Disposition: form-data; name=\"pwn\""
        with self.assertRaises(ValueError):
            client.upload_file("sb-1", hostile, b"data")
        self.assertEqual(client.requests, [], "hostile upload must be rejected before any request")

    def test_upload_file_rejects_quote_in_filename(self):
        client = RequestCapturingMicroVM()
        hostile = '/workspace/evil".txt'
        with self.assertRaises(ValueError):
            client.upload_file("sb-1", hostile, b"data")
        self.assertEqual(client.requests, [], "hostile upload must be rejected before any request")

    def test_upload_file_accepts_normal_path(self):
        client = RequestCapturingMicroVM()
        client.upload_file("sb-1", "/workspace/file.txt", b"data")
        self.assertEqual(len(client.requests), 1)


class HeaderSafeTokenTests(unittest.TestCase):
    class RecordingModule:
        def __init__(self):
            self.calls = []

        def create_connection(self, *args, **kwargs):
            self.calls.append((args, kwargs))
            return object()

    def test_websocket_connect_rejects_header_injection_in_token(self):
        module = HeaderSafeTokenTests.RecordingModule()
        with self.assertRaises(ValueError):
            client_module._connect_websocket(
                module,
                "ws://sandbox.example.com/x",
                "tok\r\nX-Evil: 1",
                "exec stream",
            )
        self.assertEqual(module.calls, [], "hostile token must be rejected before connect")

    def test_websocket_connect_rejects_newline_in_token(self):
        module = HeaderSafeTokenTests.RecordingModule()
        with self.assertRaises(ValueError):
            client_module._connect_websocket(
                module,
                "ws://sandbox.example.com/x",
                "tok\nX-Evil: 1",
                "exec stream",
            )
        self.assertEqual(module.calls, [], "hostile token must be rejected before connect")


if __name__ == "__main__":
    unittest.main()
