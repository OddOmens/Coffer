import {
  lazy,
  Suspense,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type DragEvent,
  type FormEvent,
  type PointerEvent as ReactPointerEvent,
  type ReactNode,
} from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import {
  BanIcon,
  CheckCircle2Icon,
  ChevronLeftIcon,
  ChevronRightIcon,
  ClockIcon,
  DownloadIcon,
  ExternalLinkIcon,
  FolderIcon,
    HistoryIcon,
  ImagesIcon,
  LayoutGridIcon,
  ListIcon,
  LockIcon,
  MailIcon,
  MessageSquareIcon,
  PauseIcon,
  PlayIcon,
  ShieldAlertIcon,
  TriangleAlertIcon,
  UploadCloudIcon,
  XIcon,
  type LucideIcon,
} from "lucide-react"
import { useLocation, useNavigate, useParams } from "react-router"
import { toast } from "sonner"

import { Brand } from "@/components/brand"
import { FileTile } from "@/components/file-icon"
import { FileList, sortEntries, useSelection, type Sort } from "@/components/file-list"
import { isVideo, MediaPreview, mediaKindOf, needsStill, useRendition, WatermarkOverlay } from "@/components/media-preview"
import { PathBreadcrumbs } from "@/components/path-breadcrumbs"
import { markersFrom, ReviewPanel, useComments, type ReviewAdapter } from "@/components/review-panel"
import { CenteredSpinner, EmptyState, Spinner } from "@/components/states"
import { UploadPanel } from "@/components/upload-panel"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { InputOTP, InputOTPGroup, InputOTPSlot } from "@/components/ui/input-otp"
import { Skeleton } from "@/components/ui/skeleton"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { ApiError, shareApi, type Entry, type PublicShare, type ReviewSummary, type ShareEntry, type ViewMode } from "@/lib/api"
import { describeEntry, encodePath, extOf, fileCount, formatBytes, formatDate, formatDay, previewKind } from "@/lib/format"
import { enqueue, filesFromDrop, filesFromInput, useUploads, type QueueFile } from "@/lib/upload"
import { cn } from "@/lib/utils"

const Office = {
  Docx: lazy(() => import("@/components/office-preview").then((m) => ({ default: m.DocxPreview }))),
  Sheet: lazy(() => import("@/components/office-preview").then((m) => ({ default: m.SheetPreview }))),
  Pptx: lazy(() => import("@/components/office-preview").then((m) => ({ default: m.PptxPreview }))),
  Google: lazy(() => import("@/components/office-preview").then((m) => ({ default: m.GooglePreview }))),
  Markdown: lazy(() => import("@/components/office-preview").then((m) => ({ default: m.MarkdownView }))),
}
type SApi = ReturnType<typeof shareApi>
type Ctx = { token: string; share: PublicShare; sapi: SApi }

const metaKey = (token: string) => ["share", token]

/** What can be shown for a file on a link: media, documents or nothing. */
function canPreview(e: Pick<Entry, "name" | "isDir">) {
  return !e.isDir && (!!mediaKindOf(e.name) || !!previewKind(e))
}

function watermarkText(share: PublicShare) {
  return `${share.viewer?.email ?? "Preview"} · ${new Date().toISOString().slice(0, 10)}`
}

function TileReviewBadge({ review, className }: { review?: ReviewSummary; className?: string }) {
  if (!review || (!review.status && !review.comments)) return null
  return (
    <span className={cn("inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[0.6875rem] font-medium shadow-xs", className, review.status === "approved" ? "bg-emerald-600 text-white" : review.status === "changes" ? "bg-amber-500 text-white" : "bg-background/90 text-foreground")}>
      {review.status === "approved" ? <CheckCircle2Icon className="size-3" /> : review.status === "changes" ? <TriangleAlertIcon className="size-3" /> : <MessageSquareIcon className="size-3" />}
      {review.status === "approved" ? "Approved" : review.status === "changes" ? "Changes" : review.comments}
    </span>
  )
}

// Page frame.

function Shell({ ctx, children }: { ctx?: Ctx; children: ReactNode }) {
  const qc = useQueryClient()
  const share = ctx?.share
  const viewer = share?.viewer
  const forget = async () => {
    await ctx!.sapi.forget().catch(() => {})
    await qc.invalidateQueries({ queryKey: metaKey(ctx!.token) })
  }
  return (
    <div className="flex min-h-svh flex-col bg-canvas">
      <header className="box-content flex h-14 items-center gap-3 border-b bg-background px-4 pt-[env(safe-area-inset-top)] md:px-8">
        <Brand small />
        <div className="flex-1" />
        {viewer && !viewer.revoked && (
          <div className="flex min-w-0 items-center gap-2 text-xs text-muted-foreground">
            <MailIcon className="hidden size-3.5 shrink-0 sm:block" />
            <span className="truncate">
              <span className="hidden sm:inline">Signed in as </span>
              <span className="font-medium text-foreground">{viewer.email}</span>
            </span>
            <button type="button" onClick={forget} className="shrink-0 underline-offset-2 hover:text-foreground hover:underline">
              Not you?
            </button>
          </div>
        )}
      </header>
      <main className="flex flex-1 flex-col">{children}</main>
      <footer className="flex flex-col items-center gap-1 px-4 py-6 text-center text-xs text-muted-foreground">
        {share?.footer && <p className="max-w-xl whitespace-pre-line">{share.footer}</p>}
        {share?.website && (
          <a href={share.website} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 font-medium hover:text-foreground">
            {hostOf(share.website)} <ExternalLinkIcon className="size-3" />
          </a>
        )}
        {!share?.hidePowered && <p>Shared securely with Coffer</p>}
      </footer>
      <UploadPanel />
    </div>
  )
}

