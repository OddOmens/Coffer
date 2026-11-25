import { useEffect } from "react"
import { useTheme } from "next-themes"

/** Tints the browser and status bar to match the chosen theme, not just the system one. */
export function ThemeColor() {
  const { resolvedTheme } = useTheme()
  useEffect(() => {
    const mq = window.matchMedia("(max-width: 767px)")
    const apply = () => {
      // Phones show the page surface edge to edge; wider windows show the canvas.
      const color = getComputedStyle(document.documentElement).getPropertyValue(mq.matches ? "--background" : "--canvas").trim()
      document.querySelectorAll<HTMLMetaElement>('meta[name="theme-color"]').forEach((m) => {
        m.content = color
        m.removeAttribute("media")
      })
    }
    // After next-themes has swapped the class on <html>, which happens in its own effect.
    const raf = requestAnimationFrame(apply)
    mq.addEventListener("change", apply)
    return () => {
      cancelAnimationFrame(raf)
      mq.removeEventListener("change", apply)
    }
  }, [resolvedTheme])
  return null
}
