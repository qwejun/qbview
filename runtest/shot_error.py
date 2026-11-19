# -*- coding: utf-8 -*-
"""CDP 截图：1.0.3 的假端口被拒提示 + 自动探测结果。"""
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
t.send('Emulation.setDeviceMetricsOverride', width=390, height=844,
       deviceScaleFactor=1, mobile=True)
t.send('Page.navigate', url=APP)
time.sleep(3)

# 打开设置面板，强制填假端口 8080 并提交
t.eval("document.getElementById('btnSettings').click()")
time.sleep(1.5)
t.eval("""
(()=>{
  const p=document.getElementById('inPort');
  p.value='8080'; p.dispatchEvent(new Event('input',{bubbles:true}));
  document.getElementById('inUser').value='SjViv';
  const pw=document.getElementById('inPass'); pw.value='wrongpass';
  pw.dispatchEvent(new Event('input',{bubbles:true}));
})()
""")
t.eval("document.getElementById('btnSave').click()")
time.sleep(2.5)
print('error =', t.eval("document.getElementById('setupErr').textContent"))
print('portHint =', t.eval("document.getElementById('portHint').textContent"))
t.shot('settings-error')

# 点自动探测，应填回真 qB 端口
t.eval("document.getElementById('btnProbe').click()")
time.sleep(11)
print('after probe inPort =', t.eval("document.getElementById('inPort').value"))
print('after probe hint =', t.eval("document.getElementById('portHint').textContent"))
t.shot('settings-probed')
t.ws.close()
print('OK')
