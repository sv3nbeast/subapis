import http.client
import json
import threading
import unittest
from http.server import HTTPServer
from unittest.mock import patch

from server import Handler


class ProtocolTests(unittest.TestCase):
    def setUp(self):
        self.server = HTTPServer(("127.0.0.1", 0), Handler)
        self.server.token = "local-renderer-test-token-32-characters"
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)

    def request(self, method, path, body=None, headers=None):
        conn = http.client.HTTPConnection(*self.server.server_address, timeout=3)
        try:
            conn.request(method, path, body, headers or {})
            response = conn.getresponse()
            return response.status, json.loads(response.read())
        finally:
            conn.close()

    def test_authentication_precedes_rendering(self):
        with patch("server.render") as render:
            for authorization in ("", "Bearer wrong", "Bearer \u00ff"):
                status, _ = self.request("POST", "/render", "{}", {"Authorization": authorization})
                self.assertEqual(status, 401)
            render.assert_not_called()

    def test_bounded_body_and_data_only_input(self):
        headers = {"Authorization": "Bearer " + self.server.token}
        status, _ = self.request("POST", "/render", "{}", {**headers, "Content-Length": str(2 << 20)})
        self.assertEqual(status, 413)
        status, _ = self.request("POST", "/render", '{"kind":"shell","code":"do not execute"}', headers)
        self.assertEqual(status, 422)
        status, value = self.request("GET", "/health")
        self.assertEqual(status, 200)
        self.assertEqual(value["protocol_version"], 1)

    def test_result_contract_and_sanitized_runtime_failure(self):
        headers = {"Authorization": "Bearer " + self.server.token}
        result = {"extension": "docx", "mime": "test", "file": b"synthetic", "preview": b"%PDF-test"}
        with patch("server.render", return_value=result):
            status, value = self.request("POST", "/render", "{}", headers)
        self.assertEqual(status, 200)
        self.assertEqual(value["size_bytes"], len(result["file"]))
        self.assertEqual(len(value["file_sha256"]), 64)
        self.assertIn("preview_pdf_base64", value)
        with patch("server.render", side_effect=RuntimeError("private-path-and-content")):
            status, value = self.request("POST", "/render", "{}", headers)
        self.assertEqual(status, 500)
        self.assertNotIn("private-path", json.dumps(value))


if __name__ == "__main__":
    unittest.main()
