"""Static server for the explorations that never caches (ES modules otherwise stick).

Usage, from the repo root: python3 explorations/serve.py [port]
Then open http://localhost:4319/explorations/
"""

import http.server
import sys


class NoCache(http.server.SimpleHTTPRequestHandler):
    def end_headers(self):
        self.send_header("Cache-Control", "no-store")
        super().end_headers()


port = int(sys.argv[1]) if len(sys.argv) > 1 else 4319
http.server.ThreadingHTTPServer(("127.0.0.1", port), NoCache).serve_forever()
