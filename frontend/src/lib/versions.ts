import type { Entry } from "@/lib/api"

// Same rule as the server (backend/internal/files/versions.go): "spot_v12.mov",
// "Spot v3 final.aep" and "logo-V002.png" are versions; anything after the
// number is ignored, so "spot_v12_final.mov" stacks with "spot_v11.mov".
const versionRe = /^(.*?)[\s._-]+v(\d{1,4})(?:[\s._-].*)?$/i

export function versionOf(name: string): { key: string; v: number } | null {
  const dot = name.lastIndexOf(".")
  const ext = dot > 0 ? name.slice(dot).toLowerCase() : ""
  const base = dot > 0 ? name.slice(0, dot) : name
  const m = versionRe.exec(base)
  if (!m || !m[1].trim()) return null
  return { key: m[1].trim().toLowerCase() + "\0" + ext, v: Number(m[2]) }
}

export type Stacked<T extends Entry> = T & {
  /** On the newest version: the older ones, newest first. */
  older?: T[]
  /** On an older version shown under its stack. */
  stackChild?: boolean
  version?: number
}

/**
 * Collapses versioned files into one row per stack (the newest), keeping
 * the original order. Stacks in `expanded` (by key) are followed by their
 * older versions.
 */
export function stackVersions<T extends Entry>(entries: T[], expanded: Set<string>): Stacked<T>[] {
  const groups = new Map<string, Stacked<T>[]>()
  for (const e of entries) {
    if (e.isDir) continue
    const v = versionOf(e.name)
    if (!v) continue
    const list = groups.get(v.key) ?? []
    list.push({ ...e, version: v.v })
    groups.set(v.key, list)
  }
  const head = new Map<string, Stacked<T>>()
  for (const [key, list] of groups) {
    if (list.length < 2) continue
    list.sort((a, b) => (b.version! - a.version!) || b.modTime - a.modTime)
    head.set(key, { ...list[0], older: list.slice(1) })
  }
  const out: Stacked<T>[] = []
  const placed = new Set<string>()
  for (const e of entries) {
    const v = e.isDir ? null : versionOf(e.name)
    const h = v && head.get(v.key)
    if (!h) {
      out.push(e)
      continue
    }
    if (placed.has(v.key)) continue
    placed.add(v.key)
    out.push(h)
    if (expanded.has(v.key)) for (const o of h.older!) out.push({ ...o, stackChild: true })
  }
  return out
}

export const stackKey = (e: Entry) => versionOf(e.name)?.key ?? ""
