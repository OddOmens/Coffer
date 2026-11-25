import { useCallback, useEffect, useRef, useState, type MouseEvent, type ReactNode, type Ref } from "react"
import { DownloadIcon, ImageOffIcon, VideoOffIcon } from "lucide-react"

import { Spinner } from "@/components/states"
import { Button } from "@/components/ui/button"
import { mediaState, type MediaState } from "@/lib/api"
import { extOf } from "@/lib/format"
import { cn } from "@/lib/utils"

// Mirrors backend files/media.go: which files get generated renditions.
const STILL_EXTS = new Set([
  "heic", "heif", "avif", "tif", "tiff", "bmp", "psd",
  "dng", "cr2", "cr3", "nef", "arw", "raf", "orf", "rw2", "pef", "srw", "3fr", "raw",
])
const VIDEO_EXTS = new Set(["mov", "mkv", "avi", "mxf", "mts", "m2ts", "wmv", "flv", "mpg", "mpeg", "3gp", "mp4", "webm", "m4v"])
const BROWSER_VIDEO = new Set(["mp4", "webm", "m4v"])
const BROWSER_IMAGE = new Set(["jpg", "jpeg", "png", "gif", "webp", "svg"])
const AUDIO_EXTS = new Set(["mp3", "wav", "flac", "m4a", "aac", "ogg"])

export const isVideo = (name: string) => VIDEO_EXTS.has(extOf(name))
/** Photos shown from a generated JPEG (HEIC, RAW, TIFF, PSD…). */
export const needsStill = (name: string) => STILL_EXTS.has(extOf(name))
export const browserPlayable = (name: string) => BROWSER_VIDEO.has(extOf(name))

export type MediaKind = "video" | "image" | "audio"

/** What MediaPreview can show for a file name, or null. */
export function mediaKindOf(name: string): MediaKind | null {
  const x = extOf(name)
  if (VIDEO_EXTS.has(x)) return "video"
  if (BROWSER_IMAGE.has(x) || STILL_EXTS.has(x)) return "image"
  if (AUDIO_EXTS.has(x)) return "audio"
  return null
}

export type Marker = { id: number | string; x: number; y: number; label?: string; active?: boolean }

export type MediaPreviewProps = {
  name: string
  /** The original file, inline. */
  src: string
  /** H.264 rendition (videos). */
  proxySrc?: string
  /** JPEG rendition (HEIC/RAW photos; video poster). */
  stillSrc?: string
  downloadHref?: string
  /** Text repeated over videos (images are watermarked by the server). */
  watermark?: string
  autoPlay?: boolean
  videoRef?: Ref<HTMLVideoElement>
  /** Numbered dots over images, at x/y between 0 and 1. */
  markers?: Marker[]
  onMarkerClick?: (m: Marker) => void
  /** Click on an image: where, from 0 to 1. Turns on the crosshair cursor. */
  onImageClick?: (x: number, y: number) => void
  className?: string
}

function setRef<T>(ref: Ref<T> | undefined, v: T | null) {
  if (!ref) return
  if (typeof ref === "function") ref(v)
  else (ref as { current: T | null }).current = v
}

/** Polls a rendition until it is ready (or can't be made). */
export function useRendition(url: string | undefined, enabled: boolean) {
  const [state, setState] = useState<MediaState | null>(null)
  useEffect(() => {
    if (!url || !enabled) return
    let stop = false
    let timer: ReturnType<typeof setTimeout>
    const tick = async () => {
      const s = await mediaState(url).catch(() => ({ state: "failed" as const }))
      if (stop) return
      setState(s)
      if (s.state === "queued" || s.state === "running") timer = setTimeout(tick, 3000)
    }
    setState(null)
    tick()
    return () => {
      stop = true
      clearTimeout(timer)
    }
  }, [url, enabled])
  return state
}

function Pending({ state, what, downloadHref, icon }: { state: MediaState | null; what: string; downloadHref?: string; icon: ReactNode }) {
  const busy = !state || state.state === "queued" || state.state === "running"
  return (
    <div className="flex max-w-sm flex-col items-center gap-3 p-8 text-center text-sm">
      {busy ? (
        <>
          <Spinner className="size-5 text-muted-foreground" />
          <p className="font-medium">Preparing a {what}…</p>
          <p className="text-xs text-muted-foreground">This file can't be played in a browser as it is, so a copy is being made. It appears here when it's ready.</p>
        </>
      ) : (
        <>
          <div className="flex size-10 items-center justify-center rounded-lg border bg-secondary text-muted-foreground [&_svg]:size-5">{icon}</div>
          <p className="font-medium">{state?.state === "unavailable" ? `No ${what} available` : `The ${what} couldn't be made`}</p>
          <p className="text-xs text-muted-foreground">
            {state?.state === "unavailable" ? "Previews for this kind of file aren't set up on this server." : "Download the file to open it."}
          </p>
        </>
      )}
      {!busy && downloadHref && (
        <Button size="sm" variant="outline" asChild>
          <a href={downloadHref} download>
            <DownloadIcon /> Download
          </a>
        </Button>
      )}
    </div>
  )
}

/** Faint repeated text over a video or document, to discourage screen recordings. */
export function WatermarkOverlay({ text, onLight }: { text: string; onLight?: boolean }) {
  return (
    <div aria-hidden className="pointer-events-none absolute inset-0 overflow-hidden select-none">
      <div className="absolute -inset-1/2 flex rotate-[-24deg] flex-wrap content-center items-center justify-center gap-x-16 gap-y-14">
        {Array.from({ length: 60 }).map((_, i) => (
          <span
            key={i}
            className={cn(
              "text-sm font-medium whitespace-nowrap",
              onLight ? "text-neutral-500/30" : "text-white/25 [text-shadow:0_0_2px_rgba(0,0,0,.35)]",
            )}
          >
            {text}
          </span>
        ))}
      </div>
    </div>
  )
}

