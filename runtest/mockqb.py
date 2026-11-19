# -*- coding: utf-8 -*-
"""qBittorrent Web API v2 最小 mock，供 qbview 本地回归。

端点：auth/login、torrents/info、transfer/info、sync/maindata、
torrents/pause|resume|delete。未带 SID 一律 403，和真实 qB 一致。
"""
import json
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

SID = 'mocksid-1234567890'
USER = 'admin'
PASS = 'mockpass'

T = [
    dict(hash='a' * 40, name='Ubuntu 24.04.2 LTS Desktop (amd64)',
         size=6178912256, progress=0.678, dlspeed=14 * 1024 * 1024,
         upspeed=512 * 1024, eta=3120, state='downloading',
         num_seeds=42, num_leechs=8, downloaded=4188888888,
         uploaded=123456789, ratio=0.03, category='iso',
         added_on=int(time.time()) - 3600, save_path='/volume1/video',
         amount_left=1989123456, num_complete=100, num_incomplete=20),
    dict(hash='b' * 40, name='沙丘.Dune.2021.2160p.BluRay.x265-HDH',
         size=88465680896, progress=0.214, dlspeed=0, upspeed=0,
         eta=8640000, state='stalledDL', num_seeds=0, num_leechs=0,
         downloaded=18973460992, uploaded=0, ratio=0.0, category='movie',
         added_on=int(time.time()) - 7200, save_path='/volume1/video',
         amount_left=69492219840, num_complete=0, num_incomplete=0),
    dict(hash='c' * 40, name='archlinux-2026.11.01-x86_64.iso',
         size=1389363200, progress=1.0, dlspeed=0, upspeed=640 * 1024,
         eta=0, state='uploading', num_seeds=0, num_leechs=6,
         downloaded=1389363200, uploaded=59481231808, ratio=42.8,
         category='iso', added_on=int(time.time()) - 86400,
         save_path='/volume1/iso', amount_left=0, num_complete=312,
         num_incomplete=0),
    dict(hash='d' * 40, name='The.Matrix.1999.1080p.BluRay.x264.DTS-HD',
         size=15801210880, progress=0.433, dlspeed=0, upspeed=0,
         eta=0, state='stoppedDL', num_seeds=0, num_leechs=0,
         downloaded=6841925504, uploaded=0, ratio=0.0, category='movie',
         added_on=int(time.time()) - 172800, save_path='/volume1/video',
         amount_left=8959285376, num_complete=0, num_incomplete=0),
    dict(hash='e' * 40, name='Some.Private.Tracker.Pack.2026.REPACK',
         size=52101359616, progress=0.052, dlspeed=0, upspeed=0,
         eta=0, state='error', num_seeds=0, num_leechs=0,
         downloaded=2709270297, uploaded=0, ratio=0.0, category='',
         added_on=int(time.time()) - 259200, save_path='/volume1/video',
         amount_left=49392089319, num_complete=0, num_incomplete=0),
    dict(hash='f' * 40, name='Fedora-Workstation-Live-x86_64-43.iso',
         size=2382667776, progress=0.881, dlspeed=0, upspeed=0,
         eta=180, state='checkingDL', num_seeds=0, num_leechs=0,
         downloaded=2099041792, uploaded=0, ratio=0.0, category='iso',
         added_on=int(time.time()) - 432000, save_path='/volume1/iso',
         amount_left=283625984, num_complete=0, num_incomplete=0),
]

TRANSFER = dict(
    dl_info_speed=15 * 1024 * 1024 + 340 * 1024,
    up_info_speed=640 * 1024 + 72 * 1024,
    dl_info_data=8.4 * 1024 ** 4,
    up_info_data=1.1 * 1024 ** 4,
)

MAINDATA = dict(rid=1, full_update=True,
                server_state=dict(free_space_on_disk=2638827906662))


def form(body):
    return {k: v[0] for k, v in parse_qs(body).items()}


class H(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def _send(self, code, body=b'', ctype='application/json', sid=False):
        self.send_response(code)
        self.send_header('Content-Type', ctype)
        if sid:
            self.send_header('Set-Cookie', 'SID=' + SID + '; path=/')
        self.end_headers()
        if body:
            self.wfile.write(body)

    def _authed(self):
        return SID in self.headers.get('Cookie', '')

    def do_POST(self):
        n = int(self.headers.get('Content-Length') or 0)
        body = self.rfile.read(n).decode('utf-8', 'replace')
        path = urlparse(self.path).path

        if path == '/api/v2/auth/login':
            f = form(body)
            if f.get('username') == USER and f.get('password') == PASS:
                self._send(200, b'Ok.', 'text/plain', sid=True)
            else:
                self._send(200, b'Fails.', 'text/plain')
            return

        if not self._authed():
            self._send(403, b'Forbidden', 'text/plain')
            return

        if path in ('/api/v2/torrents/pause', '/api/v2/torrents/resume'):
            hs = form(body).get('hashes', '')
            targets = hs.split('|') if hs != 'all' else [t['hash'] for t in T]
            for t in T:
                if t['hash'] in targets:
                    if path.endswith('pause'):
                        t['state'] = 'stoppedDL'
                        t['dlspeed'] = t['upspeed'] = 0
                    else:
                        t['state'] = 'downloading'
            self._send(200, b'Ok.', 'text/plain')
            return

        if path == '/api/v2/torrents/delete':
            hs = form(body).get('hashes', '')
            targets = hs.split('|') if hs != 'all' else [t['hash'] for t in T]
            T[:] = [t for t in T if t['hash'] not in targets]
            self._send(200, b'Ok.', 'text/plain')
            return

        self._send(404, b'Not Found', 'text/plain')

    def do_GET(self):
        path = urlparse(self.path).path
        # 登录页公开返回 200，与真实 qB 一致——端口探测的指纹依赖这一点
        if path == '/':
            body = (b'<!DOCTYPE html><html lang="zh"><head><meta charset="UTF-8">'
                    b'<title>qBittorrent WebUI</title>'
                    b'<meta name="description" content="qBittorrent WebUI">'
                    b'</head><body>login page</body></html>')
            self._send(200, body, 'text/html')
            return
        if not self._authed():
            self._send(403, b'Forbidden', 'text/plain')
            return
        if path == '/api/v2/torrents/info':
            self._send(200, json.dumps(T).encode())
        elif path == '/api/v2/transfer/info':
            self._send(200, json.dumps(TRANSFER).encode())
        elif path == '/api/v2/sync/maindata':
            self._send(200, json.dumps(MAINDATA).encode())
        elif path == '/api/v2/app/version':
            self._send(200, b'v5.1.0')
        else:
            self._send(404, b'Not Found', 'text/plain')


if __name__ == '__main__':
    ThreadingHTTPServer(('127.0.0.1', 18999), H).serve_forever()
