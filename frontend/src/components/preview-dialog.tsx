import { lazy, Suspense, useEffect, useRef, useState, type TouchEvent } from "react"
import type { QueryKey } from "@tanstack/react-query"
import { ChevronLeftIcon, ChevronRightIcon, DownloadIcon, ExternalLinkIcon, MessageSquareIcon, PencilIcon } from "lucide-react"

import { FileTile } from "@/components/file-icon"
import { MediaPreview, mediaKindOf } from "@/components/media-preview"
import { markersFrom, ReviewPanel, useComments, type ReviewAdapter } from "@/components/review-panel"
import { Spinner } from "@/components/states"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import type { Entry } from "@/lib/api"
import { standalone } from "@/lib/standalone"
import { cn } from "@/lib/utils"
import { describeEntry, extOf, formatDate, isEditable, previewKind } from "@/lib/format"

const Office = {
  Docx: lazy(() => import("@/components/office-preview").then((m) => ({ default: m.DocxPreview }))),
  Sheet: lazy(() => import("@/components/office-preview").then((m) => ({ default: m.SheetPreview }))),
  Pptx: lazy(() => import("@/components/office-preview").then((m) => ({ default: m.PptxPreview }))),
  Google: lazy(() => import("@/components/office-preview").then((m) => ({ default: m.GooglePreview }))),
  Markdown: lazy(() => import("@/components/office-preview").then((m) => ({ default: m.MarkdownView }))),
}

type Props = {
  entries: Entry[]
  index: number | null
  onIndexChange: (i: number | null) => void
  downloadUrl: (path: string, inline?: boolean) => string
  canDownload?: boolean
  canView?: boolean
  onEdit?: (e: Entry) => void
  /** Renditions from the media worker (video proxies, HEIC/RAW stills). */
  mediaUrl?: (e: Entry, kind: "proxy" | "still") => string
  /** Comments and approvals beside the preview. */
  review?: { adapter: (e: Entry) => ReviewAdapter; queryKey: (e: Entry) => QueryKey; allowInternal?: boolean }
  /** Open with the comments panel showing. */
  showReview?: boolean
}

const REVIEW_KEY = "coffer.preview.review"

