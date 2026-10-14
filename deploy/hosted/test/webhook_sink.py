# Test-only HTTP listener. POST anything: appended to the log file. GET /healthz: a fake degraded
# healthz whose backup check fails, to prove the monitor alerts on it.
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

port, logfile = int(sys.argv[1]), sys.argv[2]
BAD = (b'{"checks":{"backup":{"detail":"last backup failed: backup directory not available","ok":false},'
       b'"db":{"ok":true},"jobs":{"detail":"2 pending, 0 failed","ok":true},"ocr":{"detail":"ppocrv5","ok":true}},"status":"degraded"}\n')

class H(BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0))
        with open(logfile, "ab") as f:
            f.write(self.rfile.read(n) + b"\n")
        self.send_response(200); self.end_headers()
    def do_GET(self):
        self.send_response(503); self.send_header("Content-Type", "application/json"); self.end_headers()
        self.wfile.write(BAD)
    def log_message(self, *a): pass

HTTPServer(("0.0.0.0", port), H).serve_forever()
