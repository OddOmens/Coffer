/**
 * The code for each screen, loaded when it's first shown. Kept apart from
 * App.tsx so the app can start fetching the screen in the address bar before
 * React has rendered anything (lib/boot.ts).
 */
export const screens = {
  home: () => import("@/pages/home"),
  files: () => import("@/pages/files"),
  sharedWithMe: () => import("@/pages/shared-with-me"),
  shares: () => import("@/pages/shares"),
  trash: () => import("@/pages/trash"),
  notifications: () => import("@/pages/notifications"),
  account: () => import("@/pages/account"),
  editor: () => import("@/pages/editor"),
  users: () => import("@/pages/admin-users"),
  drives: () => import("@/pages/admin-drives"),
  monitor: () => import("@/pages/admin-monitor"),
  settings: () => import("@/pages/admin-settings"),
  audit: () => import("@/pages/admin-audit"),
  publicShare: () => import("@/pages/public-share"),
  backups: () => import("@/pages/admin-backups"),
  snapshot: () => import("@/pages/admin-snapshot"),
  collections: () => import("@/pages/collections"),
  collection: () => import("@/pages/collection"),
  tags: () => import("@/pages/tags"),
  login: () => import("@/pages/login"),
}

export type Screen = keyof typeof screens
export type ScreenModule<K extends Screen> = Awaited<ReturnType<(typeof screens)[K]>>

const loaded: { [K in Screen]?: ScreenModule<K> } = {}

/** Loads a screen's code, remembering it so a screen that's already here renders at once. */
export function loadScreen<K extends Screen>(name: K): Promise<ScreenModule<K>> {
  return (screens[name]() as Promise<ScreenModule<K>>).then((m) => {
    loaded[name] = m as (typeof loaded)[K]
    return m
  })
}

export const loadedScreen = <K extends Screen>(name: K) => loaded[name] as ScreenModule<K> | undefined

/** The screen a URL shows; the same table as the routes in App.tsx. */
export function screenFor(pathname: string): Screen | undefined {
  const [first = "", second, third, fourth] = pathname.split("/").slice(1)
  switch (first) {
    case "s":
      return second ? "publicShare" : undefined
    case "":
    case "home":
      return "home"
    case "files":
      // /files alone redirects to the home place.
      return "files"
    case "edit":
      return "editor"
    case "shared":
      return "sharedWithMe"
    case "collections":
      return second ? "collection" : "collections"
    case "shares":
    case "trash":
    case "notifications":
    case "account":
    case "tags":
    case "login":
      return first
    case "admin":
      if (second === "backups") return third && fourth === "browse" ? "snapshot" : "backups"
      if (second === "users" || second === "drives" || second === "monitor" || second === "settings" || second === "audit") return second
      return "users"
  }
  return undefined
}
