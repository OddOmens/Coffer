import { useEffect, useRef, useState } from "react"
import { ExternalLinkIcon } from "lucide-react"
import DOMPurify from "dompurify"
import { marked } from "marked"

import { Spinner } from "@/components/states"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

const MAX_OFFICE = 60 << 20

async function fetchBuffer(src: string, signal: AbortSignal) {
  const res = await fetch(src, { credentials: "same-origin", signal })
  if (!res.ok) throw new Error("Could not load the file")
  const len = Number(res.headers.get("Content-Length") || 0)
  if (len > MAX_OFFICE) throw new Error("This file is too large to preview. Download it to open it.")
  return res.arrayBuffer()
}

function useBuffer(src: string) {
  const [buf, setBuf] = useState<ArrayBuffer | null>(null)
  const [error, setError] = useState("")
  useEffect(() => {
    const ctl = new AbortController()
    setBuf(null)
    setError("")
    fetchBuffer(src, ctl.signal).then(setBuf, (e) => !ctl.signal.aborted && setError(e.message))
    return () => ctl.abort()
  }, [src])
  return { buf, error }
}

function Failed({ message }: { message: string }) {
  return <p className="p-8 text-center text-sm text-muted-foreground">{message}</p>
}

/** Keeps only http(s)/mailto links, opened safely in a new tab. */
function tameLinks(root: HTMLElement) {
  root.querySelectorAll("a[href]").forEach((a) => {
    const href = a.getAttribute("href") ?? ""
    if (/^(https?:|mailto:)/i.test(href)) {
      a.setAttribute("target", "_blank")
      a.setAttribute("rel", "noopener noreferrer")
    } else if (!href.startsWith("#")) {
      a.removeAttribute("href")
    }
  })
}

export function DocxPreview({ src }: { src: string }) {
  const { buf, error } = useBuffer(src)
  const ref = useRef<HTMLDivElement>(null)
  const [failed, setFailed] = useState("")
  useEffect(() => {
    if (!buf || !ref.current) return
    let cancelled = false
    import("docx-preview").then(({ renderAsync }) => {
      if (cancelled || !ref.current) return
      ref.current.innerHTML = ""
      renderAsync(buf, ref.current, undefined, {
        inWrapper: false,
        breakPages: true,
        ignoreLastRenderedPageBreak: true,
        useBase64URL: true,
        renderComments: false,
        // altChunks are raw HTML parts, shown in a same-origin srcdoc iframe.
        renderAltChunks: false,
      })
        .then(() => ref.current && tameLinks(ref.current))
        .catch(() => setFailed("This document could not be displayed. Download it to open it in Word or Pages."))
    })
    return () => {
      cancelled = true
    }
  }, [buf])
  if (error || failed) return <Failed message={error || failed} />
  return (
    <div className="size-full overflow-auto bg-muted/60">
      {!buf && (
        <div className="flex h-full items-center justify-center">
          <Spinner />
        </div>
      )}
      <div ref={ref} className="docx-host flex flex-col items-center gap-4 p-4 [&_section.docx]:min-h-[60vh] [&_section.docx]:max-w-full [&_section.docx]:bg-white [&_section.docx]:text-black [&_section.docx]:shadow-md [&_section.docx]:p-[min(72px,6vw)]" />
    </div>
  )
}

type Sheet = { name: string; rows: (string | number | boolean | null)[][] }

