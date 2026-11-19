import json, os, time, urllib.request
import websocket
CDP='127.0.0.1:9341'; APP='http://127.0.0.1:21012'
for k in ('HTTP_PROXY','HTTPS_PROXY','http_proxy','https_proxy'): os.environ.pop(k,None)
os.environ['NO_PROXY']='127.0.0.1'
req=urllib.request.Request('http://%s/json/new?%s'%(CDP,APP), method='PUT')
ws=websocket.create_connection(json.load(urllib.request.urlopen(req,timeout=10))['webSocketDebuggerUrl'],timeout=20)
mid=[0]
def send(m,**p):
    mid[0]+=1; ws.send(json.dumps({'id':mid[0],'method':m,'params':p}))
    while True:
        r=json.loads(ws.recv())
        if r.get('id')==mid[0]: return r
send('Page.enable'); send('Emulation.setDeviceMetricsOverride',width=390,height=844,deviceScaleFactor=1,mobile=False)
send('Page.navigate',url=APP); time.sleep(3)
q="""(()=>{const n=document.getElementById('filters');
const cs=getComputedStyle(n);
return {cls:n.className, sw:n.scrollWidth, cw:n.clientWidth, sl:n.scrollLeft,
        mask:cs.maskImage||cs.webkitMaskImage, ov:cs.overflowX};})()"""
print(json.dumps(send('Runtime.evaluate',expression=q,returnByValue=True)['result']['result']['value'],ensure_ascii=False))
# 滚动到底再查一次（渐隐应消失）
send('Runtime.evaluate',expression="document.getElementById('filters').scrollLeft=99999")
time.sleep(0.4)
r2=send('Runtime.evaluate',expression=q,returnByValue=True)['result']['result']['value']
print('after scroll:',json.dumps(r2,ensure_ascii=False))
ws.close()
