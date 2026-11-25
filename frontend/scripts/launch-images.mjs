// Draws the launch screens iOS shows while a Home Screen app starts, and
// writes the <link> tags that point at them into index.html.
//
//   node scripts/launch-images.mjs
//
// iOS only uses a launch image whose pixel size is exactly the screen's, so
// there is one per screen size, upright (and on its side for iPads), light
// and dark: the app's background with its icon in the middle. Without one the
// app opens on a white screen, whatever the theme. Run this again after
// changing the icon or the background colours, or to add a new screen size.
// It needs nothing but Node.
import { mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs"
import path from "node:path"
import { fileURLToPath } from "node:url"
import { deflateSync, inflateSync } from "node:zlib"

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..")
const outDir = path.join(root, "public/launch")
const htmlFile = path.join(root, "index.html")

// Screen sizes in CSS pixels (upright) and pixels per CSS pixel.
const PHONES = [
  [375, 667, 2], // SE (2nd, 3rd), 8
  [414, 736, 3], // 8 Plus
  [375, 812, 3], // X, XS, 11 Pro, 12 mini, 13 mini
  [414, 896, 2], // XR, 11
  [414, 896, 3], // XS Max, 11 Pro Max
  [390, 844, 3], // 12, 13, 14, 16e
  [428, 926, 3], // 12 Pro Max, 13 Pro Max, 14 Plus
  [393, 852, 3], // 14 Pro, 15, 15 Pro, 16
  [430, 932, 3], // 14 Pro Max, 15 Plus, 15 Pro Max, 16 Plus
  [402, 874, 3], // 16 Pro, 17, 17 Pro
  [440, 956, 3], // 16 Pro Max, 17 Pro Max
  [420, 912, 3], // Air
]
const TABLETS = [
  [768, 1024, 2], // iPad 9.7", mini 5
  [744, 1133, 2], // mini 6, mini 7
  [810, 1080, 2], // iPad 10.2"
  [820, 1180, 2], // iPad 10.9" and 11", Air 10.9" and 11"
  [834, 1112, 2], // Air 10.5", Pro 10.5"
  [834, 1194, 2], // Pro 11" (to M2)
  [834, 1210, 2], // Pro 11" (M4 on)
  [1024, 1366, 2], // Pro 12.9", Air 13"
  [1032, 1376, 2], // Pro 13" (M4 on)
]
// The same colours as src/index.css: phones show the page surface edge to
// edge (--background), wider screens the canvas behind the sidebar (--canvas).
const COLOURS = {
  phone: { light: "#ffffff", dark: "#18181a" },
  tablet: { light: "#f4f4f5", dark: "#0e0e0f" },
}
/** The icon's width on the launch screen, in CSS pixels. */
const ICON = 96

// --- PNG, just enough of it -------------------------------------------------

const crcTable = Array.from({ length: 256 }, (_, n) => {
  let c = n
  for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1
  return c >>> 0
})
function crc(buf) {
  let c = 0xffffffff
  for (const b of buf) c = crcTable[(c ^ b) & 0xff] ^ (c >>> 8)
  return (c ^ 0xffffffff) >>> 0
}
function chunk(type, data) {
  const body = Buffer.concat([Buffer.from(type, "latin1"), data])
  const out = Buffer.alloc(body.length + 8)
  out.writeUInt32BE(data.length, 0)
  body.copy(out, 4)
  out.writeUInt32BE(crc(body), body.length + 4)
  return out
}

/** Reads an 8-bit RGBA PNG (not interlaced) into { width, height, data }. */
function decode(file) {
  const png = readFileSync(file)
  const width = png.readUInt32BE(16)
  const height = png.readUInt32BE(20)
  if (png[24] !== 8 || png[25] !== 6 || png[28] !== 0) throw new Error(`${file}: expected an 8-bit RGBA PNG that isn't interlaced`)
  const parts = []
  for (let at = 8; at < png.length; ) {
    const len = png.readUInt32BE(at)
    if (png.toString("latin1", at + 4, at + 8) === "IDAT") parts.push(png.subarray(at + 8, at + 8 + len))
    at += len + 12
  }
  const raw = inflateSync(Buffer.concat(parts))
  const stride = width * 4
  const data = Buffer.alloc(stride * height)
  for (let y = 0; y < height; y++) {
    const filter = raw[y * (stride + 1)]
    const line = raw.subarray(y * (stride + 1) + 1, (y + 1) * (stride + 1))
    for (let x = 0; x < stride; x++) {
      const left = x >= 4 ? data[y * stride + x - 4] : 0
      const up = y ? data[(y - 1) * stride + x] : 0
      const corner = x >= 4 && y ? data[(y - 1) * stride + x - 4] : 0
      let predicted = 0
      if (filter === 1) predicted = left
      else if (filter === 2) predicted = up
      else if (filter === 3) predicted = (left + up) >> 1
      else if (filter === 4) {
        const p = left + up - corner
        const [a, b, c] = [Math.abs(p - left), Math.abs(p - up), Math.abs(p - corner)]
        predicted = a <= b && a <= c ? left : b <= c ? up : corner
      }
      data[y * stride + x] = (line[x] + predicted) & 0xff
    }
  }
  return { width, height, data }
}

/** Shrinks an RGBA image to size x size, averaging the pixels each new one covers. */
function shrink(img, size) {
  const out = new Float64Array(size * size * 4)
  const scale = img.width / size
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      let [r, g, b, a, n] = [0, 0, 0, 0, 0]
      for (let sy = Math.floor(y * scale); sy < Math.ceil((y + 1) * scale); sy++) {
        for (let sx = Math.floor(x * scale); sx < Math.ceil((x + 1) * scale); sx++) {
          const i = (sy * img.width + sx) * 4
          const alpha = img.data[i + 3] / 255
          r += img.data[i] * alpha
          g += img.data[i + 1] * alpha
          b += img.data[i + 2] * alpha
          a += alpha
          n++
        }
      }
      // Colours stay multiplied by their opacity, ready to lay over a background.
      out.set([r / n, g / n, b / n, a / n], (y * size + x) * 4)
    }
  }
  return out
}

