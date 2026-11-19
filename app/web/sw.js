/* 策略：网络优先，失败才回落缓存。
   NAS 内网常年可达，缓存的唯一价值是断网时还能打开看上一眼；
   用 cache-first 会导致改完前端、用户刷新仍看到旧界面，这个代价比离线收益大。 */

const CACHE = 'qbview-v8';
const SHELL = ['./', './index.html', './style.css', './app.js', './manifest.webmanifest',
  './icon.png', './icon-maskable.png'];

self.addEventListener('install', (e) => {
  e.waitUntil(
    caches.open(CACHE).then((c) => c.addAll(SHELL)).then(() => self.skipWaiting())
  );
});

self.addEventListener('activate', (e) => {
  e.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim())
  );
});

self.addEventListener('fetch', (e) => {
  const url = new URL(e.request.url);

  // 只接管同源 GET；API 请求完全不碰，避免把任务列表缓存成静止画面
  if (e.request.method !== 'GET' || url.origin !== self.location.origin) return;
  if (url.pathname.startsWith('/api/')) return;

  e.respondWith(
    fetch(e.request).then((res) => {
      if (res && res.status === 200 && res.type === 'basic') {
        const copy = res.clone();
        caches.open(CACHE).then((c) => c.put(e.request, copy));
      }
      return res;
    }).catch(() => caches.match(e.request).then((hit) => {
      if (hit) return hit;
      // 深层路由在离线时也要能落到外壳上
      if (e.request.mode === 'navigate') return caches.match('./index.html');
      return Response.error();
    }))
  );
});
