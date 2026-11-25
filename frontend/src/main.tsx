import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { QueryClientProvider } from "@tanstack/react-query"
import { ThemeProvider } from "next-themes"
import { BrowserRouter } from "react-router"

import App from "@/App"
import { ThemeColor } from "@/components/theme-color"
import { Toaster } from "@/components/ui/sonner"
import { TooltipProvider } from "@/components/ui/tooltip"
import { queryClient } from "@/lib/boot"
import "./index.css"

// After an update the server only has the new build's files, so a tab opened
// before it can't load the parts it fetches later (screens, previews). Reload
// once to pick up the new build; the timestamp stops a loop if the file is
// really missing.
window.addEventListener("vite:preloadError", (event) => {
  let last = 0
  try {
    last = Number(sessionStorage.getItem("coffer:reloaded-at")) || 0
    if (Date.now() - last < 60_000) return
    sessionStorage.setItem("coffer:reloaded-at", String(Date.now()))
  } catch {
    // No session storage: still reload, but only this once per tab.
    if ((window as { cofferReloaded?: boolean }).cofferReloaded) return
    ;(window as { cofferReloaded?: boolean }).cofferReloaded = true
  }
  event.preventDefault()
  window.location.reload()
})

// Installable PWA. Service workers need HTTPS (or localhost).
if ("serviceWorker" in navigator && import.meta.env.PROD && window.isSecureContext) {
  window.addEventListener("load", () => navigator.serviceWorker.register("/sw.js").catch(() => {}))
}

// Bottom sheets sit on top of the on-screen keyboard: --kb is how much of the
// layout viewport it covers (iOS doesn't resize the page for it).
const vv = window.visualViewport
if (vv) {
  const update = () => {
    const kb = Math.max(0, window.innerHeight - vv.height - vv.offsetTop)
    document.documentElement.style.setProperty("--kb", `${Math.round(kb)}px`)
  }
  vv.addEventListener("resize", update)
  vv.addEventListener("scroll", update)
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ThemeProvider attribute="class" defaultTheme="system" enableSystem disableTransitionOnChange>
      <ThemeColor />
      <QueryClientProvider client={queryClient}>
        <BrowserRouter>
          <TooltipProvider delayDuration={300}>
            <App />
            <Toaster position="bottom-left" />
          </TooltipProvider>
        </BrowserRouter>
      </QueryClientProvider>
    </ThemeProvider>
  </StrictMode>,
)
