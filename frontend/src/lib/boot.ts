import { QueryClient } from "@tanstack/react-query"

import { api, ApiError, type AppConfig, type Me } from "@/lib/api"
import { whenIdle } from "@/lib/idle"
import { restoreShell, wasSignedIn } from "@/lib/shell-cache"
import { loadScreen, screenFor } from "@/routes"

/**
 * Starts everything the first screen needs at once, before React renders:
 * the branding, the session check, the screen's code, and the data the
 * sidebar and the screen will ask for. They used to run one after another.
 * The keys and fetchers are the components' own, so they find the answers
 * (or the request still under way) instead of asking again.
 */

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 10_000,
      refetchOnWindowFocus: false,
      retry: (count, err) => !(err instanceof ApiError && err.status < 500) && count < 2,
    },
  },
})

restoreShell(queryClient)

// staleTime 0: ask even when a copy from the last visit is in the cache.
const fetch = <T>(queryKey: unknown[], queryFn: () => Promise<T>, retry?: false) =>
  void queryClient.prefetchQuery({ queryKey, queryFn, staleTime: 0, ...(retry === false && { retry }) })

const screen = screenFor(location.pathname)
if (screen) void loadScreen(screen).catch(() => {})

const known = queryClient.getQueryData<AppConfig>(["config"])
fetch(["config"], api.config)

// A browser with no record of a session will most likely be shown the sign-in screen.
if (screen !== "publicShare" && screen !== "login" && !wasSignedIn() && !known?.publicOnly) void loadScreen("login").catch(() => {})

// Only a browser that was signed in asks for the rest up front. Anyone else
// (a visitor to the share-links address, someone about to sign in) waits for
// the app to decide, as before, rather than collecting refusals.
if (screen !== "publicShare" && screen !== "login" && wasSignedIn() && !known?.publicOnly) {
  fetch(["me"], api.me, false)
  fetch(["places"], api.places)
  fetch(["favorites"], api.favorites)
  fetch(["tags"], api.tags)
  fetch(["notifications"], api.notifications)
  if (screen === "files") {
    const [, , place, ...rest] = location.pathname.split("/")
    try {
      const placeId = place ? decodeURIComponent(place) : ""
      const path = ("/" + rest.map(decodeURIComponent).join("/")).replace(/\/+$/, "") || "/"
      // The same key as pages/files.tsx: the last part is the "show hidden files" preference.
      const showHidden = queryClient.getQueryData<Me>(["me"])?.prefs?.showHidden ?? false
      if (placeId) fetch(["list", placeId, path, showHidden], () => api.list(placeId, path))
    } catch {
      // A malformed address: the screen deals with it.
    }
  }

  // Then, quietly, the screens most likely to be opened next.
  const saveData = (navigator as { connection?: { saveData?: boolean } }).connection?.saveData
  if (!saveData) {
    whenIdle(() => {
      for (const next of ["files", "home"] as const) if (next !== screen) void loadScreen(next).catch(() => {})
    }, 4000)
  }
}
