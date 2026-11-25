function luminance(hex: string) {
  const n = parseInt(hex.slice(1), 16)
  const [r, g, b] = [(n >> 16) & 255, (n >> 8) & 255, n & 255].map((c) => {
    const s = c / 255
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4
  })
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

/** Mixes hex towards white by t (0..1). */
function lighten(hex: string, t: number) {
  const n = parseInt(hex.slice(1), 16)
  const ch = [(n >> 16) & 255, (n >> 8) & 255, n & 255].map((c) => Math.round(c + (255 - c) * t))
  return "#" + ch.map((c) => c.toString(16).padStart(2, "0")).join("")
}

const fgFor = (hex: string) => (luminance(hex) > 0.45 ? "#0a0a0a" : "#ffffff")

/** Applies the accent colour (admin branding or a user's override). */
export function applyAccent(hex: string) {
  const root = document.documentElement.style
  const props = ["--brand", "--brand-foreground", "--brand-dark", "--brand-dark-foreground"]
  if (!/^#[0-9a-f]{6}$/i.test(hex)) {
    props.forEach((p) => root.removeProperty(p))
    return
  }
  root.setProperty("--brand", hex)
  root.setProperty("--brand-foreground", fgFor(hex))
  // Very dark accents would vanish on the dark theme; lift them.
  const dark = luminance(hex) < 0.12 ? lighten(hex, 0.85) : hex
  root.setProperty("--brand-dark", dark)
  root.setProperty("--brand-dark-foreground", fgFor(dark))
}
