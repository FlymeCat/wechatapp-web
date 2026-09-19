#!/usr/bin/env python3
"""Mock remove.bg server for local smoke tests.

Serves POST /v1.0/removebg and returns testdata/cutout.png (a transparent PNG),
mirroring what the real remove.bg API returns after background removal.
"""
import http.server
import socketserver

CUTOUT = open('testdata/cutout.png', 'rb').read()


class Handler(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get('Content-Length', 0))
        body = self.rfile.read(length)
        # Basic sanity: multipart body must contain the image_file field.
        if b'name="image_file"' not in body:
            self.send_response(400)
            self.end_headers()
            self.wfile.write(b'{"errors":[{"title":"image_file missing"}]}')
            return
        self.send_response(200)
        self.send_header('Content-Type', 'image/png')
        self.end_headers()
        self.wfile.write(CUTOUT)

    def log_message(self, fmt, *args):
        print(fmt % args)


if __name__ == '__main__':
    # Bind on 0.0.0.0 so the mock is reachable from Docker containers too.
    with socketserver.TCPServer(('0.0.0.0', 18098), Handler) as httpd:
        print('mock remove.bg listening on :18098')
        httpd.serve_forever()
