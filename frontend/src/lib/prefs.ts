import { useCallback, useEffect, useRef } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { useTheme } from "next-themes"

import { useConfig } from "@/App"
import { api, type Me, type Prefs } from "@/lib/api"
import { applyAccent } from "@/lib/branding"

/** Effective preferences: the user's choices over the admin's defaults. */
export function usePrefs(): [Required<Prefs>, (p: Prefs) => void] {
  const qc = useQueryClient()
  const { data: config } = useConfig()
  // Subscribe (not just read the cache) so a change re-renders every user of the prefs.
  const { data: me } = useQuery({ queryKey: ["me"], queryFn: api.me, staleTime: 60_000, retry: false })
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)

  const prefs: Required<Prefs> = {
    theme: me?.prefs?.theme ?? config?.defaultTheme ?? "system",
    density: me?.prefs?.density ?? config?.defaultDensity ?? "comfortable",
    accent: me?.prefs?.accent ?? "",
    view: me?.prefs?.view ?? "list",
    quickCreate: me?.prefs?.quickCreate ?? config?.showQuickCreate ?? true,
    pageSize: me?.prefs?.pageSize ?? 50,
    stackVersions: me?.prefs?.stackVersions ?? true,
    emailNotify: me?.prefs?.emailNotify ?? true,
    showHidden: me?.prefs?.showHidden ?? false,
  }

  const update = useCallback(
    (p: Prefs) => {
      const cur = qc.getQueryData<Me | null>(["me"])
      if (!cur) return
      const next = { ...cur.prefs, ...p }
      qc.setQueryData(["me"], { ...cur, prefs: next })
      clearTimeout(timer.current)
      timer.current = setTimeout(() => api.savePrefs(next).catch(() => {}), 400)
    },
    [qc],
  )
  return [prefs, update]
}

/** Applies theme, density and accent whenever they change. */
export function useApplyPrefs(prefs: Required<Prefs>) {
  const { setTheme } = useTheme()
  const { data: config } = useConfig()
  useEffect(() => setTheme(prefs.theme), [prefs.theme, setTheme])
  useEffect(() => {
    document.documentElement.dataset.density = prefs.density
  }, [prefs.density])
  useEffect(() => applyAccent(prefs.accent || config?.accentColor || ""), [prefs.accent, config?.accentColor])
}