export function MediaPreview(props: MediaPreviewProps) {
  const kind = mediaKindOf(props.name)
  if (kind === "video") return <VideoView {...props} />
  if (kind === "image") return <ImageView {...props} />
  if (kind === "audio") return <audio src={props.src} controls autoPlay={props.autoPlay} className={cn("w-full max-w-lg", props.className)} />
  return null
}

function VideoView({ name, src, proxySrc, stillSrc, downloadHref, watermark, autoPlay = true, videoRef, className }: MediaPreviewProps) {
  // Browser formats play directly; others (and originals that fail, such as
  // HEVC or ProRes in .mp4) use the H.264 copy.
  const [useProxy, setUseProxy] = useState(!browserPlayable(name) && !!proxySrc)
  const [error, setError] = useState(false)
  const rendition = useRendition(proxySrc, useProxy)
  useEffect(() => {
    setUseProxy(!browserPlayable(name) && !!proxySrc)
    setError(false)
  }, [name, src, proxySrc])
  const local = useRef<HTMLVideoElement | null>(null)
  const ref = useCallback(
    (el: HTMLVideoElement | null) => {
      local.current = el
      setRef(videoRef, el)
    },
    [videoRef],
  )

  if (error && !proxySrc) return <Pending state={{ state: "failed" }} what="preview" downloadHref={downloadHref} icon={<VideoOffIcon />} />
  if (useProxy && rendition?.state !== "ready") return <Pending state={rendition} what="preview" downloadHref={downloadHref} icon={<VideoOffIcon />} />
  const url = useProxy ? proxySrc! : src
  return (
    <div className={cn("relative flex size-full items-center justify-center overflow-hidden", className)}>
      <video
        key={url}
        ref={ref}
        src={url}
        poster={useProxy ? stillSrc : undefined}
        controls
        autoPlay={autoPlay}
        playsInline
        controlsList={watermark ? "nodownload" : undefined}
        onContextMenu={watermark ? (e) => e.preventDefault() : undefined}
        onError={() => {
          if (!useProxy && proxySrc) setUseProxy(true)
          else setError(true)
        }}
        className="max-h-full max-w-full bg-black"
      />
      {watermark && <WatermarkOverlay text={watermark} />}
    </div>
  )
}

/** Where an object-contain image actually sits inside its box. */
function useImageRect(img: HTMLImageElement | null) {
  const [rect, setRect] = useState<{ left: number; top: number; width: number; height: number } | null>(null)
  useEffect(() => {
    if (!img) return
    const measure = () => {
      const bw = img.clientWidth, bh = img.clientHeight
      const nw = img.naturalWidth, nh = img.naturalHeight
      if (!nw || !nh || !bw || !bh) return
      const scale = Math.min(bw / nw, bh / nh, 1)
      const width = nw * scale, height = nh * scale
      setRect({ left: (bw - width) / 2, top: (bh - height) / 2, width, height })
    }
    measure()
    img.addEventListener("load", measure)
    const ro = new ResizeObserver(measure)
    ro.observe(img)
    return () => {
      img.removeEventListener("load", measure)
      ro.disconnect()
    }
  }, [img])
  return rect
}

function ImageView({ name, src, stillSrc, downloadHref, markers, onMarkerClick, onImageClick, className }: MediaPreviewProps) {
  const still = needsStill(name)
  const rendition = useRendition(stillSrc, still && !!stillSrc)
  const [failed, setFailed] = useState(false)
  const [img, setImg] = useState<HTMLImageElement | null>(null)
  const rect = useImageRect(img)
  useEffect(() => setFailed(false), [src])
  if (still && !stillSrc) return <Pending state={{ state: "unavailable" }} what="preview" downloadHref={downloadHref} icon={<ImageOffIcon />} />
  if (still && rendition?.state !== "ready") return <Pending state={rendition} what="preview" downloadHref={downloadHref} icon={<ImageOffIcon />} />
  if (failed) return <Pending state={{ state: "failed" }} what="preview" downloadHref={downloadHref} icon={<ImageOffIcon />} />
  const click = (ev: MouseEvent<HTMLImageElement>) => {
    if (!onImageClick || !rect) return
    const r = ev.currentTarget.getBoundingClientRect()
    const x = (ev.clientX - r.left - rect.left) / rect.width
    const y = (ev.clientY - r.top - rect.top) / rect.height
    if (x < 0 || x > 1 || y < 0 || y > 1) return
    onImageClick(x, y)
  }
  return (
    <div className={cn("relative size-full", className)}>
      <img
        ref={setImg}
        src={still ? stillSrc : src}
        alt={name}
        draggable={false}
        onError={() => setFailed(true)}
        onClick={click}
        className={cn("absolute inset-0 size-full object-contain select-none", onImageClick && "cursor-crosshair")}
      />
      {rect &&
        markers?.map((m) => (
          <button
            key={m.id}
            type="button"
            onClick={() => onMarkerClick?.(m)}
            style={{ left: rect.left + m.x * rect.width, top: rect.top + m.y * rect.height }}
            className={cn(
              "absolute flex size-6 -translate-x-1/2 -translate-y-1/2 items-center justify-center rounded-full border-2 border-white text-[0.6875rem] font-semibold shadow-md transition-transform",
              m.active ? "z-10 scale-125 bg-brand text-brand-foreground" : "bg-amber-500 text-white",
            )}
            aria-label={`Comment ${m.label ?? ""}`}
          >
            {m.label}
          </button>
        ))}
    </div>
  )
}
