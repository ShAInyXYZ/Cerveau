/* Cerveau PWA service worker — network-first passthrough.
   Exists to satisfy installability; the app is a live mirror of the
   machine and must never serve stale UI or cache API traffic. */
self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', (e) => e.waitUntil(self.clients.claim()));
self.addEventListener('fetch', (e) => {
  if (e.request.method !== 'GET') return;
  e.respondWith(fetch(e.request).catch(() => new Response('offline', { status: 503 })));
});
