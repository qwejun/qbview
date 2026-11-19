# -*- coding: utf-8 -*-
"""假的 qB 入口，复现用户截图：8080 上的网关/反代。

GET  /                       -> 200，页面含 qBittorrent WebUI（反代过来的登录页）
GET  /api/v2/app/version     -> 403（网关对未登录 API 的通用拒绝）
POST /api/v2/auth/login      -> 200 但响应体为空、也没有 SID cookie

旧版 isQBAt 只看前两道就会把它当成 qB；1.0.3 的第三道必须把它剔除。
"""
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse


class H(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def _send(self, code, body=b'', ctype='text/plain'):
        self.send_response(code)
        self.send_header('Content-Type', ctype)
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        if body:
            self.wfile.write(body)

    def do_GET(self):
        p = urlparse(self.path).path
        if p == '/':
            self._send(200, b'<!DOCTYPE html><html><head>'
                            b'<meta name="description" content="qBittorrent WebUI">'
                            b'</head><body>gateway login</body></html>', 'text/html')
            return
        # 网关对任何未登录的 /api/v2/ 请求一律 403
        self._send(403, b'Forbidden')

    def do_POST(self):
        n = int(self.headers.get('Content-Length') or 0)
        self.rfile.read(n)
        if urlparse(self.path).path == '/api/v2/auth/login':
            # 关键：200 + 空 body + 无 SID，正是截图里「登录未返回 SID:」的来源
            self._send(200, b'')
            return
        self._send(403, b'Forbidden')


if __name__ == '__main__':
    ThreadingHTTPServer(('127.0.0.1', 8080), H).serve_forever()
