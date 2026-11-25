import { useSyncExternalStore } from "react"

// Backblaze B2 bills storage by the day for what a bucket holds; uploads are
// free. Its Caps & Alerts page states the rate as how many GB a daily cap of
// $0.01 covers, so that is the number Coffer asks for and keeps.

export const B2_DEFAULT_GB_PER_CENT = 53
const KEY = "coffer.b2GbPerCent"
const listeners = new Set<() => void>()

function read() {
  try {
    const v = Number(localStorage.getItem(KEY))
    return v > 0 ? v : B2_DEFAULT_GB_PER_CENT
  } catch {
    return B2_DEFAULT_GB_PER_CENT
  }
}

export function setB2Rate(gbPerCent: number) {
  try {
    if (gbPerCent > 0 && gbPerCent !== B2_DEFAULT_GB_PER_CENT) localStorage.setItem(KEY, String(gbPerCent))
    else localStorage.removeItem(KEY)
  } catch {
    // Not kept; it still applies until the page reloads.
  }
  listeners.forEach((l) => l())
}

/** GB a $0.01 daily storage cap holds, as set on this browser. */
export function useB2Rate() {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l)
      return () => listeners.delete(l)
    },
    read,
  )
}

export const isB2 = (endpoint?: string) => /backblazeb2\.com/i.test(endpoint ?? "")

/** Backblaze counts a GB as a billion bytes. */
export const toGB = (bytes: number) => bytes / 1e9

/** What storing gb costs per day, month and year, in dollars. */
export function b2Cost(gb: number, gbPerCent: number) {
  const day = (gb / gbPerCent) * 0.01
  return { day, month: day * 30, year: day * 365 }
}

/** The smallest daily storage cap, in whole cents, that holds gb. */
export const b2Cap = (gb: number, gbPerCent: number) => Math.max(0.01, Math.ceil(gb / gbPerCent) / 100)

export function formatMoney(dollars: number) {
  // Small amounts keep a third decimal so a few GB don't all read $0.00.
  const digits = dollars > 0 && dollars < 0.1 ? 3 : 2
  return new Intl.NumberFormat(undefined, { style: "currency", currency: "USD", minimumFractionDigits: digits, maximumFractionDigits: digits }).format(dollars)
}

export function formatGB(gb: number) {
  if (gb >= 1000) return `${(gb / 1000).toFixed(gb >= 10_000 ? 0 : 2)} TB`
  return `${gb.toFixed(gb < 10 ? 1 : 0)} GB`
}