export function PreviewDialog({ entries, index, onIndexChange, downloadUrl, canDownload = true, canView = true, onEdit, mediaUrl, review, showReview }: Props) {
  const entry = index !== null ? entries[index] : undefined
  const [panel, setPanel] = useState(() => {
    try {
      return localStorage.getItem(REVIEW_KEY) === "1"
    } catch {
      return false
    }
  })
  useEffect(() => {
    if (showReview) setPanel(true)
  }, [showReview])
  const togglePanel = () => {
    setPanel(!panel)
    try {
      localStorage.setItem(REVIEW_KEY, panel ? "0" : "1")
    } catch {
      /* private mode */
    }
  }
  const reviewing = !!review && panel
  const swipe = useSwipe(
    () => index !== null && index < entries.length - 1 && onIndexChange(index + 1),
    () => index !== null && index > 0 && onIndexChange(index - 1),
  )

  useEffect(() => {
    if (index === null) return
    const onKey = (ev: KeyboardEvent) => {
      if ((ev.target as HTMLElement).closest("input, textarea, [contenteditable]")) return
      if (ev.key === "ArrowRight" && index < entries.length - 1) onIndexChange(index + 1)
      if (ev.key === "ArrowLeft" && index > 0) onIndexChange(index - 1)
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [index, entries.length, onIndexChange])

  return (
    <Dialog open={!!entry} onOpenChange={(o) => !o && onIndexChange(null)}>
      <DialogContent mobile="full" className="flex max-h-(--h) w-[min(84rem,calc(100vw-1rem))] max-w-none flex-col gap-3 p-3 [--h:min(96svh,calc(100svh-1rem-env(safe-area-inset-top)-env(safe-area-inset-bottom)))] sm:max-h-(--h) sm:w-[min(84rem,calc(100vw-1rem-env(safe-area-inset-left)-env(safe-area-inset-right)))] sm:max-w-none sm:p-4">
        {entry && (
          <>
            <div className="flex items-center gap-3 pr-8">
              <FileTile entry={entry} size="sm" />
              <div className="min-w-0 flex-1">
                <DialogTitle className="truncate text-sm">{entry.name}</DialogTitle>
                <DialogDescription className="truncate text-xs">
                  {describeEntry(entry)}
                  <span className="max-sm:hidden"> · {formatDate(entry.modTime)}</span>
                </DialogDescription>
              </div>
              {review && (
                <Button variant={panel ? "secondary" : "outline"} size="sm" onClick={togglePanel} aria-pressed={panel}>
                  <MessageSquareIcon /> <span className="hidden sm:inline">Comments</span>
                  {(entry.review?.comments ?? 0) > 0 && <span className="tabular-nums">{entry.review!.comments}</span>}
                </Button>
              )}
              {onEdit && isEditable(entry) && (
                <Button variant="outline" size="sm" onClick={() => onEdit(entry)} aria-label="Edit">
                  <PencilIcon /> <span className="hidden sm:inline">Edit</span>
                </Button>
              )}
              {/* Not from the Home Screen: there are no tabs there, and the bare file would replace the app with no way back. */}
              {!standalone && canView && previewKind(entry) && !["docx", "sheet", "pptx", "google"].includes(previewKind(entry)!) && (
                <Button variant="ghost" size="icon-sm" asChild aria-label="Open in new tab">
                  <a href={downloadUrl(entry.path, true)} target="_blank" rel="noopener noreferrer">
                    <ExternalLinkIcon />
                  </a>
                </Button>
              )}
              {canDownload && (
                <Button size="sm" asChild>
                  <a href={downloadUrl(entry.path)} download>
                    <DownloadIcon /> <span className="hidden sm:inline">Download</span>
                  </a>
                </Button>
              )}
            </div>
            {reviewing ? (
              <Reviewable key={entry.path} entry={entry} src={downloadUrl(entry.path, true)} mediaUrl={mediaUrl} review={review!} downloadUrl={downloadUrl}>
                {index! > 0 && (
                  <Button variant="secondary" size="icon" className="absolute left-2 z-10 rounded-full shadow" onClick={() => onIndexChange(index! - 1)} aria-label="Previous">
                    <ChevronLeftIcon />
                  </Button>
                )}
                {index! < entries.length - 1 && (
                  <Button variant="secondary" size="icon" className="absolute right-2 z-10 rounded-full shadow" onClick={() => onIndexChange(index! + 1)} aria-label="Next">
                    <ChevronRightIcon />
                  </Button>
                )}
              </Reviewable>
            ) : (
            <div
              {...swipe}
              className="relative flex h-[calc(var(--h)-6rem)] min-h-64 items-center justify-center overflow-hidden rounded-xl bg-muted/50 max-sm:h-auto max-sm:min-h-0 max-sm:flex-1"
            >
              {canView ? (
                <Suspense fallback={<Spinner />}>
                  <PreviewBody key={entry.path} entry={entry} src={downloadUrl(entry.path, true)} mediaUrl={mediaUrl} downloadHref={canDownload ? downloadUrl(entry.path) : undefined} />
                </Suspense>
              ) : (
                <NoPreview entry={entry} text="Previews are turned off for this link." />
              )}
              {index! > 0 && (
                <Button variant="secondary" size="icon" className="absolute left-2 rounded-full shadow" onClick={() => onIndexChange(index! - 1)} aria-label="Previous">
                  <ChevronLeftIcon />
                </Button>
              )}
              {index! < entries.length - 1 && (
                <Button variant="secondary" size="icon" className="absolute right-2 rounded-full shadow" onClick={() => onIndexChange(index! + 1)} aria-label="Next">
                  <ChevronRightIcon />
                </Button>
              )}
            </div>
            )}
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}

/** Swipe left or right with one finger to page between files, as in Photos. */
function useSwipe(onNext: () => void, onPrev: () => void) {
  const start = useRef<{ x: number; y: number } | null>(null)
  return {
    onTouchStart: (ev: TouchEvent) => {
      // Leave media controls, fields and pinch-zoom alone.
      const busy = ev.touches.length > 1 || (ev.target as HTMLElement).closest("video, audio, input, textarea, [contenteditable], [data-no-swipe]")
      start.current = busy ? null : { x: ev.touches[0].clientX, y: ev.touches[0].clientY }
    },
    onTouchMove: (ev: TouchEvent) => {
      if (ev.touches.length > 1) start.current = null
    },
    onTouchEnd: (ev: TouchEvent) => {
      const s = start.current
      start.current = null
      if (!s) return
      const dx = ev.changedTouches[0].clientX - s.x
      const dy = ev.changedTouches[0].clientY - s.y
      if (Math.abs(dx) < 60 || Math.abs(dx) < Math.abs(dy) * 2) return
      if (dx < 0) onNext()
      else onPrev()
    },
  }
}

function NoPreview({ entry, text }: { entry: Entry; text: string }) {
  return (
    <div className="flex flex-col items-center gap-3 p-10 text-center">
      <FileTile entry={entry} size="lg" />
      <p className="max-w-sm text-sm text-muted-foreground">{text}</p>
    </div>
  )
}

/** The preview with the comments panel beside it (below on small screens). */
function Reviewable({
  entry,
  src,
  mediaUrl,
  review,
  downloadUrl,
  children,
}: {
  entry: Entry
  src: string
  mediaUrl?: Props["mediaUrl"]
  review: NonNullable<Props["review"]>
  downloadUrl: Props["downloadUrl"]
  children?: React.ReactNode
}) {
  const video = useRef<HTMLVideoElement>(null)
  const adapter = review.adapter(entry)
  const queryKey = review.queryKey(entry)
  const comments = useComments(adapter, queryKey)
  const [pin, setPin] = useState<{ x: number; y: number } | null>(null)
  const [active, setActive] = useState<number | null>(null)
  const kind = mediaKindOf(entry.name)
  return (
    <div className="grid h-[calc(var(--h)-6rem)] min-h-64 gap-3 max-sm:h-auto max-sm:min-h-0 max-sm:flex-1 max-lg:grid-rows-[minmax(0,1fr)_minmax(0,45%)] lg:grid-cols-[minmax(0,1fr)_22rem]">
      <div className="relative flex min-h-0 items-center justify-center overflow-hidden rounded-xl bg-muted/50">
        {kind === "video" || kind === "image" ? (
          <MediaPreview
            name={entry.name}
            src={src}
            proxySrc={mediaUrl?.(entry, "proxy")}
            stillSrc={mediaUrl?.(entry, "still")}
            downloadHref={downloadUrl(entry.path)}
            videoRef={video}
            markers={kind === "image" ? markersFrom(comments.data, active) : undefined}
            onMarkerClick={(m) => setActive(Number(m.id))}
            onImageClick={kind === "image" ? (x, y) => setPin({ x, y }) : undefined}
          />
        ) : (
          <Suspense fallback={<Spinner />}>
            <PreviewBody entry={entry} src={src} mediaUrl={mediaUrl} />
          </Suspense>
        )}
        {children}
      </div>
      <ReviewPanel
        adapter={adapter}
        queryKey={queryKey}
        allowInternal={review.allowInternal}
        getTime={kind === "video" ? () => video.current?.currentTime ?? null : undefined}
        onCompose={() => video.current?.pause()}
        onSeek={(t) => {
          if (video.current) video.current.currentTime = t
        }}
        pin={pin}
        onClearPin={() => setPin(null)}
        pinnable={kind === "image"}
        activeId={active}
        onActiveChange={setActive}
        className={cn("min-h-0 rounded-xl border bg-card")}
      />
    </div>
  )
}

function PreviewBody({ entry, src, mediaUrl, downloadHref }: { entry: Entry; src: string; mediaUrl?: Props["mediaUrl"]; downloadHref?: string }) {
  const kind = previewKind(entry)
  if ((kind === "image" || kind === "video") && mediaUrl)
    return (
      <MediaPreview name={entry.name} src={src} proxySrc={mediaUrl(entry, "proxy")} stillSrc={mediaUrl(entry, "still")} downloadHref={downloadHref} autoPlay />
    )
  switch (kind) {
    case "image":
      return <img src={src} alt={entry.name} className="max-h-full max-w-full object-contain" />
    case "video":
      return <video src={src} controls autoPlay playsInline className="max-h-full max-w-full bg-black" />
    case "audio":
      return <audio src={src} controls autoPlay className="w-full max-w-lg" />
    case "pdf":
      return <iframe src={src} title={entry.name} className="size-full border-0 bg-white" />
    case "docx":
      return <Office.Docx src={src} />
    case "sheet":
      return <Office.Sheet src={src} />
    case "pptx":
      return <Office.Pptx src={src} />
    case "google":
      return <Office.Google src={src} name={entry.name} />
    case "text":
      return <TextPreview src={src} markdown={["md", "markdown"].includes(extOf(entry.name))} />
    default: {
      const x = extOf(entry.name)
      const hint =
        x === "aep" || x === "prproj"
          ? "Open this project in After Effects or Premiere from the mounted drive."
          : x === "doc" || x === "xls" || x === "ppt"
            ? "Older Office formats can't be previewed. Download it to open it."
            : "No preview for this file type. Download it to open it."
      return <NoPreview entry={entry} text={hint} />
    }
  }
}

const TEXT_LIMIT = 512 * 1024

function TextPreview({ src, markdown }: { src: string; markdown: boolean }) {
  const [text, setText] = useState<string | null>(null)
  const [error, setError] = useState(false)
  const [mode, setMode] = useState(markdown ? "rendered" : "raw")
  useEffect(() => {
    const ctl = new AbortController()
    fetch(src, { headers: { Range: `bytes=0-${TEXT_LIMIT - 1}` }, signal: ctl.signal, credentials: "same-origin" })
      .then((r) => (r.ok ? r.text() : Promise.reject()))
      .then(setText)
      .catch(() => !ctl.signal.aborted && setError(true))
    return () => ctl.abort()
  }, [src])
  if (error) return <p className="text-sm text-muted-foreground">Could not load this file.</p>
  if (text === null) return <Spinner />
  return (
    <div className="relative size-full overflow-auto bg-background">
      {markdown && (
        <ToggleGroup type="single" size="sm" variant="outline" value={mode} onValueChange={(v) => v && setMode(v)} className="absolute top-3 right-3 z-10 bg-background">
          <ToggleGroupItem value="rendered">Preview</ToggleGroupItem>
          <ToggleGroupItem value="raw">Source</ToggleGroupItem>
        </ToggleGroup>
      )}
      {mode === "rendered" ? (
        <Office.Markdown text={text} className="mx-auto max-w-3xl p-8" />
      ) : (
        <pre className="p-5 font-mono text-xs leading-relaxed whitespace-pre-wrap">
          {text}
          {text.length >= TEXT_LIMIT && "\n\n… (preview truncated, download to see everything)"}
        </pre>
      )}
    </div>
  )
}
