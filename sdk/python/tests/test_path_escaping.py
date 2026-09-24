import unittest

from microvm.client import MicroVM


class PathCapturingMicroVM(MicroVM):
    def __init__(self) -> None:
        super().__init__(api_url="https://sandbox.example.com", pat_token="pat-token")
        self.paths = []

    def _do_json(self, method, path, payload):  # type: ignore[override]
        self.paths.append(path)
        return {}

    def _request(self, method, url, body=None, content_type=None, extra_headers=None):  # type: ignore[override]
        self.paths.append(url)
        return b"{}"


class ResourcePathEscapingTests(unittest.TestCase):
    """Dynamic IDs must be percent-escaped into a single path segment.

    A hostile id like "x/../admin" concatenated raw traverses out of its
    route; escaped it stays one literal segment ("x%2F..%2Fadmin").
    """

    HOSTILE = "x/../admin"
    ESCAPED = "x%2F..%2Fadmin"

    def test_resource_ids_are_percent_escaped(self):
        client = PathCapturingMicroVM()

        client.destroy(self.HOSTILE)
        client.delete_template(self.HOSTILE)
        client.delete_wasm_module(self.HOSTILE)
        client.get_session(self.HOSTILE, self.HOSTILE)
        client.delete_session(self.HOSTILE, self.HOSTILE)
        client.signal_session(self.HOSTILE, self.HOSTILE, "INT")
        client.unexpose_port(self.HOSTILE, 8080)
        client.session_log(self.HOSTILE, self.HOSTILE)

        want = [
            "/v1/sandboxes/" + self.ESCAPED,
            "/v1/templates/" + self.ESCAPED,
            "/v1/wasm-modules/" + self.ESCAPED,
            "/v1/sandboxes/" + self.ESCAPED + "/sessions/" + self.ESCAPED,
            "/v1/sandboxes/" + self.ESCAPED + "/sessions/" + self.ESCAPED,
            "/v1/sandboxes/" + self.ESCAPED + "/sessions/" + self.ESCAPED + "/signal",
            "/v1/sandboxes/" + self.ESCAPED + "/ports/8080",
            "https://sandbox.example.com"
            "/v1/sandboxes/" + self.ESCAPED + "/sessions/" + self.ESCAPED + "/log",
        ]
        self.assertEqual(client.paths, want)


if __name__ == "__main__":
    unittest.main()
