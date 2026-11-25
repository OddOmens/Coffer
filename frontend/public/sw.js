// Coffer service worker: makes the app installable and quick to start.
// Only the app shell and hashed assets are cached. API responses, file
// downloads, WebDAV and share content are never cached.
const CACHE = "coffer-shell-v1"

self.addEventListener("install", (event) => {
  event.waitUntil(caches.open(CACHE).then((c) => c.add("/")).then(() => self.skipWaiting()))
})

self.addEventListener("activate", (event) => {
  event.waitUntil(
    (async () => {
      const keys = await caches.keys()
      await Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k)))
      // Lets the browser ask for a page while this worker is still starting
      // up, instead of waiting for it.
      if (self.registration.navigationPreload) await self.registration.navigationPreload.enable()
      await self.clients.claim()
    })(),
  )
})

// Keeps the newest shell for offline starts. A shell that differs from the
// stored one is a new build, whose assets have new names: the old build's
// are dropped so the cache doesn't grow with every update.
async function keepShell(res) {
  const cache = await caches.open(CACHE)
  const old = await cache.match("/")
  if (old) {
    const [next, prev] = await Promise.all([res.clone().text(), old.text()])
    if (next !== prev) {
      const keys = await cache.keys()
      await Promise.all(keys.filter((k) => new URL(k.url).pathname.startsWith("/assets/")).map((k) => cache.delete(k)))
    }
  }
  await cache.put("/", res)
}

self.addEventListener("fetch", (event) => {
  const req = event.request
  if (req.method !== "GET") return
  const url = new URL(req.url)
  if (url.origin !== self.location.origin) return
  if (url.pathname.startsWith("/api/") || url.pathname.startsWith("/dav")) return

  // Hashed build assets never change: cache first.
  if (url.pathname.startsWith("/assets/")) {
    event.respondWith(
      caches.match(req).then(
        (hit) =>
          hit ||
          fetch(req).then((res) => {
            if (res.ok) {
              const copy = res.clone()
              event.waitUntil(caches.open(CACHE).then((c) => c.put(req, copy)))
            }
            return res
          }),
      ),
    )
    return
  }

  // Pages: always try the network, fall back to the cached shell offline.
  if (req.mode === "navigate") {
    event.respondWith(
      (async () => {
        try {
          const res = (await event.preloadResponse) || (await fetch(req))
          if (res.ok && !url.pathname.startsWith("/s/")) event.waitUntil(keepShell(res.clone()).catch(() => {}))
          return res
        } catch {
          return (await caches.match("/")) || Response.error()
        }
      })(),
    )
  }
})
