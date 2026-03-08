// Governing: SPEC-0029 REQ "Service Worker for Offline Shell"
// Service worker for Claude Ops PWA — cache-first static, network-first API.

const CACHE_VERSION = 'claudeops-v1';
const STATIC_ASSETS = [
  '/',
  '/static/style.css',
  '/static/logo.svg',
  '/static/manifest.json',
  '/static/icon-192.svg',
  '/static/icon-512.svg',
  '/favicon.ico',
];

// Install: pre-cache the app shell.
self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(CACHE_VERSION).then((cache) => cache.addAll(STATIC_ASSETS))
  );
  self.skipWaiting();
});

// Activate: purge old cache versions.
self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(
        keys
          .filter((key) => key !== CACHE_VERSION)
          .map((key) => caches.delete(key))
      )
    )
  );
  self.clients.claim();
});

// Fetch: route requests through the appropriate strategy.
self.addEventListener('fetch', (event) => {
  const url = new URL(event.request.url);

  // MUST NOT cache SSE streams or POST requests.
  if (event.request.method !== 'GET') {
    return;
  }
  if (url.pathname.match(/\/sessions\/\d+\/stream/)) {
    return;
  }

  // Network-first for /api/v1/ endpoints with cache fallback.
  if (url.pathname.startsWith('/api/v1/')) {
    event.respondWith(
      fetch(event.request)
        .then((response) => {
          const clone = response.clone();
          caches.open(CACHE_VERSION).then((cache) => cache.put(event.request, clone));
          return response;
        })
        .catch(() => caches.match(event.request))
    );
    return;
  }

  // Cache-first for static assets.
  event.respondWith(
    caches.match(event.request).then((cached) => {
      if (cached) {
        return cached;
      }
      return fetch(event.request).then((response) => {
        // Only cache same-origin successful responses.
        if (response.ok && url.origin === self.location.origin) {
          const clone = response.clone();
          caches.open(CACHE_VERSION).then((cache) => cache.put(event.request, clone));
        }
        return response;
      });
    })
  );
});
