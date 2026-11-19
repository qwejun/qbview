# -*- coding: utf-8 -*-
"""CDP 截图回归：桌面 / 手机 / 暗色 / 删除确认态 / 筛选标题。

坑（沿用上次结论）：
- --window-size 会被窗口装饰吃掉几十像素，必须用 Emulation.setDeviceMetricsOverride。
- 本机 Clash 会代理 127.0.0.1，这里显式清掉代理环境变量。
"""
import base64
import json
import os
import time
import urllib.request

import websocket

CDP = '127.0.0.1:9341'
APP = 'http://127.0.0.1:21012'
OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'shots')
PORT = 9341

for k in ('HTTP_PROXY', 'HTTPS_PROXY', 'http_proxy', 'https_proxy'):
    os.environ.pop(k, None)
os.environ['NO_PROXY'] = '127.0.0.1'

os.makedirs(OUT, exist_ok=True)


def new_tab():
    # 新版 Chromium 的 /json/new 只接受 PUT
    req = urllib.request.Request('http://%s/json/new?%s' % (CDP, APP), method='PUT')
    return json.load(urllib.request.urlopen(req, timeout=10))['webSocketDebuggerUrl']


class Tab:
    def __init__(self):
        self.ws = websocket.create_connection(new_tab(), timeout=20)
        self.id = 0

    def send(self, method, **params):
        self.id += 1
        self.ws.send(json.dumps({'id': self.id, 'method': method, 'params': params}))
        while True:
            m = json.loads(self.ws.recv())
            if m.get('id') == self.id:
                return m

    def eval(self, expr):
        r = self.send('Runtime.evaluate', expression=expr, returnByValue=True)
        return r.get('result', {}).get('result', {}).get('value')

    def viewport(self, w, h):
        self.send('Emulation.setDeviceMetricsOverride',
                  width=w, height=h, deviceScaleFactor=1, mobile=False)

    def open(self):
        self.send('Page.navigate', url=APP)
        time.sleep(3)  # 首次 fetchState + 渲染

    def shot(self, name):
        r = self.send('Page.captureScreenshot', format='png')
        path = os.path.join(OUT, name + '.png')
        with open(path, 'wb') as f:
            f.write(base64.b64decode(r['result']['data']))
        print('saved', name)

    def close(self):
        self.ws.close()


t = Tab()
t.send('Page.enable')
t.send('Runtime.enable')

# 1. 桌面
t.viewport(1440, 900)
t.open()
t.shot('desktop')

# 2. 手机 亮色
t.viewport(390, 844)
time.sleep(1)
t.shot('mobile')

# 3. 手机 暗色
t.eval("localStorage.setItem('qbview-theme','dark')")
t.open()
time.sleep(1)
t.shot('mobile-dark')

# 4. 恢复亮色，删除确认态（原地展开三选项）
t.eval("localStorage.setItem('qbview-theme','light')")
t.open()
t.eval("document.querySelector('[data-act=\"delete\"]').click()")
time.sleep(0.6)
t.shot('mobile-confirm')

# 5. 取消确认 + 切"下载中"筛选，看顶栏标题联动
t.eval("document.querySelector('[data-act=\"cancel-del\"]').click()")
t.eval("document.querySelector('[data-filter=\"down\"]').click()")
time.sleep(0.6)
t.shot('mobile-filter-down')
print('title =', t.eval("document.getElementById('pageTitle').textContent"))

# 6. 桌面暗色 + 筛"异常"（上一轮漏了 reload，主题没生效）
t.viewport(1440, 900)
t.eval("localStorage.setItem('qbview-theme','dark')")
t.open()
t.eval("document.querySelector('[data-filter=\"error\"]').click()")
time.sleep(1)
t.shot('desktop-dark-error')

t.close()
print('done ->', OUT)