function hostOf(url: string) {
  try {
    return new URL(url).host
  } catch {
    return url
  }
}

function Centered({ children }: { children: ReactNode }) {
  return <div className="flex flex-1 items-center justify-center p-4 md:p-6">{children}</div>
}

export default function PublicSharePage() {
  const { token = "", "*": rest = "" } = useParams()
  const location = useLocation()
  const navigate = useNavigate()
  const sapi = useMemo(() => shareApi(token), [token])
  const invite = new URLSearchParams(location.search).get("invite")
  const [inviteDone, setInviteDone] = useState(!invite)

  // Invitation emails carry a token that confirms the address; use it once
  // and drop it from the address bar.
  useEffect(() => {
    if (!invite) return
    let cancelled = false
    sapi
      .verifyInvite(invite)
      .catch((e: Error) => !cancelled && toast.error(e.message))
      .finally(() => {
        if (cancelled) return
        const sp = new URLSearchParams(location.search)
        sp.delete("invite")
        navigate({ pathname: location.pathname, search: sp.toString() ? `?${sp}` : "" }, { replace: true })
        setInviteDone(true)
      })
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [invite, sapi])

  const meta = useQuery({ queryKey: metaKey(token), queryFn: sapi.meta, retry: false, enabled: inviteDone })

  if (!inviteDone || meta.isLoading) return <CenteredSpinner />
  if (meta.error) {
    const err = meta.error as ApiError
    const map: Record<string, [LucideIcon, string, string]> = {
      share_expired: [ClockIcon, "This link has expired", "Ask the person who shared it for a new link."],
      share_paused: [PauseIcon, "This link is paused", "The owner has temporarily turned it off."],
      share_network: [ShieldAlertIcon, "Not available from your network", "This link can only be opened from specific networks."],
    }
    const [Icon, title, desc] = map[err.code] ?? [XIcon, "This link is not available", "It may have been removed, or the address is incomplete."]
    return (
      <Shell>
        <Centered>
          <EmptyState icon={<Icon />} title={title} description={desc} />
        </Centered>
      </Shell>
    )
  }
  const share = meta.data!
  const ctx: Ctx = { token, share, sapi }
  const path = ("/" + rest).replace(/\/+$/, "") || "/"
  let body: ReactNode
  if (share.gate === "share_email") body = <EmailGate ctx={ctx} />
  else if (share.gate === "share_revoked")
    body = (
      <Centered>
        <EmptyState icon={<BanIcon />} title="Your access was removed" description={`${share.from} no longer shares this with ${share.viewer?.email ?? "you"}. Ask them if you think this is a mistake.`} />
      </Centered>
    )
  else if (share.gate === "share_locked" || (share.needsPassword && !share.unlocked)) body = <PasswordGate ctx={ctx} />
  else if (!share.isDir) body = <SingleFile ctx={ctx} />
  else if (!share.canView) body = share.canUpload ? <DropBox ctx={ctx} /> : <DownloadOnly ctx={ctx} />
  else body = <FolderShare ctx={ctx} path={path} />
  return <Shell ctx={ctx}>{body}</Shell>
}

function Heading({ share, children }: { share: PublicShare; children?: ReactNode }) {
  return (
    <div className="mb-5 flex flex-wrap items-start justify-between gap-3">
      <div className="min-w-0">
        <h1 className="truncate text-xl font-semibold tracking-tight md:text-2xl">{share.title || share.name}</h1>
        <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-muted-foreground">
          {share.from && <span>Shared by {share.from}</span>}
          {share.isDir && share.canView && share.items != null && share.items > 0 && (
            <span>
              {formatBytes(share.size ?? 0)} · {fileCount(share.items)}
            </span>
          )}
          {!share.isDir && share.size != null && <span>{formatBytes(share.size)}</span>}
          {share.version != null && (
            <span className="inline-flex items-center gap-1 rounded-md bg-brand/10 px-1.5 py-0.5 text-xs font-medium text-brand">
              <HistoryIcon className="size-3" /> v{share.version} · always the latest version
            </span>
          )}
          {share.expiresAt > 0 && (
            <span className="flex items-center gap-1">
              <ClockIcon className="size-3.5" /> Available until {formatDay(share.expiresAt)}
            </span>
          )}
        </div>
      </div>
      {children}
    </div>
  )
}

function Message({ share }: { share: PublicShare }) {
  if (!share.message && !share.description) return null
  return (
    <div className="mb-5 space-y-2 rounded-xl border bg-card p-4">
      {share.description && <p className="text-sm whitespace-pre-line text-muted-foreground">{share.description}</p>}
      {share.message && (
        <Suspense fallback={null}>
          <Office.Markdown text={share.message} />
        </Suspense>
      )}
    </div>
  )
}

function GateCard({ icon, title, description, children }: { icon: ReactNode; title: string; description: ReactNode; children: ReactNode }) {
  return (
    <Centered>
      <Card className="w-full max-w-sm">
        <CardHeader>
          <div className="mb-1 flex size-10 items-center justify-center rounded-lg border bg-secondary [&_svg]:size-5">{icon}</div>
          <CardTitle className="text-base">{title}</CardTitle>
          <CardDescription>{description}</CardDescription>
        </CardHeader>
        <CardContent>{children}</CardContent>
      </Card>
    </Centered>
  )
}

function EmailGate({ ctx }: { ctx: Ctx }) {
  const { share, sapi, token } = ctx
  const qc = useQueryClient()
  const [step, setStep] = useState<"email" | "code">("email")
  const [email, setEmail] = useState("")
  const [name, setName] = useState("")
  const [code, setCode] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const [sentAt, setSentAt] = useState(0)
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (step !== "code") return
    const t = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(t)
  }, [step])
  const wait = Math.max(0, 30 - Math.floor((now - sentAt) / 1000))
  const invited = share.access === "invited"
  const title = share.title || share.name

  const send = async (ev?: FormEvent) => {
    ev?.preventDefault()
    setBusy(true)
    setError("")
    try {
      await sapi.verify(email.trim(), name.trim())
      setStep("code")
      setCode("")
      setSentAt(Date.now())
      setNow(Date.now())
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const check = async (value: string) => {
    setBusy(true)
    setError("")
    try {
      await sapi.verifyCheck(email.trim(), value, name.trim())
      await qc.invalidateQueries({ queryKey: metaKey(token) })
    } catch (e) {
      setError((e as Error).message)
      setCode("")
    } finally {
      setBusy(false)
    }
  }

  if (!share.mailReady)
    return (
      <GateCard icon={<MailIcon />} title={`“${title}” needs your email address`} description="This link asks visitors to confirm their email, but the server can't send email yet.">
        <p className="text-sm text-muted-foreground">Let {share.from} know, so they can set up email or send you a different link.</p>
      </GateCard>
    )

  if (step === "code")
    return (
      <GateCard
        icon={<MailIcon />}
        title="Check your email"
        description={
          <>
            {invited ? "If this address was invited, we sent" : "We sent"} a 6-digit code to <span className="font-medium text-foreground">{email.trim()}</span>.
          </>
        }
      >
        <FieldGroup>
          <Field data-invalid={!!error}>
            <FieldLabel>Code</FieldLabel>
            <InputOTP
              maxLength={6}
              value={code}
              autoFocus
              disabled={busy}
              inputMode="numeric"
              onChange={(v) => {
                setCode(v)
                if (v.length === 6) check(v)
              }}
            >
              <InputOTPGroup>
                {Array.from({ length: 6 }).map((_, i) => (
                  <InputOTPSlot key={i} index={i} />
                ))}
              </InputOTPGroup>
            </InputOTP>
            {error && <FieldError>{error}</FieldError>}
            <FieldDescription>It expires in 15 minutes. Check your spam folder if it doesn't arrive.</FieldDescription>
          </Field>
          <div className="flex items-center justify-between gap-2">
            <Button type="button" variant="ghost" size="sm" onClick={() => setStep("email")} disabled={busy}>
              Use another address
            </Button>
            <Button type="button" variant="outline" size="sm" onClick={() => send()} disabled={busy || wait > 0}>
              {wait > 0 ? `Resend in ${wait}s` : "Resend code"}
            </Button>
          </div>
        </FieldGroup>
      </GateCard>
    )

  return (
    <GateCard
      icon={<MailIcon />}
      title={invited ? `“${title}” is for invited people` : `Confirm your email to open “${title}”`}
      description={
        invited
          ? "Only invited people can open this. Enter the email address the invitation was sent to."
          : `${share.from} would like to know who opens this. We'll email you a code, and you won't need an account.`
      }
    >
      <form onSubmit={send}>
        <FieldGroup>
          <Field data-invalid={!!error}>
            <FieldLabel htmlFor="gate-email">Email</FieldLabel>
            <Input id="gate-email" type="email" autoComplete="email" autoFocus required value={email} onChange={(e) => setEmail(e.target.value)} placeholder="you@example.com" aria-invalid={!!error} />
            {error && <FieldError>{error}</FieldError>}
          </Field>
          <Field>
            <FieldLabel htmlFor="gate-name">
              Name <span className="font-normal text-muted-foreground">(optional)</span>
            </FieldLabel>
            <Input id="gate-name" autoComplete="name" maxLength={80} value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Button type="submit" disabled={busy || !email.trim()}>
            {busy && <Spinner />} Email me a code
          </Button>
        </FieldGroup>
      </form>
    </GateCard>
  )
}

function PasswordGate({ ctx }: { ctx: Ctx }) {
  const { share, sapi, token } = ctx
  const qc = useQueryClient()
  const [password, setPassword] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const submit = async (ev: FormEvent) => {
    ev.preventDefault()
    setBusy(true)
    setError("")
    try {
      await sapi.auth(password)
      await qc.invalidateQueries({ queryKey: metaKey(token) })
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <GateCard icon={<LockIcon />} title={`“${share.title || share.name}” is protected`} description="Enter the password you were given to open it.">
      <form onSubmit={submit}>
        <FieldGroup>
          <Field data-invalid={!!error}>
            <Input type="password" autoFocus required placeholder="Password" value={password} onChange={(e) => setPassword(e.target.value)} aria-invalid={!!error} />
            {error && <FieldError>{error}</FieldError>}
          </Field>
          <Button type="submit" disabled={busy}>
            {busy && <Spinner />} Open
          </Button>
        </FieldGroup>
      </form>
    </GateCard>
  )
}

// Previews.

const TEXT_LIMIT = 512 * 1024

function TextBody({ src, markdown }: { src: string; markdown: boolean }) {
  const [text, setText] = useState<string | null>(null)
  const [error, setError] = useState(false)
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
    <div className="size-full overflow-auto bg-background">
      {markdown ? <Office.Markdown text={text} className="mx-auto max-w-3xl p-6" /> : <pre className="p-5 font-mono text-xs leading-relaxed whitespace-pre-wrap">{text}</pre>}
    </div>
  )
}

function NoPreview({ entry, text, ctx }: { entry: Entry; text: string; ctx: Ctx }) {
  return (
    <div className="flex flex-col items-center gap-3 p-8 text-center">
      <FileTile entry={entry} size="lg" />
      <p className="max-w-sm text-sm text-muted-foreground">{text}</p>
      {ctx.share.canDownload && (
        <Button size="sm" asChild>
          <a href={ctx.sapi.downloadUrl(entry.path)} download>
            <DownloadIcon /> Download
          </a>
        </Button>
      )}
    </div>
  )
}

type BodyProps = {
  ctx: Ctx
  entry: Entry
  videoRef?: React.Ref<HTMLVideoElement>
  markers?: ReturnType<typeof markersFrom>
  onImageClick?: (x: number, y: number) => void
  onMarkerClick?: (id: number) => void
}

/** The file itself: media through MediaPreview, documents through the Office viewers. */
function FileBody({ ctx, entry, videoRef, markers, onImageClick, onMarkerClick }: BodyProps) {
  const { share, sapi } = ctx
  if (!share.canView) return <NoPreview ctx={ctx} entry={entry} text="Previews are turned off for this link." />
  const src = sapi.downloadUrl(entry.path, true)
  const media = mediaKindOf(entry.name)
  if (media)
    return (
      <MediaPreview
        name={entry.name}
        src={src}
        proxySrc={isVideo(entry.name) ? sapi.mediaUrl(entry, "proxy") : undefined}
        stillSrc={isVideo(entry.name) || needsStill(entry.name) ? sapi.mediaUrl(entry, "still") : undefined}
        downloadHref={share.canDownload ? sapi.downloadUrl(entry.path) : undefined}
        watermark={share.watermark ? watermarkText(share) : undefined}
        videoRef={videoRef}
        markers={markers}
        onImageClick={onImageClick}
        onMarkerClick={onMarkerClick ? (m) => onMarkerClick(Number(m.id)) : undefined}
      />
    )
  let doc: React.ReactNode
  switch (previewKind(entry)) {
    case "pdf":
      doc = <iframe src={src} title={entry.name} className="size-full border-0 bg-white" />
      break
    case "docx":
      doc = <Office.Docx src={src} />
      break
    case "sheet":
      doc = <Office.Sheet src={src} />
      break
    case "pptx":
      doc = <Office.Pptx src={src} />
      break
    case "google":
      doc = <Office.Google src={src} name={entry.name} />
      break
    case "text":
      doc = <TextBody src={src} markdown={["md", "markdown"].includes(extOf(entry.name))} />
      break
    default:
      return <NoPreview ctx={ctx} entry={entry} text="No preview for this file type. Download it to open it." />
  }
  if (!share.watermark) return doc
  // Documents are sent as they are; the watermark is only drawn over them.
  return (
    <div className="relative size-full">
      {doc}
      <WatermarkOverlay text={watermarkText(share)} onLight />
    </div>
  )
}

/** A file with its review panel beside (desktop) or below (mobile) it. */
function ReviewableFile({ ctx, entry, className }: { ctx: Ctx; entry: Entry; className?: string }) {
  const { share, sapi, token } = ctx
  const review = share.canComment && share.canView
  const videoRef = useRef<HTMLVideoElement>(null)
  const [pin, setPin] = useState<{ x: number; y: number } | null>(null)
  const [active, setActive] = useState<number | null>(null)
  const adapter = useMemo<ReviewAdapter>(
    () => ({ list: () => sapi.comments(entry.path), add: (input) => sapi.addComment({ ...input, path: entry.path }) }),
    [sapi, entry.path],
  )
  const queryKey = useMemo(() => ["share-comments", token, entry.path], [token, entry.path])
  const comments = useComments(adapter, queryKey, review)
  const media = mediaKindOf(entry.name)
  const pinnable = review && media === "image"
  const markers = useMemo(() => {
    const ms = markersFrom(comments.data, active)
    return pin ? [...ms, { id: "pin", x: pin.x, y: pin.y, label: "+", active: true }] : ms
  }, [comments.data, active, pin])

  const body = (
    <div className="relative flex min-h-0 flex-1 items-center justify-center overflow-hidden bg-muted/50">
      <Suspense fallback={<Spinner />}>
        <FileBody
          key={entry.path}
          ctx={ctx}
          entry={entry}
          videoRef={videoRef}
          markers={pinnable ? markers : undefined}
          onImageClick={pinnable ? (x, y) => setPin({ x, y }) : undefined}
          onMarkerClick={(id) => setActive((a) => (a === id ? null : id))}
        />
      </Suspense>
    </div>
  )
  if (!review) return <div className={cn("flex min-h-0 flex-col", className)}>{body}</div>
  return (
    <div className={cn("flex min-h-0 flex-col lg:flex-row", className)}>
      {body}
      <ReviewPanel
        adapter={adapter}
        queryKey={queryKey}
        className="h-[min(26rem,60%)] shrink-0 border-t bg-background lg:h-auto lg:w-80 lg:border-t-0 lg:border-l"
        getTime={media === "video" ? () => videoRef.current?.currentTime ?? null : undefined}
        onCompose={() => videoRef.current?.pause()}
        onSeek={(t) => {
          const v = videoRef.current
          if (!v) return
          v.currentTime = t
          v.pause()
        }}
        pin={pin}
        onClearPin={() => setPin(null)}
        pinnable={pinnable}
        activeId={active}
        onActiveChange={setActive}
        needsName={!share.viewer}
      />
    </div>
  )
}

function SingleFile({ ctx }: { ctx: Ctx }) {
  const { share, sapi } = ctx
  const entry: Entry = { name: share.name, path: "/", isDir: false, size: share.size ?? 0, modTime: share.modTime ?? 0, kind: share.kind ?? "other" }
  const media = mediaKindOf(entry.name)
  const previewable = share.canView && canPreview(entry)
  return (
    <div className="mx-auto flex w-full max-w-6xl flex-1 flex-col p-4 md:p-8">
      <Heading share={share}>
        {share.canDownload && (
          <Button asChild>
            <a href={sapi.downloadUrl("/")} download>
              <DownloadIcon /> Download
            </a>
          </Button>
        )}
      </Heading>
      <Message share={share} />
      {previewable ? (
        <ReviewableFile
          ctx={ctx}
          entry={entry}
          className={cn("overflow-hidden rounded-xl border bg-card", media === "audio" ? "h-48" : "h-[min(78svh,52rem)] lg:h-[min(72svh,48rem)]")}
        />
      ) : (
        <div className="flex items-center gap-4 rounded-xl border bg-card p-5">
          <FileTile entry={entry} size="lg" />
          <div className="min-w-0">
            <div className="truncate font-medium">{share.name}</div>
            <div className="text-sm text-muted-foreground">{describeEntry(entry)}</div>
          </div>
        </div>
      )}
    </div>
  )
}

/** Preview dialog for files in a folder link, with review when allowed. */
function SharePreview({ ctx, entries, index, onIndexChange }: { ctx: Ctx; entries: Entry[]; index: number | null; onIndexChange: (i: number | null) => void }) {
  const { share, sapi } = ctx
  const entry = index !== null ? entries[index] : undefined
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
      <DialogContent mobile="full" onOpenAutoFocus={(e) => e.preventDefault()} className="flex max-h-[96svh] w-[min(88rem,calc(100vw-1rem))] max-w-none flex-col gap-3 p-3 sm:max-w-none sm:p-4">
        {entry && (
          <>
            <div className="flex items-center gap-3 pr-8">
              <FileTile entry={entry} size="sm" />
              <div className="min-w-0 flex-1">
                <DialogTitle className="truncate text-sm">{entry.name}</DialogTitle>
                <DialogDescription className="text-xs">
                  {describeEntry(entry)} · {formatDate(entry.modTime)}
                </DialogDescription>
              </div>
              {share.canDownload && (
                <Button size="sm" asChild>
                  <a href={sapi.downloadUrl(entry.path)} download>
                    <DownloadIcon /> <span className="hidden sm:inline">Download</span>
                  </a>
                </Button>
              )}
            </div>
            <div className="relative flex h-[calc(96svh-6rem)] min-h-64 flex-col overflow-hidden rounded-xl max-sm:h-auto max-sm:min-h-0 max-sm:flex-1">
              <ReviewableFile key={entry.path} ctx={ctx} entry={entry} className="min-h-0 flex-1" />
              {index! > 0 && (
                <Button variant="secondary" size="icon" className="absolute top-1/3 left-2 rounded-full shadow lg:top-1/2" onClick={() => onIndexChange(index! - 1)} aria-label="Previous">
                  <ChevronLeftIcon />
                </Button>
              )}
              {index! < entries.length - 1 && (
                <Button
                  variant="secondary"
                  size="icon"
                  className={cn("absolute top-1/3 right-2 rounded-full shadow lg:top-1/2", share.canComment && share.canView && "lg:right-[21.5rem]")}
                  onClick={() => onIndexChange(index! + 1)}
                  aria-label="Next"
                >
                  <ChevronRightIcon />
                </Button>
              )}
            </div>
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}

// Download-only and drop boxes.

function DownloadOnly({ ctx }: { ctx: Ctx }) {
  const { share, sapi } = ctx
  return (
    <div className="mx-auto flex w-full max-w-3xl flex-1 flex-col p-4 md:p-8">
      <Heading share={share} />
      <Message share={share} />
      <div className="flex flex-col items-center gap-4 rounded-xl border bg-card p-8 text-center">
        <FileTile entry={{ name: share.name, isDir: true }} size="lg" />
        <p className="text-sm text-muted-foreground">This folder can be downloaded as a single zip file.</p>
        {share.canDownload && (
          <Button asChild>
            <a href={sapi.zipUrl("/")} download>
              <DownloadIcon /> Download all
            </a>
          </Button>
        )}
      </div>
    </div>
  )
}

function useUploader(share: PublicShare, sapi: SApi, dir: string, uploader: string, onDone?: () => void) {
  const [dragging, setDragging] = useState(false)
  const depth = useRef(0)
  const start = (files: QueueFile[]) => {
    if (share.uploadNamed && !uploader.trim()) {
      toast.error("Enter your name first")
      return
    }
    const max = share.uploadMaxMB * 1024 * 1024
    const exts = share.uploadExts ?? []
    const ok = files.filter((f) => (!max || f.file.size <= max) && (!exts.length || exts.includes(extOf(f.file.name))))
    if (ok.length < files.length) {
      toast.error(
        `${files.length - ok.length} file(s) skipped: ${exts.length ? `only ${exts.join(", ")} files` : ""}${exts.length && max ? " up to " : ""}${max ? formatBytes(max) : ""} accepted`,
      )
    }
    if (ok.length) enqueue(ok, { dir, base: sapi.uploadBase, uploader: uploader.trim() || undefined, onDone })
  }
  const dropProps = {
    onDragEnter: (ev: DragEvent) => {
      if (!ev.dataTransfer.types.includes("Files")) return
      depth.current++
      setDragging(true)
    },
    onDragOver: (ev: DragEvent) => ev.dataTransfer.types.includes("Files") && ev.preventDefault(),
    onDragLeave: () => {
      depth.current = Math.max(0, depth.current - 1)
      if (!depth.current) setDragging(false)
    },
    onDrop: async (ev: DragEvent) => {
      depth.current = 0
      setDragging(false)
      if (!ev.dataTransfer.types.includes("Files")) return
      ev.preventDefault()
      start(await filesFromDrop(ev.dataTransfer))
    },
  }
  return { dragging, dropProps, start }
}

const viewerName = (share: PublicShare) => share.viewer?.name || share.viewer?.email || ""

function UploaderName({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <Field>
      <FieldLabel htmlFor="uploader">Your name</FieldLabel>
      <Input id="uploader" maxLength={60} placeholder="So they know who sent the files" value={value} onChange={(e) => onChange(e.target.value)} />
    </Field>
  )
}

function DropBox({ ctx }: { ctx: Ctx }) {
  const { share, sapi } = ctx
  const [name, setName] = useState(() => viewerName(share))
  const up = useUploader(share, sapi, "/", name)
  const input = useRef<HTMLInputElement>(null)
  const done = useUploads().filter((u) => u.base === sapi.uploadBase && u.status === "done")
  const exts = share.uploadExts ?? []
  return (
    <div className="flex flex-1 items-center justify-center p-4 md:p-8" {...up.dropProps}>
      <div className="w-full max-w-xl">
        <Heading share={share} />
        <Message share={share} />
        <Card>
          <CardContent className="space-y-4">
            {share.uploadNamed && <UploaderName value={name} onChange={setName} />}
            <button
              type="button"
              onClick={() => input.current?.click()}
              className={cn("flex w-full flex-col items-center gap-2 rounded-xl border-2 border-dashed p-8 text-center transition-colors", up.dragging ? "border-brand bg-brand/10" : "hover:bg-muted/60")}
            >
              <div className="mb-1 flex size-10 items-center justify-center rounded-lg border bg-secondary">
                <UploadCloudIcon className="size-5" />
              </div>
              <span className="text-sm font-semibold">Click to upload or drag and drop</span>
              <span className="text-xs text-muted-foreground">
                {exts.length ? exts.map((e) => e.toUpperCase()).join(", ") : "Any file type"}
                {share.uploadMaxMB ? ` · up to ${formatBytes(share.uploadMaxMB * 1024 * 1024)} each` : ""}
              </span>
            </button>
            <input
              ref={input}
              type="file"
              multiple
              hidden
              accept={exts.length ? exts.map((e) => "." + e).join(",") : undefined}
              onChange={(e) => {
                if (e.target.files) up.start(filesFromInput(e.target.files))
                e.target.value = ""
              }}
            />
            <p className="text-xs text-muted-foreground">Files are delivered privately. You can't see what others have sent.</p>
            {done.length > 0 && (
              <p className="text-sm font-medium text-emerald-600 dark:text-emerald-400">
                {done.length} file{done.length === 1 ? "" : "s"} delivered.
              </p>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  )
}

// Folders and collections.

function FolderShare({ ctx, path }: { ctx: Ctx; path: string }) {
  const { token, share, sapi } = ctx
  const navigate = useNavigate()
  const qc = useQueryClient()
  const [view, setView] = useState<ViewMode>(share.viewMode)
  const [sort, setSort] = useState<Sort>({ key: "name", dir: "asc" })
  const [preview, setPreview] = useState<number | null>(null)
  const [lightbox, setLightbox] = useState<number | null>(null)
  const [uploader, setUploader] = useState(() => viewerName(share))
  const listing = useQuery({ queryKey: ["share-list", token, path], queryFn: () => sapi.list(path) })
  const entries = useMemo(() => sortEntries(listing.data?.entries ?? [], sort), [listing.data, sort])
  const files = useMemo(() => entries.filter((e) => !e.isDir), [entries])
  const selection = useSelection(entries)
  const input = useRef<HTMLInputElement>(null)
  const up = useUploader(share, sapi, path, uploader, () => qc.invalidateQueries({ queryKey: ["share-list", token] }))
  const canUpload = share.canUpload && !share.collection

  const go = (p: string) => navigate(`/s/${token}${p === "/" ? "" : encodePath(p)}`)
  const open = (e: ShareEntry) => {
    if (e.isDir) return go(e.path)
    const i = files.findIndex((f) => f.path === e.path)
    if (view === "gallery" && !share.canComment) return setLightbox(i)
    if (canPreview(e)) setPreview(i)
    else if (share.canDownload) window.location.href = sapi.downloadUrl(e.path)
  }
  const sel = selection.items

  return (
    <div className="relative mx-auto flex w-full max-w-6xl flex-1 flex-col p-3 md:p-8" {...(canUpload ? up.dropProps : {})}>
      <Heading share={share}>
        <div className="flex flex-wrap items-center gap-2">
          {canUpload && (
            <Button variant="outline" onClick={() => input.current?.click()}>
              <UploadCloudIcon /> Upload
            </Button>
          )}
          {share.canDownload && (
            <Button asChild>
              <a href={sel.length && view !== "gallery" ? sapi.zipUrl(path, sel.map((e) => e.name)) : sapi.zipUrl(path)} download>
                <DownloadIcon /> {sel.length && view !== "gallery" ? `Download ${sel.length}` : "Download all"}
              </a>
            </Button>
          )}
        </div>
      </Heading>
      <Message share={share} />
      {canUpload && share.uploadNamed && (
        <div className="mb-4 max-w-sm">
          <UploaderName value={uploader} onChange={setUploader} />
        </div>
      )}
      <input
        ref={input}
        type="file"
        multiple
        hidden
        onChange={(e) => {
          if (e.target.files) up.start(filesFromInput(e.target.files))
          e.target.value = ""
        }}
      />
      <div className="mb-3 flex items-center gap-3">
        <div className="min-w-0 flex-1">
          <PathBreadcrumbs path={path} rootLabel={share.title || share.name} rootIcon={<FolderIcon className="size-3.5" />} onNavigate={go} />
        </div>
        <ToggleGroup type="single" variant="outline" value={view} onValueChange={(v) => v && setView(v as ViewMode)}>
          <ToggleGroupItem value="list" aria-label="List view" className="w-8">
            <ListIcon />
          </ToggleGroupItem>
          <ToggleGroupItem value="grid" aria-label="Grid view" className="w-8">
            <LayoutGridIcon />
          </ToggleGroupItem>
          <ToggleGroupItem value="gallery" aria-label="Gallery view" className="w-8">
            <ImagesIcon />
          </ToggleGroupItem>
        </ToggleGroup>
      </div>
      {listing.isLoading ? (
        <div className="space-y-2">
          {Array.from({ length: 6 }).map((_, i) => (
            <Skeleton key={i} className="h-11 w-full rounded-lg" />
          ))}
        </div>
      ) : entries.length === 0 ? (
        <div className="rounded-xl border border-dashed bg-card/50 py-6">
          <EmptyState icon={<FolderIcon />} title="This folder is empty" description={canUpload ? "Drop files here to upload them." : undefined} />
        </div>
      ) : view === "gallery" ? (
        <Gallery ctx={ctx} entries={entries} onOpen={open} />
      ) : (
        <FileList
          entries={entries}
          view={view}
          selection={selection}
          sort={sort}
          onSort={setSort}
          onOpen={open}
          thumbUrl={sapi.thumbUrl}
          resetKey={path}
          actions={(e) => [
            { label: e.isDir ? "Open" : "Preview", icon: <FolderIcon />, onSelect: () => open(e) },
            ...(share.canDownload
              ? [{ label: "Download", icon: <DownloadIcon />, onSelect: () => (window.location.href = e.isDir ? sapi.zipUrl(path, [e.name]) : sapi.downloadUrl(e.path)) }]
              : []),
          ]}
        />
      )}
      {up.dragging && (
        <div className="pointer-events-none absolute inset-3 z-30 flex items-center justify-center rounded-2xl border-2 border-dashed border-brand bg-brand/10">
          <p className="rounded-xl bg-popover px-6 py-4 text-sm font-medium shadow-lg">Drop to upload</p>
        </div>
      )}
      <SharePreview ctx={ctx} entries={files} index={preview} onIndexChange={setPreview} />
      {lightbox !== null && <Lightbox ctx={ctx} items={files} index={lightbox} onIndexChange={setLightbox} />}
    </div>
  )
}

/** Thumbnail that falls back to the file's icon. Videos and HEIC/RAW photos
 * get theirs from a generated still, so wait for it. */
function GalleryThumb({ ctx, entry }: { ctx: Ctx; entry: ShareEntry }) {
  const [failed, setFailed] = useState(false)
  const media = mediaKindOf(entry.name)
  const generated = isVideo(entry.name) || needsStill(entry.name)
  const still = useRendition(generated ? ctx.sapi.mediaUrl(entry, "still") : undefined, generated)
  const fallback = (
    <div className="flex aspect-[4/3] w-full flex-col items-center justify-center gap-2 bg-muted/60 p-3">
      <FileTile entry={entry} />
      <span className="w-full truncate text-center text-xs text-muted-foreground">{entry.name}</span>
    </div>
  )
  if (failed || !media || media === "audio") return fallback
  if (generated && still?.state !== "ready") {
    if (still && still.state !== "queued" && still.state !== "running") return fallback
    return (
      <div className="flex aspect-[4/3] w-full items-center justify-center bg-muted/60">
        <Spinner className="text-muted-foreground" />
      </div>
    )
  }
  return (
    <>
      <img src={ctx.sapi.thumbUrl(entry)} alt={entry.name} loading="lazy" decoding="async" draggable={false} onError={() => setFailed(true)} className="block h-auto w-full bg-muted" />
      {isVideo(entry.name) && (
        <span className="absolute top-1/2 left-1/2 flex size-10 -translate-x-1/2 -translate-y-1/2 items-center justify-center rounded-full bg-black/55 text-white backdrop-blur-sm">
          <PlayIcon className="ml-0.5 size-4 fill-current" />
        </span>
      )}
    </>
  )
}

function Gallery({ ctx, entries, onOpen }: { ctx: Ctx; entries: ShareEntry[]; onOpen: (e: ShareEntry) => void }) {
  const folders = entries.filter((e) => e.isDir)
  const items = entries.filter((e) => !e.isDir)
  return (
    <div className="space-y-3">
      {folders.length > 0 && (
        <div className="flex flex-wrap gap-2">
          {folders.map((f) => (
            <button key={f.path} type="button" onClick={() => onOpen(f)} className="flex items-center gap-2 rounded-lg border bg-card px-3 py-1.5 text-sm font-medium hover:border-ring/50">
              <FolderIcon className="size-4 text-sky-500" />
              {f.name}
              {f.items ? <span className="text-xs font-normal text-muted-foreground">{f.items}</span> : null}
            </button>
          ))}
        </div>
      )}
      <div className="columns-2 gap-2 sm:columns-3 lg:columns-4 [&>*]:mb-2">
        {items.map((e) => (
          <button
            key={e.path}
            type="button"
            onClick={() => onOpen(e)}
            className="group relative block w-full break-inside-avoid overflow-hidden rounded-lg border bg-card text-left focus-visible:ring-2 focus-visible:ring-ring"
            title={e.name}
          >
            <GalleryThumb ctx={ctx} entry={e} />
            <TileReviewBadge review={e.review} className="absolute top-1.5 left-1.5" />
            <span className="pointer-events-none absolute inset-x-0 bottom-0 truncate bg-gradient-to-t from-black/60 to-transparent px-2 pt-6 pb-1.5 text-xs text-white opacity-0 transition-opacity group-hover:opacity-100">
              {e.name}
            </span>
          </button>
        ))}
      </div>
    </div>
  )
}

/** Full-screen photo viewer with keyboard and swipe. */
function Lightbox({ ctx, items, index, onIndexChange }: { ctx: Ctx; items: Entry[]; index: number; onIndexChange: (i: number | null) => void }) {
  const { share, sapi } = ctx
  const entry = items[index]
  const start = useRef<{ x: number; y: number } | null>(null)
  const next = useCallback(() => index < items.length - 1 && onIndexChange(index + 1), [index, items.length, onIndexChange])
  const prev = useCallback(() => index > 0 && onIndexChange(index - 1), [index, onIndexChange])

  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      if (ev.key === "Escape") onIndexChange(null)
      else if (ev.key === "ArrowRight") next()
      else if (ev.key === "ArrowLeft") prev()
    }
    window.addEventListener("keydown", onKey)
    const overflow = document.body.style.overflow
    document.body.style.overflow = "hidden"
    return () => {
      window.removeEventListener("keydown", onKey)
      document.body.style.overflow = overflow
    }
  }, [next, prev, onIndexChange])

  // Preload the next photo.
  useEffect(() => {
    const n = items[index + 1]
    if (n && mediaKindOf(n.name) === "image" && !needsStill(n.name)) new Image().src = sapi.downloadUrl(n.path, true)
  }, [index, items, sapi])

  if (!entry) return null
  const onPointerDown = (ev: ReactPointerEvent) => {
    if (ev.pointerType !== "mouse") start.current = { x: ev.clientX, y: ev.clientY }
  }
  const onPointerUp = (ev: ReactPointerEvent) => {
    const s = start.current
    start.current = null
    if (!s) return
    const dx = ev.clientX - s.x
    if (Math.abs(dx) > 50 && Math.abs(dx) > Math.abs(ev.clientY - s.y)) (dx < 0 ? next : prev)()
  }

  return (
    <div role="dialog" aria-modal aria-label={entry.name} className="fixed inset-0 z-50 flex flex-col bg-black/95 text-white">
      <div className="flex h-12 shrink-0 items-center gap-2 px-3">
        <span className="text-xs text-white/60 tabular-nums">
          {index + 1} / {items.length}
        </span>
        <span className="min-w-0 flex-1 truncate text-sm font-medium">{entry.name}</span>
        {share.canDownload && (
          <Button variant="ghost" size="icon" className="text-white hover:bg-white/10 hover:text-white" asChild>
            <a href={sapi.downloadUrl(entry.path)} download aria-label="Download">
              <DownloadIcon />
            </a>
          </Button>
        )}
        <Button variant="ghost" size="icon" className="text-white hover:bg-white/10 hover:text-white" onClick={() => onIndexChange(null)} aria-label="Close">
          <XIcon />
        </Button>
      </div>
      <div className="relative flex min-h-0 flex-1 touch-pan-y items-center justify-center px-2 pb-4 sm:px-14" onPointerDown={onPointerDown} onPointerUp={onPointerUp}>
        <Suspense fallback={<Spinner />}>
          <FileBody key={entry.path} ctx={ctx} entry={entry} />
        </Suspense>
        {index > 0 && (
          <button type="button" onClick={prev} aria-label="Previous" className="absolute left-2 hidden size-10 items-center justify-center rounded-full bg-white/10 hover:bg-white/20 sm:flex">
            <ChevronLeftIcon className="size-5" />
          </button>
        )}
        {index < items.length - 1 && (
          <button type="button" onClick={next} aria-label="Next" className="absolute right-2 hidden size-10 items-center justify-center rounded-full bg-white/10 hover:bg-white/20 sm:flex">
            <ChevronRightIcon className="size-5" />
          </button>
        )}
      </div>
    </div>
  )
}
