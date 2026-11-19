# -*- coding: utf-8 -*-
"""CDP 截图：端口探测版的设置面板（桌面 + 手机）。复用 shot.py 的坑位结论。"""
import base64
import json
import os
import time
import urllib.request

import websocket

CDP = '127.0.0.1:9341'
APP = 'http://127.0.0.1:21012'
OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'shots')

for k in ('HTTP_PROXY', 'HTTPS_PROXY', 'http_proxy', 'https_proxy'):
    os.environ.pop(k, None)
os.environ['NO_PROXY'] = '127.0.0.1'
os.makedirs(OUT, exist_ok=True)


def new_tab():
    req = urllib.request.Request('http://%s/json/new?%s' % (CDP, APP), method='PUT')
    return json.load(urllib.request.urlopen(req, timeout=10))['webSocketDebuggerUrl']


class Tab:
    def __init__(self):
        self.ws = websocket.create_connection(new_tab(), timeout=30)
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

    def shot(self, name):
        r = self.send('Page.captureScreenshot', format='png')
        open(os.path.join(OUT, name + '.png'), 'wb').write(
            base64.b64decode(r['result']['data']))
        print('shot:', name)


t = Tab()
t.send('Page.enable')
t.eval("localStorage.setItem('qbview-theme','light')")

# 桌面：打开设置面板，等探测结果出现在提示里
t.send('Emulation.setDeviceMetricsOverride', width=1440, height=900,
       deviceScaleFactor=1, mobile=False)
t.send('Page.navigate', url=APP)
time.sleep(3)
t.eval("document.getElementById('btnSettings').click()")
time.sleep(2)
t.shot('settings-desktop')

port_val = t.eval("document.getElementById('inPort').value")
hint = t.eval("document.getElementById('portHint').textContent")
print('inPort =', port_val, '| hint =', hint)

# 手机：设置面板
t.send('Emulation.setDeviceMetricsOverride', width=390, height=844,
       deviceScaleFactor=1, mobile=True)
time.sleep(1.2)
t.shot('settings-mobile')
t.ws.close()
print('OK')