export function SheetPreview({ src }: { src: string }) {
  const { buf, error } = useBuffer(src)
  const [sheets, setSheets] = useState<Sheet[] | null>(null)
  const [active, setActive] = useState(0)
  const [failed, setFailed] = useState("")
  useEffect(() => {
    if (!buf) return
    import("xlsx").then((XLSX) => {
      try {
        const wb = XLSX.read(buf, { type: "array", cellDates: true, sheetRows: 2001 })
        setSheets(
          wb.SheetNames.slice(0, 30).map((name) => ({
            name,
            rows: (XLSX.utils.sheet_to_json(wb.Sheets[name], { header: 1, raw: false, defval: null, blankrows: false }) as Sheet["rows"])
              .slice(0, 2000)
              .map((r) => r.slice(0, 100)),
          })),
        )
      } catch {
        setFailed("This spreadsheet could not be read. Download it to open it in Excel or Numbers.")
      }
    })
  }, [buf])
  if (error || failed) return <Failed message={error || failed} />
  if (!sheets)
    return (
      <div className="flex h-full items-center justify-center">
        <Spinner />
      </div>
    )
  const sheet = sheets[active]
  const width = Math.max(0, ...sheet.rows.map((r) => r.length))
  const col = (i: number) => {
    let s = ""
    for (i++; i > 0; i = Math.floor((i - 1) / 26)) s = String.fromCharCode(65 + ((i - 1) % 26)) + s
    return s
  }
  return (
    <div className="flex size-full flex-col bg-background">
      <div className="flex-1 overflow-auto">
        <table className="border-separate border-spacing-0 text-xs">
          <thead className="sticky top-0 z-10">
            <tr>
              <th className="sticky left-0 z-20 border-r border-b bg-muted px-2 py-1" />
              {Array.from({ length: width }, (_, i) => (
                <th key={i} className="min-w-24 border-r border-b bg-muted px-2 py-1 font-medium text-muted-foreground">
                  {col(i)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {sheet.rows.map((r, ri) => (
              <tr key={ri}>
                <td className="sticky left-0 border-r border-b bg-muted px-2 py-1 text-right text-muted-foreground tabular-nums">{ri + 1}</td>
                {Array.from({ length: width }, (_, ci) => (
                  <td key={ci} className="max-w-80 truncate border-r border-b px-2 py-1 tabular-nums" title={r[ci] == null ? "" : String(r[ci])}>
                    {r[ci] == null ? "" : String(r[ci])}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
        {sheet.rows.length >= 2000 && <p className="p-3 text-xs text-muted-foreground">Showing the first 2,000 rows.</p>}
      </div>
      {sheets.length > 1 && (
        <div className="flex gap-1 overflow-x-auto border-t bg-muted/50 p-1.5">
          {sheets.map((s, i) => (
            <button
              key={s.name}
              type="button"
              onClick={() => setActive(i)}
              className={cn("shrink-0 rounded-md px-3 py-1 text-xs font-medium", i === active ? "bg-background shadow-xs ring-1 ring-border" : "text-muted-foreground hover:bg-background/60")}
            >
              {s.name}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

type Slide = { n: number; title: string; lines: string[]; image?: string }

/** A light PowerPoint reader: slide text and the first picture per slide. */
export function PptxPreview({ src }: { src: string }) {
  const { buf, error } = useBuffer(src)
  const [slides, setSlides] = useState<Slide[] | null>(null)
  const [thumb, setThumb] = useState<string>()
  const [failed, setFailed] = useState("")
  useEffect(() => {
    if (!buf) return
    const urls: string[] = []
    import("jszip").then(async ({ default: JSZip }) => {
      try {
        const zip = await JSZip.loadAsync(buf)
        const t = zip.file("docProps/thumbnail.jpeg")
        if (t) {
          const u = URL.createObjectURL(await t.async("blob"))
          urls.push(u)
          setThumb(u)
        }
        const names = Object.keys(zip.files)
          .filter((n) => /^ppt\/slides\/slide\d+\.xml$/.test(n))
          .sort((a, b) => Number(a.match(/\d+/)![0]) - Number(b.match(/\d+/)![0]))
        const parser = new DOMParser()
        const out: Slide[] = []
        for (const name of names.slice(0, 200)) {
          const doc = parser.parseFromString(await zip.file(name)!.async("text"), "application/xml")
          const paras = Array.from(doc.getElementsByTagName("a:p"))
            .map((p) => Array.from(p.getElementsByTagName("a:t")).map((t) => t.textContent ?? "").join(""))
            .filter((s) => s.trim())
          const n = Number(name.match(/\d+/)![0])
          let image: string | undefined
          const rels = zip.file(`ppt/slides/_rels/slide${n}.xml.rels`)
          if (rels) {
            const rdoc = parser.parseFromString(await rels.async("text"), "application/xml")
            const rel = Array.from(rdoc.getElementsByTagName("Relationship")).find((r) => /\/image$/.test(r.getAttribute("Type") ?? ""))
            const target = rel?.getAttribute("Target")?.replace(/^\.\.\//, "ppt/")
            const file = target && zip.file(target)
            if (file && /\.(png|jpe?g|gif|webp)$/i.test(target!)) {
              image = URL.createObjectURL(await file.async("blob"))
              urls.push(image)
            }
          }
          out.push({ n, title: paras[0] ?? `Slide ${n}`, lines: paras.slice(1, 12), image })
        }
        setSlides(out)
      } catch {
        setFailed("This presentation could not be read. Download it to open it in PowerPoint or Keynote.")
      }
    })
    return () => urls.forEach((u) => URL.revokeObjectURL(u))
  }, [buf])
  if (error || failed) return <Failed message={error || failed} />
  if (!slides)
    return (
      <div className="flex h-full items-center justify-center">
        <Spinner />
      </div>
    )
  return (
    <div className="size-full overflow-auto bg-muted/60 p-4">
      <div className="mx-auto grid max-w-4xl gap-4">
        {thumb && slides.length === 0 && <img src={thumb} alt="" className="rounded-lg shadow" />}
        {slides.map((s) => (
          <div key={s.n} className="relative flex aspect-video overflow-hidden rounded-lg border bg-card p-6 shadow-sm">
            <span className="absolute top-2 right-3 text-xs text-muted-foreground">{s.n}</span>
            <div className={cn("flex min-w-0 flex-col gap-2", s.image ? "w-1/2 pr-4" : "w-full")}>
              <h3 className="text-lg font-semibold">{s.title}</h3>
              <ul className="space-y-1 overflow-hidden text-sm text-muted-foreground">
                {s.lines.map((l, i) => (
                  <li key={i} className="truncate">
                    {l}
                  </li>
                ))}
              </ul>
            </div>
            {s.image && <img src={s.image} alt="" className="w-1/2 rounded object-contain" />}
          </div>
        ))}
        <p className="text-center text-xs text-muted-foreground">A simplified preview. Download for the full presentation.</p>
      </div>
    </div>
  )
}

/** Google Drive shortcut files (.gdoc/.gsheet/.gslides) are JSON pointers. */
export function GooglePreview({ src, name }: { src: string; name: string }) {
  const [url, setUrl] = useState<string | null>(null)
  const [error, setError] = useState(false)
  useEffect(() => {
    fetch(src, { credentials: "same-origin" })
      .then((r) => r.json())
      .then((j) => {
        const u = String(j.url ?? (j.doc_id ? `https://docs.google.com/open?id=${encodeURIComponent(j.doc_id)}` : ""))
        if (/^https:\/\/(docs|drive)\.google\.com\//.test(u)) setUrl(u)
        else setError(true)
      })
      .catch(() => setError(true))
  }, [src])
  return (
    <div className="flex flex-col items-center gap-4 p-10 text-center">
      <p className="max-w-sm text-sm text-muted-foreground">
        “{name}” is a shortcut to a Google file. Its content lives in Google Drive.
      </p>
      {url ? (
        <Button asChild>
          <a href={url} target="_blank" rel="noopener noreferrer">
            <ExternalLinkIcon /> Open in Google
          </a>
        </Button>
      ) : error ? (
        <p className="text-sm text-muted-foreground">No Google link found in this file.</p>
      ) : (
        <Spinner />
      )}
    </div>
  )
}

/** Markdown, rendered and sanitised. */
export function MarkdownView({ text, className }: { text: string; className?: string }) {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!ref.current) return
    // No forms: a document could otherwise post to our own API when clicked.
    // No styles or controls either: a document shouldn't be able to restyle
    // the app around it or draw something that looks like one of its prompts.
    const html = DOMPurify.sanitize(marked.parse(text, { async: false, gfm: true }) as string, {
      USE_PROFILES: { html: true },
      FORBID_TAGS: ["form", "style", "input", "button", "textarea", "select"],
      FORBID_ATTR: ["style"],
    })
    ref.current.innerHTML = html
    tameLinks(ref.current)
  }, [text])
  return <div ref={ref} className={cn("markdown", className)} />
}

/** A CSV/TSV grid preview from text (used by the editor). */
export function CsvTable({ text }: { text: string }) {
  const [rows, setRows] = useState<(string | null)[][]>([])
  useEffect(() => {
    import("xlsx").then((XLSX) => {
      try {
        const wb = XLSX.read(text.slice(0, 2_000_000), { type: "string", sheetRows: 1001, raw: true })
        const ws = wb.Sheets[wb.SheetNames[0]]
        setRows((XLSX.utils.sheet_to_json(ws, { header: 1, raw: false, defval: null }) as (string | null)[][]).slice(0, 1000))
      } catch {
        setRows([])
      }
    })
  }, [text])
  const width = Math.max(0, ...rows.map((r) => r.length))
  return (
    <div className="size-full overflow-auto">
      <table className="border-separate border-spacing-0 text-xs">
        <tbody>
          {rows.map((r, ri) => (
            <tr key={ri} className={ri === 0 ? "font-semibold" : undefined}>
              <td className="sticky left-0 border-r border-b bg-muted px-2 py-1 text-right text-muted-foreground">{ri + 1}</td>
              {Array.from({ length: width }, (_, ci) => (
                <td key={ci} className="max-w-64 truncate border-r border-b px-2 py-1">
                  {r[ci] ?? ""}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