/**
 * A width x height PNG of one colour with the icon in the middle. It's stored
 * with a palette (a byte a pixel) and each row as its difference from the row
 * above, so the plain background costs next to nothing.
 */
function launchImage(width, height, colour, icon, size) {
  const bg = [1, 3, 5].map((i) => parseInt(colour.slice(i, i + 2), 16))
  // The icon laid over the background, then its colours rounded until 255 of them (and the background) are enough.
  const over = new Uint8Array(size * size * 3)
  for (let i = 0; i < size * size; i++) {
    const keep = 1 - icon[i * 4 + 3]
    for (let c = 0; c < 3; c++) over[i * 3 + c] = Math.round(icon[i * 4 + c] + bg[c] * keep)
  }
  let palette, cells
  for (let drop = 0; ; drop++) {
    palette = new Map([[(bg[0] << 16) | (bg[1] << 8) | bg[2], 0]])
    cells = new Uint8Array(size * size)
    const round = (v) => (drop ? Math.min(255, (((v >> drop) << drop) | (1 << (drop - 1)))) : v)
    let fits = true
    for (let i = 0; i < size * size && fits; i++) {
      const same = over[i * 3] === bg[0] && over[i * 3 + 1] === bg[1] && over[i * 3 + 2] === bg[2]
      const key = same ? (bg[0] << 16) | (bg[1] << 8) | bg[2] : (round(over[i * 3]) << 16) | (round(over[i * 3 + 1]) << 8) | round(over[i * 3 + 2])
      if (!palette.has(key)) {
        if (palette.size === 256) fits = false
        else palette.set(key, palette.size)
      }
      cells[i] = palette.get(key) ?? 0
    }
    if (fits) break
  }
  const stride = width + 1
  const raw = Buffer.alloc(stride * height)
  const left = Math.round((width - size) / 2)
  const top = Math.round((height - size) / 2)
  const row = Buffer.alloc(width)
  let above = Buffer.alloc(width)
  for (let y = 0; y < height; y++) {
    row.fill(0)
    if (y >= top && y < top + size) row.set(cells.subarray((y - top) * size, (y - top + 1) * size), left)
    raw[y * stride] = 2
    for (let x = 0; x < width; x++) raw[y * stride + 1 + x] = (row[x] - above[x]) & 0xff
    above = Buffer.from(row)
  }
  const header = Buffer.alloc(13)
  header.writeUInt32BE(width, 0)
  header.writeUInt32BE(height, 4)
  header.set([8, 3, 0, 0, 0], 8) // 8 bits a pixel, palette
  const colours = Buffer.alloc(palette.size * 3)
  for (const [key, i] of palette) colours.set([key >> 16, (key >> 8) & 0xff, key & 0xff], i * 3)
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk("IHDR", header),
    chunk("PLTE", colours),
    chunk("IDAT", deflateSync(raw, { level: 9 })),
    chunk("IEND", Buffer.alloc(0)),
  ])
}

// --- The images and their tags ----------------------------------------------

const source = decode(path.join(root, "public/icon-512.png"))
const icons = new Map()
const iconAt = (ratio) => {
  if (!icons.has(ratio)) icons.set(ratio, shrink(source, ICON * ratio))
  return icons.get(ratio)
}

rmSync(outDir, { recursive: true, force: true })
mkdirSync(outDir, { recursive: true })

const tags = []
const made = new Set()
let bytes = 0
for (const [kind, list] of [["phone", PHONES], ["tablet", TABLETS]]) {
  for (const [w, h, ratio] of list) {
    // An iPhone's Home Screen is always upright; an iPad can start either way up.
    for (const turned of kind === "tablet" ? [false, true] : [false]) {
      const [pw, ph] = turned ? [h * ratio, w * ratio] : [w * ratio, h * ratio]
      for (const theme of ["light", "dark"]) {
        const name = `${pw}x${ph}-${theme}.png`
        if (made.has(name)) throw new Error(`two screens want ${name}`)
        const png = launchImage(pw, ph, COLOURS[kind][theme], iconAt(ratio), ICON * ratio)
        writeFileSync(path.join(outDir, name), png)
        made.add(name)
        bytes += png.length
        const media = `(prefers-color-scheme:${theme}) and (device-width:${w}px) and (device-height:${h}px) and (-webkit-device-pixel-ratio:${ratio}) and (orientation:${turned ? "landscape" : "portrait"})`
        tags.push(`    <link rel="apple-touch-startup-image" media="${media}" href="/launch/${name}" />`)
      }
    }
  }
}

const START = "    <!-- launch-images:start (written by scripts/launch-images.mjs) -->"
const END = "    <!-- launch-images:end -->"
const html = readFileSync(htmlFile, "utf8")
const [before, rest] = html.split(START)
if (rest === undefined || !rest.includes(END)) throw new Error("index.html has lost its launch-images markers")
writeFileSync(htmlFile, `${before}${START}\n${tags.join("\n")}\n${END}${rest.split(END)[1]}`)

console.log(`${made.size} launch images (${(bytes / 1024).toFixed(0)} kB) in public/launch, ${tags.length} tags in index.html`)
if (readdirSync(outDir).length !== made.size) throw new Error("public/launch holds files this script didn't write")
