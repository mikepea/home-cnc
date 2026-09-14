// Minimal service worker: required for installability. It uses a
// network-first strategy so command actions always hit the live server, and
// only falls back to a cached shell when offline.
const CACHE = 'home-cnc-v1';
const SHELL = ['/static/app.css', '/static/app.js'];

self.addEventListener('install', (e) => {
  e.waitUntil(caches.open(CACHE).then((c) => c.addAll(SHELL)));
  self.skipWaiting();
});

self.addEventListener('activate', (e) => {
  e.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k)))
    )
  );
  self.clients.claim();
});

self.addEventListener('fetch', (e) => {
  const req = e.request;
  // Never cache anything but same-origin GETs of static shell assets.
  if (req.method !== 'GET' || !SHELL.some((p) => req.url.endsWith(p))) return;
  e.respondWith(
    fetch(req)
      .then((res) => {
        const copy = res.clone();
        caches.open(CACHE).then((c) => c.put(req, copy));
        return res;
      })
      .catch(() => caches.match(req))
  );
});
