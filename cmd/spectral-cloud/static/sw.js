/* Spectral Cloud — Service Worker
 * Provides offline support and fast repeat loads via a cache-first strategy
 * for static assets, falling back to network for API calls.
 */

const CACHE_VERSION = 'spectral-v1';
const STATIC_ASSETS = [
  '/ui/',
  '/ui/index.html',
  '/ui/manifest.json',
  '/ui/terms.html',
  '/ui/privacy.html',
  '/ui/security.html'
];

// ── Install: pre-cache static shell ──────────────────────────────────────────
self.addEventListener('install', function(event) {
  event.waitUntil(
    caches.open(CACHE_VERSION).then(function(cache) {
      return cache.addAll(STATIC_ASSETS);
    }).then(function() {
      return self.skipWaiting();
    })
  );
});

// ── Activate: remove stale caches ────────────────────────────────────────────
self.addEventListener('activate', function(event) {
  event.waitUntil(
    caches.keys().then(function(keys) {
      return Promise.all(
        keys.filter(function(k) { return k !== CACHE_VERSION; })
            .map(function(k) { return caches.delete(k); })
      );
    }).then(function() {
      return self.clients.claim();
    })
  );
});

// ── Fetch: cache-first for static, network-first for API ─────────────────────
self.addEventListener('fetch', function(event) {
  const url = new URL(event.request.url);

  // Always hit the network for API calls and metrics
  if (url.pathname.startsWith('/blockchain') ||
      url.pathname.startsWith('/routes') ||
      url.pathname.startsWith('/agents') ||
      url.pathname.startsWith('/admin') ||
      url.pathname.startsWith('/auth') ||
      url.pathname.startsWith('/metrics') ||
      url.pathname.startsWith('/health') ||
      url.pathname.startsWith('/events') ||
      url.pathname.startsWith('/jobs') ||
      url.pathname.startsWith('/kv') ||
      url.pathname.startsWith('/notify') ||
      url.pathname.startsWith('/schedules') ||
      url.pathname.startsWith('/webhook') ||
      url.pathname.startsWith('/proto') ||
      url.pathname.startsWith('/system') ||
      url.pathname.startsWith('/pipeline') ||
      url.pathname.startsWith('/search') ||
      url.pathname.startsWith('/audit')) {
    event.respondWith(networkFirst(event.request));
    return;
  }

  // Cache-first for static UI assets
  event.respondWith(cacheFirst(event.request));
});

function cacheFirst(request) {
  return caches.match(request).then(function(cached) {
    if (cached) return cached;
    return fetch(request).then(function(response) {
      if (response && response.status === 200 && response.type === 'basic') {
        const clone = response.clone();
        caches.open(CACHE_VERSION).then(function(cache) {
          cache.put(request, clone);
        });
      }
      return response;
    }).catch(function() {
      // Offline fallback: return cached index
      return caches.match('/ui/index.html');
    });
  });
}

function networkFirst(request) {
  return fetch(request).catch(function(err) {
    // Network unavailable — serve cached response if available.
    // Errors are not re-thrown here because the service worker is an
    // enhancement only; failed API calls surface to callers normally.
    return caches.match(request);
  });
}
