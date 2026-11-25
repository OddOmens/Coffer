import { useEffect, useMemo, useState, type ReactNode } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import {
  CheckIcon,
  ChevronDownIcon,
  CopyIcon,
  EyeIcon,
  GlobeIcon,
  InboxIcon,
  LinkIcon,
  MailCheckIcon,
  MailIcon,
  MessageSquareTextIcon,
  PackageIcon,
  PauseIcon,
  PencilIcon,
  PlayIcon,
  PlusIcon,
  QrCodeIcon,
  RotateCcwIcon,
  SendIcon,
  ShieldCheckIcon,
  Trash2Icon,
  UserXIcon,
  UsersIcon,
  WifiIcon,
  XIcon,
} from "lucide-react"
import QRCode from "qrcode"
import { toast } from "sonner"

import { usePlaces, useUser } from "@/components/app-layout"
import { Spinner } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Textarea } from "@/components/ui/textarea"
import {
  api,
  NOTIFY_DOWNLOAD,
  NOTIFY_OPEN,
  NOTIFY_UPLOAD,
  type Share,
  type ShareAccess,
  type ShareInput,
  type ViewMode,
} from "@/lib/api"
import { formatDay, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"

export function useCopy() {
  const [copied, setCopied] = useState(false)
  return {
    copied,
    copy: async (text: string) => {
      try {
        await navigator.clipboard.writeText(text)
        setCopied(true)
        setTimeout(() => setCopied(false), 1500)
      } catch {
        toast.error("Copying needs HTTPS. Select the link and copy it manually.")
      }
    },
  }
}

/** What is being shared: an item in a place, or a collection. */
export type ShareTarget = { place: string; path: string; name: string; isDir: boolean; collection?: string; versioned?: boolean }

/** Private address ranges, for links that only work on this network. */
const LOCAL_NETS = "10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,100.64.0.0/10,127.0.0.0/8,fc00::/7,::1/128"

const DAY = 86400
const expiryChoices = [
  ["1", "1 day"],
  ["7", "7 days"],
  ["14", "14 days"],
  ["30", "30 days"],
  ["90", "90 days"],
  ["365", "1 year"],
  ["0", "Never"],
  ["custom", "Pick a date…"],
] as const

type Form = {
  canView: boolean
  canDownload: boolean
  canUpload: boolean
  usePassword: boolean
  password: string
  expiry: string
  expiryDate: string
  maxDownloads: string
  title: string
  message: string
  note: string
  uploadMaxMB: string
  uploadExts: string
  uploadNamed: boolean
  notify: number
  allowedNets: string
  viewMode: ViewMode
  access: ShareAccess
  canComment: boolean
  latest: boolean
  watermark: boolean
  emails: string[]
  inviteMessage: string
  request: boolean // phrase invitations as asking for files
}

type Preset = { id: string; label: string; help: string; icon: typeof EyeIcon; dirOnly?: boolean; uploads?: boolean; apply: Partial<Form>; match: (keyof Form)[] }

const presets: Preset[] = [
  { id: "review", label: "Review", help: "Comment and approve", icon: MessageSquareTextIcon, match: ["canView", "canDownload", "canUpload", "canComment"],
    apply: { canView: true, canDownload: true, canUpload: false, canComment: true, watermark: false, request: false } },
  { id: "deliver", label: "Deliver", help: "View and download", icon: PackageIcon, match: ["canView", "canDownload", "canUpload", "canComment"],
    apply: { canView: true, canDownload: true, canUpload: false, canComment: false, watermark: false, request: false, notify: NOTIFY_DOWNLOAD } },
  { id: "view", label: "View only", help: "Watermarked, no downloads", icon: ShieldCheckIcon, match: ["canView", "canDownload", "canUpload"],
    apply: { canView: true, canDownload: false, canUpload: false, watermark: true, request: false } },
  { id: "request", label: "Request files", help: "They upload, see nothing", icon: InboxIcon, dirOnly: true, uploads: true, match: ["canView", "canDownload", "canUpload"],
    apply: { canView: false, canDownload: false, canUpload: true, canComment: false, uploadNamed: true, notify: NOTIFY_UPLOAD, request: true, watermark: false } },
  { id: "collab", label: "Collaborate", help: "View, download, upload", icon: UsersIcon, dirOnly: true, uploads: true, match: ["canView", "canDownload", "canUpload"],
    apply: { canView: true, canDownload: true, canUpload: true, request: false } },
]

const accessChoices: { id: ShareAccess; label: string; help: string; icon: typeof EyeIcon; mail?: boolean }[] = [
  { id: "link", label: "Anyone with the link", help: "No sign-in. Good for quick sends.", icon: GlobeIcon },
  { id: "email", label: "Anyone who confirms their email", help: "They get a one-time code, so you see who opened what.", icon: MailCheckIcon, mail: true },
  { id: "invited", label: "Only people you invite", help: "Forwarded links don't work for anyone else.", icon: ShieldCheckIcon, mail: true },
]

function blankForm(days: number): Form {
  return {
    canView: true, canDownload: true, canUpload: false, usePassword: false, password: "",
    expiry: String(days), expiryDate: "", maxDownloads: "", title: "", message: "", note: "",
    uploadMaxMB: "", uploadExts: "", uploadNamed: false, notify: 0, allowedNets: "", viewMode: "list",
    access: "link", canComment: false, latest: false, watermark: false, emails: [], inviteMessage: "", request: false,
  }
}

function formFrom(s: Share): Form {
  return {
    canView: s.canView, canDownload: s.canDownload, canUpload: s.canUpload, usePassword: s.hasPassword, password: "",
    expiry: s.expiresAt ? "custom" : "0", expiryDate: s.expiresAt ? new Date(s.expiresAt * 1000).toISOString().slice(0, 10) : "",
    maxDownloads: s.maxDownloads ? String(s.maxDownloads) : "", title: s.title, message: s.message, note: s.note,
    uploadMaxMB: s.uploadMaxMB ? String(s.uploadMaxMB) : "", uploadExts: s.uploadExts, uploadNamed: s.uploadNamed,
    notify: s.notify, allowedNets: s.allowedNets.split(",").filter(Boolean).join("\n"), viewMode: s.viewMode,
    access: s.access, canComment: s.canComment, latest: s.latest, watermark: s.watermark, emails: [], inviteMessage: "",
    request: s.canUpload && !s.canView,
  }
}

function toInput(f: Form, isDir: boolean, editing?: Share): ShareInput | string {
  const input: ShareInput = {
    canView: f.canView, canDownload: f.canDownload, canUpload: isDir && f.canUpload,
    maxDownloads: Number(f.maxDownloads) || 0, title: f.title, message: f.message, note: f.note,
    uploadMaxMB: Number(f.uploadMaxMB) || 0, uploadExts: f.uploadExts, uploadNamed: f.uploadNamed, notify: f.notify,
    allowedNets: f.allowedNets.split(/[\s,]+/).filter(Boolean).join(","), viewMode: f.viewMode,
    access: f.access, canComment: f.canComment, latest: f.latest, watermark: f.watermark,
  }
  if (f.emails.length && !editing) input.invite = { emails: f.emails, message: f.inviteMessage, request: f.request }
  if (f.expiry === "custom") {
    if (!f.expiryDate) return "Pick an expiry date"
    input.expiresAt = Math.floor(new Date(f.expiryDate + "T23:59:59").getTime() / 1000)
  } else {
    input.expiresAt = Number(f.expiry) ? Math.floor(Date.now() / 1000) + Number(f.expiry) * DAY : 0
  }
  if (!f.usePassword) input.password = ""
  else if (f.password) input.password = f.password
  else if (!editing?.hasPassword) return "Enter a password or turn password protection off"
  if (!input.canView && !input.canDownload && !input.canUpload) return "Allow at least viewing, downloading or uploading"
  return input
}

function Toggle({ id, label, help, checked, onChange, disabled }: { id: string; label: ReactNode; help?: ReactNode; checked: boolean; onChange: (v: boolean) => void; disabled?: boolean }) {
  return (
    <Field orientation="horizontal" data-disabled={disabled}>
      <div className="flex-1">
        <FieldLabel htmlFor={id}>{label}</FieldLabel>
        {help && <FieldDescription>{help}</FieldDescription>}
      </div>
      <Switch id={id} checked={checked} onCheckedChange={onChange} disabled={disabled} />
    </Field>
  )
}

function Group({ title, children, defaultOpen }: { title: string; children: ReactNode; defaultOpen?: boolean }) {
  const [open, setOpen] = useState(!!defaultOpen)
  return (
    <Collapsible open={open} onOpenChange={setOpen} className="rounded-xl border">
      <CollapsibleTrigger className="flex w-full items-center justify-between px-4 py-3 text-sm font-semibold">
        {title}
        <ChevronDownIcon className={cn("size-4 text-muted-foreground transition-transform", open && "rotate-180")} />
      </CollapsibleTrigger>
      <CollapsibleContent>
        <FieldGroup className="gap-4 border-t px-4 py-4">{children}</FieldGroup>
      </CollapsibleContent>
    </Collapsible>
  )
}

/** Email addresses typed or pasted into one box, shown as chips. */
export function EmailsInput({ value, onChange, placeholder }: { value: string[]; onChange: (v: string[]) => void; placeholder?: string }) {
  const [draft, setDraft] = useState("")
  const add = (text: string, keepLast = false) => {
    const parts = text.split(/[\s,;]+/)
    const rest = keepLast ? (parts.pop() ?? "") : ""
    const found = parts.map((x) => x.trim().toLowerCase()).filter(Boolean)
    const ok = found.filter((x) => /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(x))
    if (ok.length < found.length) toast.error("Some of those aren't email addresses")
    if (ok.length) onChange([...new Set([...value, ...ok])])
    setDraft(rest)
  }
  return (
    <div className="flex min-h-8 flex-wrap items-center gap-1 rounded-lg border border-input px-1.5 py-1 focus-within:border-ring focus-within:ring-3 focus-within:ring-ring/50 dark:bg-input/30">
      {value.map((e) => (
        <span key={e} className="flex items-center gap-1 rounded-md bg-secondary py-0.5 pr-1 pl-2 text-xs">
          {e}
          <button type="button" onClick={() => onChange(value.filter((x) => x !== e))} aria-label={`Remove ${e}`} className="text-muted-foreground hover:text-foreground">
            <XIcon className="size-3" />
          </button>
        </span>
      ))}
      <input
        type="text"
        inputMode="email"
        autoComplete="email"
        aria-label="Email addresses"
        value={draft}
        placeholder={value.length ? "" : (placeholder ?? "name@example.com, …")}
        onChange={(e) => (/[\s,;]/.test(e.target.value) ? add(e.target.value, true) : setDraft(e.target.value))}
        onKeyDown={(e) => {
          if (e.key === "Enter" && draft) {
            e.preventDefault()
            add(draft)
          } else if (e.key === "Backspace" && !draft && value.length) onChange(value.slice(0, -1))
        }}
        onPaste={(e) => {
          e.preventDefault()
          add(draft + " " + e.clipboardData.getData("text"))
        }}
        onBlur={() => draft && add(draft)}
        className="min-w-32 flex-1 bg-transparent px-1 text-sm outline-none placeholder:text-muted-foreground"
      />
    </div>
  )
}

/** Link editor: presets plus every option. */
export function LinkForm({
  isDir,
  canUpload,
  form,
  setForm,
  editing,
  versioned,
  collection,
}: {
  isDir: boolean
  canUpload: boolean
  form: Form
  setForm: (f: Form) => void
  editing?: Share
  versioned?: boolean
  collection?: boolean
}) {
  const me = useUser()
  const mail = me.mailReady
  const set = <K extends keyof Form>(k: K, v: Form[K]) => setForm({ ...form, [k]: v })
  const notifyBit = (bit: number, on: boolean) => set("notify", on ? form.notify | bit : form.notify & ~bit)
  const active = presets.find((p) => p.match.every((k) => p.apply[k] === undefined || form[k] === p.apply[k]))
  const canUp = isDir && canUpload && !collection

  return (
    <div className="space-y-4">
      <div className="grid grid-cols-3 gap-1.5">
        {presets
          .filter((p) => (!p.dirOnly || isDir) && (!p.uploads || canUp))
          .map((p) => (
            <button
              key={p.id}
              type="button"
              onClick={() => setForm({ ...form, ...p.apply })}
              className={cn(
                "flex flex-col items-start gap-0.5 rounded-lg border p-2.5 text-left transition-colors hover:border-ring/60",
                active?.id === p.id && "border-brand bg-brand/5 ring-1 ring-brand",
              )}
            >
              <p.icon className="mb-0.5 size-4 text-muted-foreground" />
              <span className="text-[0.8125rem] font-medium">{p.label}</span>
              <span className="text-xs leading-tight text-muted-foreground">{p.help}</span>
            </button>
          ))}
      </div>

      <div className="space-y-1.5">
        <Label className="text-xs font-semibold tracking-wide text-muted-foreground uppercase">Who can open it</Label>
        <div className="overflow-hidden rounded-lg border">
          {accessChoices.map((c) => {
            const off = c.mail && !mail
            return (
              <label
                key={c.id}
                className={cn(
                  "flex cursor-pointer items-start gap-3 border-b px-3 py-2.5 last:border-b-0 hover:bg-muted/50",
                  form.access === c.id && "bg-brand/5",
                  off && "cursor-not-allowed opacity-50 hover:bg-transparent",
                )}
              >
                <input
                  type="radio"
                  name="access"
                  className="mt-1 accent-[var(--tint)]"
                  checked={form.access === c.id}
                  disabled={off}
                  onChange={() => set("access", c.id)}
                />
                <c.icon className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
                <span className="min-w-0">
                  <span className="block text-sm font-medium">{c.label}</span>
                  <span className="block text-xs text-muted-foreground">{off ? "Needs email to be set up (App settings → Email)." : c.help}</span>
                </span>
              </label>
            )
          })}
        </div>
      </div>

      {!editing && (
        <div className="space-y-1.5">
          <Label className="flex items-center gap-1.5 text-xs font-semibold tracking-wide text-muted-foreground uppercase">
            <MailIcon className="size-3.5" /> Send by email {form.access !== "invited" && <span className="font-normal normal-case">(optional)</span>}
          </Label>
          {mail ? (
            <>
              <EmailsInput value={form.emails} onChange={(v) => set("emails", v)} />
              {form.emails.length > 0 && (
                <Textarea
                  rows={2}
                  maxLength={2000}
                  placeholder={form.request ? "e.g. Please upload the brand assets here by Friday." : "Add a message (optional)"}
                  value={form.inviteMessage}
                  onChange={(e) => set("inviteMessage", e.target.value)}
                />
              )}
              <p className="text-xs text-muted-foreground">
                {form.access === "link" ? "They get the link by email. No account needed." : "Their invitation opens the link straight away. Anyone else has to be invited."}
              </p>
            </>
          ) : (
            <p className="rounded-lg border border-dashed px-3 py-2 text-xs text-muted-foreground">
              Ask an administrator to set up email (App settings → Email) to send links and invitations from here.
            </p>
          )}
        </div>
      )}

      <FieldGroup className="gap-4">
        <div className="grid gap-4 sm:grid-cols-2">
          <Field>
            <FieldLabel>Expires</FieldLabel>
            <Select value={form.expiry} onValueChange={(v) => set("expiry", v)}>
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {expiryChoices.map(([v, l]) => (
                  <SelectItem key={v} value={v}>
                    {l}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {form.expiry === "custom" && (
              <Input type="date" value={form.expiryDate} min={new Date().toISOString().slice(0, 10)} onChange={(e) => set("expiryDate", e.target.value)} />
            )}
          </Field>
          {form.canDownload && (
            <Field>
              <FieldLabel htmlFor="max-dl">Download limit</FieldLabel>
              <Input id="max-dl" type="number" min={0} placeholder="Unlimited" value={form.maxDownloads} onChange={(e) => set("maxDownloads", e.target.value)} />
            </Field>
          )}
        </div>
        <Toggle id="pw" label="Require a password" checked={form.usePassword} onChange={(v) => set("usePassword", v)} />
        {form.usePassword && (
          <Input
            type="password"
            autoComplete="new-password"
            placeholder={editing?.hasPassword ? "Leave blank to keep the current password" : "At least 10 characters"}
            value={form.password}
            onChange={(e) => set("password", e.target.value)}
          />
        )}
      </FieldGroup>

      <div className="space-y-2">
        <Group title="Access">
          <Toggle id="cv" label="View and preview" help={isDir ? "Browse folders and open previews in the browser." : "Open the preview page."} checked={form.canView} onChange={(v) => set("canView", v)} />
          <Toggle id="cd" label="Download originals" help={isDir ? "Single files and zips of folders." : undefined} checked={form.canDownload} onChange={(v) => set("canDownload", v)} />
          {isDir && !collection && (
            <Toggle id="cu" label="Upload files" help={canUpload ? "Recipients can add files to this folder." : "You can't write to this folder."} checked={form.canUpload} onChange={(v) => set("canUpload", v)} disabled={!canUpload} />
          )}
          <Toggle id="cc" label="Comments and approval" help="Comment on videos at a timecode or on a spot in an image, and approve or ask for changes." checked={form.canComment} onChange={(v) => set("canComment", v)} disabled={!form.canView} />
          <Toggle id="wm" label="Watermark previews" help="Burns the viewer's email (or “Preview”) into image previews and shows it over videos and documents. Not protection against a determined viewer." checked={form.watermark} onChange={(v) => set("watermark", v)} disabled={!form.canView} />
          {!isDir && versioned && (
            <Toggle id="latest" label="Always show the newest version" help="When you save name_v13 next to name_v12, the link shows v13." checked={form.latest} onChange={(v) => set("latest", v)} />
          )}
          <Field>
            <FieldLabel htmlFor="nets">Only from these networks</FieldLabel>
            <Textarea id="nets" rows={2} placeholder="Anywhere. Or e.g. 203.0.113.0/24, one per line" value={form.allowedNets} onChange={(e) => set("allowedNets", e.target.value)} className="font-mono text-xs" />
            <button type="button" className="self-start text-xs text-brand hover:underline" onClick={() => set("allowedNets", LOCAL_NETS.split(",").join("\n"))}>
              Only this network (LAN and VPN)
            </button>
          </Field>
        </Group>

        {isDir && form.canUpload && (
          <Group title="Upload rules" defaultOpen>
            <Toggle id="named" label="Ask for the uploader's name" help="Their files go into a folder named after them and the date." checked={form.uploadNamed} onChange={(v) => set("uploadNamed", v)} />
            <div className="grid gap-4 sm:grid-cols-2">
              <Field>
                <FieldLabel htmlFor="upmax">Max file size (MB)</FieldLabel>
                <Input id="upmax" type="number" min={0} placeholder="Server default" value={form.uploadMaxMB} onChange={(e) => set("uploadMaxMB", e.target.value)} />
              </Field>
              <Field>
                <FieldLabel htmlFor="exts">Allowed file types</FieldLabel>
                <Input id="exts" placeholder="Any, or e.g. mov, mp4, zip" value={form.uploadExts} onChange={(e) => set("uploadExts", e.target.value)} />
              </Field>
            </div>
          </Group>
        )}

        <Group title="Recipient page">
          <Field>
            <FieldLabel htmlFor="title">Title</FieldLabel>
            <Input id="title" maxLength={120} placeholder="Shown instead of the file name" value={form.title} onChange={(e) => set("title", e.target.value)} />
          </Field>
          <Field>
            <FieldLabel htmlFor="msg">Message</FieldLabel>
            <Textarea id="msg" rows={3} maxLength={2000} placeholder="e.g. Here are the final renders for round 2." value={form.message} onChange={(e) => set("message", e.target.value)} />
            <FieldDescription>Shown on the page once they're in.</FieldDescription>
          </Field>
          {isDir && (
            <Field>
              <FieldLabel>Layout</FieldLabel>
              <Select value={form.viewMode} onValueChange={(v) => set("viewMode", v as ViewMode)}>
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="list">List</SelectItem>
                  <SelectItem value="grid">Grid with thumbnails</SelectItem>
                  <SelectItem value="gallery">Photo gallery</SelectItem>
                </SelectContent>
              </Select>
            </Field>
          )}
        </Group>

        <Group title="Notifications & note">
          <div className="space-y-2.5">
            {([
              [NOTIFY_OPEN, "When the link is opened (at most hourly per person)"],
              [NOTIFY_DOWNLOAD, "When something is downloaded"],
              ...(isDir ? [[NOTIFY_UPLOAD, "When a file is uploaded"] as const] : []),
            ] as const).map(([bit, label]) => (
              <div key={bit} className="flex items-center gap-2">
                <Checkbox id={`n${bit}`} checked={(form.notify & bit) !== 0} onCheckedChange={(v) => notifyBit(bit, v === true)} />
                <Label htmlFor={`n${bit}`} className="font-normal">
                  {label}
                </Label>
              </div>
            ))}
            {form.canComment && <p className="text-xs text-muted-foreground">You're always told about comments and approvals.</p>}
          </div>
          <Field>
            <FieldLabel htmlFor="note">Private note</FieldLabel>
            <Input id="note" maxLength={500} placeholder="Only you see this, e.g. Acme review round 2" value={form.note} onChange={(e) => set("note", e.target.value)} />
          </Field>
        </Group>
      </div>
    </div>
  )
}

const accessLabel: Record<ShareAccess, string> = { link: "Anyone with link", email: "Email confirmed", invited: "Invited only" }

function LinkRow({ share, onEdit, onChanged }: { share: Share; onEdit: () => void; onChanged: () => void }) {
  const { copied, copy } = useCopy()
  const toggle = async () => {
    try {
      await api.updateShare(share.id, { disabled: !share.disabled })
      onChanged()
    } catch (e) {
      toast.error((e as Error).message)
    }
  }
  const remove = async () => {
    await api.deleteShare(share.id).catch((e) => toast.error(e.message))
    onChanged()
  }
  const people = share.recipients.filter((r) => !r.revoked).length
  return (
    <div className="rounded-lg border p-2.5">
      <div className="flex items-center gap-1.5">
        <Input readOnly value={share.url} className="h-7 font-mono text-xs" onFocus={(e) => e.target.select()} />
        <Button variant="outline" size="icon-sm" onClick={() => copy(share.url)} aria-label="Copy link">
          {copied ? <CheckIcon /> : <CopyIcon />}
        </Button>
        <QrButton url={share.url} />
      </div>
      <div className="mt-2 flex flex-wrap items-center gap-1">
        {!share.active && <Badge variant="destructive">{share.disabled ? "Paused" : "Expired"}</Badge>}
        {share.access !== "link" && <Badge variant="outline">{accessLabel[share.access]}</Badge>}
        {share.canView && <Badge variant="secondary">View</Badge>}
        {share.canDownload && <Badge variant="secondary">Download</Badge>}
        {share.canUpload && <Badge variant="secondary">Upload</Badge>}
        {share.canComment && <Badge variant="secondary">Review</Badge>}
        {share.hasPassword && <Badge variant="outline">Password</Badge>}
        <span className="text-xs text-muted-foreground">
          {share.expiresAt ? `until ${formatDay(share.expiresAt)}` : "no expiry"} · {share.downloads} downloads
          {people > 0 ? ` · ${people} ${people === 1 ? "person" : "people"}` : ""}
          {share.lastAccess ? ` · used ${relativeTime(share.lastAccess)}` : ""}
        </span>
        <div className="ml-auto flex gap-0.5">
          <Button variant="ghost" size="icon-xs" onClick={onEdit} aria-label="Edit">
            <PencilIcon />
          </Button>
          <Button variant="ghost" size="icon-xs" onClick={toggle} aria-label={share.disabled ? "Resume" : "Pause"}>
            {share.disabled ? <PlayIcon /> : <PauseIcon />}
          </Button>
          <Button variant="ghost" size="icon-xs" onClick={remove} aria-label="Delete link">
            <Trash2Icon />
          </Button>
        </div>
      </div>
    </div>
  )
}

/** A QR code for a link, in a small popover-like panel. */
export function QrButton({ url, size = "icon-sm" }: { url: string; size?: "icon-sm" | "icon" }) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <Button variant="outline" size={size} onClick={() => setOpen(true)} aria-label="Show QR code">
        <QrCodeIcon />
      </Button>
      <QrDialog url={url} open={open} onClose={() => setOpen(false)} />
    </>
  )
}

function QrDialog({ url, open, onClose }: { url: string; open: boolean; onClose: () => void }) {
  const [qr, setQr] = useState<string | null>(null)
  useEffect(() => {
    if (open) QRCode.toDataURL(url, { margin: 1, width: 320 }).then(setQr, () => setQr(null))
  }, [url, open])
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>Scan to open</DialogTitle>
          <DialogDescription>Point a phone camera at the code.</DialogDescription>
        </DialogHeader>
        {qr ? <img src={qr} alt="QR code for the link" className="mx-auto w-64 rounded-lg border bg-white p-2" /> : <Spinner />}
        <p className="text-center font-mono text-xs break-all text-muted-foreground">{url}</p>
      </DialogContent>
    </Dialog>
  )
}

/** The same link through the address you're using now, for people on this network. */
function localUrl(share: Share) {
  try {
    const u = new URL(share.url)
    if (u.origin === window.location.origin) return null
    return window.location.origin + u.pathname
  } catch {
    return null
  }
}

function LinkResult({ share, invited }: { share: Share; invited: number }) {
  const { copied, copy } = useCopy()
  const lan = localUrl(share)
  return (
    <div className="min-w-0 space-y-3">
      {invited > 0 && (
        <div className="flex items-center gap-2 rounded-lg bg-emerald-500/10 px-3 py-2 text-sm text-emerald-700 dark:text-emerald-400">
          <SendIcon className="size-4" /> Sent to {invited} {invited === 1 ? "person" : "people"}.
        </div>
      )}
      <div className="flex items-center gap-1.5">
        <Input readOnly value={share.url} className="font-mono text-xs" onFocus={(e) => e.target.select()} />
        <Button variant="outline" size="icon" onClick={() => copy(share.url)} aria-label="Copy link">
          {copied ? <CheckIcon /> : <CopyIcon />}
        </Button>
        <QrButton url={share.url} size="icon" />
      </div>
      {lan && (
        <div className="flex min-w-0 items-center gap-2 text-xs text-muted-foreground">
          <WifiIcon className="size-3.5 shrink-0" />
          <span className="min-w-0 flex-1 truncate">On this network: {lan}</span>
          <QrButton url={lan} />
        </div>
      )}
      <p className="text-xs text-muted-foreground">
        {share.hasPassword ? "Send the password separately from the link. " : ""}
        {share.access === "invited" ? "Only invited people can open it. " : share.access === "email" ? "Visitors confirm their email with a code. " : ""}
        {share.expiresAt ? `Expires ${formatDay(share.expiresAt)}.` : "Never expires."} Manage it any time under Shared links.
      </p>
    </div>
  )
}

/** People a link was sent to, or who confirmed their email to open it. */
export function Recipients({ share, onChanged }: { share: Share; onChanged: (s: Share) => void }) {
  const me = useUser()
  const [emails, setEmails] = useState<string[]>([])
  const [message, setMessage] = useState("")
  const [busy, setBusy] = useState(false)
  const send = async () => {
    setBusy(true)
    try {
      const s = await api.invite(share.id, { emails, message, request: share.canUpload && !share.canView })
      onChanged(s)
      toast.success(`Sent to ${emails.length} ${emails.length === 1 ? "person" : "people"}`)
      setEmails([])
      setMessage("")
    } catch (e) {
      toast.error((e as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const setRevoked = async (rid: string, revoked: boolean) => {
    try {
      onChanged(await api.updateRecipient(share.id, rid, { revoked }))
    } catch (e) {
      toast.error((e as Error).message)
    }
  }
  return (
    <div className="space-y-2">
      <Label className="text-xs font-semibold tracking-wide text-muted-foreground uppercase">People</Label>
      {share.recipients.length === 0 ? (
        <p className="text-xs text-muted-foreground">
          {share.access === "link" ? "Nobody has been sent this link by email yet." : "Nobody has opened it yet."}
        </p>
      ) : (
        <div className="overflow-hidden rounded-lg border">
          {share.recipients.map((r) => (
            <div key={r.id} className={cn("flex items-center gap-2.5 border-b px-3 py-2 last:border-b-0", r.revoked && "opacity-60")}>
              <span className="flex size-7 shrink-0 items-center justify-center rounded-full bg-secondary text-[0.6875rem] font-semibold uppercase">
                {(r.name || r.email).slice(0, 2)}
              </span>
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm">{r.name ? `${r.name} · ${r.email}` : r.email}</div>
                <div className="truncate text-xs text-muted-foreground">
                  {r.revoked
                    ? "Access removed"
                    : r.lastSeen
                      ? `Opened ${relativeTime(r.lastSeen)} · ${r.views} ${r.views === 1 ? "visit" : "visits"} · ${r.downloads} downloads`
                      : r.invitedAt
                        ? `Invited ${relativeTime(r.invitedAt)} · not opened yet`
                        : "Not opened yet"}
                </div>
              </div>
              {r.revoked ? (
                <Button variant="ghost" size="icon-xs" onClick={() => setRevoked(r.id, false)} aria-label={`Give ${r.email} access again`}>
                  <RotateCcwIcon />
                </Button>
              ) : (
                <Button variant="ghost" size="icon-xs" onClick={() => setRevoked(r.id, true)} aria-label={`Remove access for ${r.email}`}>
                  <UserXIcon />
                </Button>
              )}
            </div>
          ))}
        </div>
      )}
      {me.mailReady && (
        <div className="space-y-1.5">
          <EmailsInput value={emails} onChange={setEmails} placeholder="Invite more people by email…" />
          {emails.length > 0 && (
            <>
              <Textarea rows={2} maxLength={2000} placeholder="Add a message (optional)" value={message} onChange={(e) => setMessage(e.target.value)} />
              <Button size="sm" onClick={send} disabled={busy}>
                {busy ? <Spinner /> : <SendIcon />} Send
              </Button>
            </>
          )}
        </div>
      )}
    </div>
  )
}

/** Share with people: grant other users access to a folder. */
function PeopleTab({ target, onDone }: { target: ShareTarget; onDone: () => void }) {
  const qc = useQueryClient()
  const places = usePlaces()
  const dir = useQuery({ queryKey: ["directory"], queryFn: api.directory })
  const grants = useQuery({ queryKey: ["grants"], queryFn: api.grants })
  const [picked, setPicked] = useState<number[]>([])
  const [perm, setPerm] = useState({ canWrite: false, canDelete: false, canShare: false })
  const [filter, setFilter] = useState("")
  const [busy, setBusy] = useState(false)

  const place = places.data?.places.find((p) => p.id === target.place)
  const drivePath = place ? (place.root === "/" ? target.path : target.path === "/" ? place.root : place.root + target.path) : ""
  const existing = (grants.data ?? []).filter((g) => place && g.driveId === place.driveId && g.path === drivePath)
  const users = (dir.data ?? []).filter((u) => u.username.toLowerCase().includes(filter.toLowerCase()) && !existing.some((g) => g.userId === u.id))

  const share = async () => {
    setBusy(true)
    try {
      const r = await api.grant({ place: target.place, path: target.path, users: picked, ...perm })
      toast.success(`Shared with ${r.added} ${r.added === 1 ? "person" : "people"}`)
      setPicked([])
      qc.invalidateQueries({ queryKey: ["grants"] })
    } catch (e) {
      toast.error((e as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const revoke = async (id: string) => {
    await api.deletePlace(id).catch((e) => toast.error(e.message))
    qc.invalidateQueries({ queryKey: ["grants"] })
  }

  if (!target.isDir) return <p className="py-6 text-sm text-muted-foreground">Share a folder to give people ongoing access. For a single file, use a link.</p>
  return (
    <div className="space-y-4">
      <p className="text-sm text-muted-foreground">People you add see this folder under “Shared with me”. Access ends if you lose access yourself.</p>
      <Input placeholder="Find people…" value={filter} onChange={(e) => setFilter(e.target.value)} />
      <div className="max-h-44 overflow-y-auto rounded-xl border p-1">
        {users.length === 0 ? (
          <p className="p-4 text-center text-sm text-muted-foreground">{dir.isLoading ? "Loading…" : "Nobody else to add."}</p>
        ) : (
          users.map((u) => (
            <label key={u.id} className="flex cursor-pointer items-center gap-3 rounded-lg px-2.5 py-2 hover:bg-muted">
              <Checkbox checked={picked.includes(u.id)} onCheckedChange={(v) => setPicked(v ? [...picked, u.id] : picked.filter((x) => x !== u.id))} />
              <span className="flex size-7 items-center justify-center rounded-full bg-secondary text-xs font-semibold">{u.username.slice(0, 2).toUpperCase()}</span>
              <span className="text-sm">{u.username}</span>
            </label>
          ))
        )}
      </div>
      <div className="flex flex-wrap gap-x-5 gap-y-2">
        {([
          ["canWrite", "Can edit and upload", place?.canWrite],
          ["canDelete", "Can delete", place?.canDelete],
          ["canShare", "Can reshare", true],
        ] as const).map(([k, label, allowed]) => (
          <div key={k} className="flex items-center gap-2">
            <Checkbox id={k} disabled={!allowed} checked={perm[k]} onCheckedChange={(v) => setPerm({ ...perm, [k]: v === true })} />
            <Label htmlFor={k} className="font-normal">
              {label}
            </Label>
          </div>
        ))}
      </div>
      <Button onClick={share} disabled={!picked.length || busy}>
        {busy && <Spinner />} Share with {picked.length || ""} {picked.length === 1 ? "person" : "people"}
      </Button>
      {existing.length > 0 && (
        <div className="space-y-1.5">
          <div className="text-xs font-semibold tracking-wide text-muted-foreground uppercase">Has access</div>
          {existing.map((g) => (
            <div key={g.id} className="flex items-center gap-3 rounded-lg border px-3 py-2">
              <span className="flex size-7 items-center justify-center rounded-full bg-secondary text-xs font-semibold">{g.username.slice(0, 2).toUpperCase()}</span>
              <span className="flex-1 text-sm">{g.username}</span>
              <span className="text-xs text-muted-foreground">{g.canWrite ? "Can edit" : "Can view"}</span>
              <Button variant="ghost" size="icon-xs" onClick={() => revoke(g.id)} aria-label={`Remove ${g.username}`}>
                <Trash2Icon />
              </Button>
            </div>
          ))}
        </div>
      )}
      <DialogFooter>
        <Button variant="outline" onClick={onDone}>
          Done
        </Button>
      </DialogFooter>
    </div>
  )
}

/**
 * Share an item by link or with people, or edit an existing link (`share`).
 */
export function ShareDialog({
  open,
  target,
  share,
  canUpload,
  onClose,
}: {
  open: boolean
  target?: ShareTarget
  share?: Share
  canUpload: boolean
  onClose: () => void
}) {
  const qc = useQueryClient()
  const days = 14
  const links = useQuery({
    queryKey: ["shares", "for", target?.collection ?? target?.place, target?.path],
    queryFn: async () =>
      target!.collection
        ? (await api.shares()).filter((s) => s.collection === target!.collection)
        : api.shares({ place: target!.place, path: target!.path }),
    enabled: open && !!target && !share,
  })
  const [mode, setMode] = useState<"list" | "form" | "done">("list")
  const [editing, setEditing] = useState<Share | undefined>(share)
  const [form, setForm] = useState<Form>(blankForm(days))
  const [created, setCreated] = useState<{ share: Share; invited: number } | null>(null)
  const [busy, setBusy] = useState(false)
  const [tab, setTab] = useState("link")

  useEffect(() => {
    if (!open) return
    setCreated(null)
    setTab("link")
    if (share) {
      setEditing(share)
      setForm(formFrom(share))
      setMode("form")
    } else {
      setEditing(undefined)
      setForm(blankForm(days))
      setMode("list")
    }
  }, [open, share])

  const existing = useMemo(() => links.data ?? [], [links.data])
  useEffect(() => {
    if (open && !share && links.isSuccess && existing.length === 0) setMode("form")
  }, [open, share, links.isSuccess, existing.length])

  const isDir = share ? share.isDir : !!target?.isDir
  const isCollection = share ? !!share.collection : !!target?.collection
  const name = share ? share.title || share.name : target?.name
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["shares"] })
  }

  const submit = async () => {
    const input = toInput(form, isDir, editing)
    if (typeof input === "string") return toast.error(input)
    setBusy(true)
    try {
      if (editing) {
        await api.updateShare(editing.id, input)
        toast.success("Link updated")
        refresh()
        if (share) onClose()
        else setMode("list")
      } else {
        const where = target!.collection ? { collection: target!.collection } : { place: target!.place, path: target!.path }
        const result = await api.createShare({ ...input, ...where })
        refresh()
        setCreated({ share: result, invited: form.emails.length })
        setMode("done")
        navigator.clipboard?.writeText(result.url).then(() => toast.success("Link created and copied"), () => toast.success("Link created"))
      }
    } catch (e) {
      toast.error((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const linkBody =
    mode === "done" && created ? (
      <>
        <LinkResult share={created.share} invited={created.invited} />
        <DialogFooter>
          <Button variant="outline" onClick={() => setMode("list")}>
            All links
          </Button>
          <Button onClick={onClose}>Done</Button>
        </DialogFooter>
      </>
    ) : mode === "list" && !share ? (
      <>
        <div className="space-y-2">
          {links.isLoading ? (
            <Spinner />
          ) : (
            existing.map((s) => (
              <LinkRow
                key={s.id}
                share={s}
                onChanged={refresh}
                onEdit={() => {
                  setEditing(s)
                  setForm(formFrom(s))
                  setMode("form")
                }}
              />
            ))
          )}
        </div>
        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => {
              setEditing(undefined)
              setForm(blankForm(days))
              setMode("form")
            }}
          >
            <PlusIcon /> New link
          </Button>
          <Button onClick={onClose}>Done</Button>
        </DialogFooter>
      </>
    ) : (
      <>
        <div className="-mx-1 max-h-[60svh] overflow-y-auto px-1">
          <LinkForm
            isDir={isDir}
            canUpload={canUpload}
            form={form}
            setForm={setForm}
            editing={editing}
            versioned={target?.versioned || editing?.latest}
            collection={isCollection}
          />
          {editing && (
            <div className="mt-4 border-t pt-4">
              <Recipients share={editing} onChanged={(s) => (setEditing(s), refresh())} />
            </div>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => (share || !existing.length ? onClose() : setMode("list"))}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={busy}>
            {busy && <Spinner />}
            {editing ? (
              "Save link"
            ) : form.emails.length ? (
              <>
                <SendIcon /> Create and send
              </>
            ) : (
              <>
                <LinkIcon /> Create link
              </>
            )}
          </Button>
        </DialogFooter>
      </>
    )

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="min-w-0 sm:max-w-xl">
        <DialogHeader>
          <DialogTitle className="truncate pr-6">{share ? "Edit link" : `Share “${name}”`}</DialogTitle>
          <DialogDescription>
            {isCollection
              ? "Links open the items in this collection, wherever they live, and nothing else."
              : isDir
                ? "Links open this folder and everything inside it, never anything outside."
                : "Links open this one file."}
          </DialogDescription>
        </DialogHeader>
        {share || !target?.isDir || target.collection ? (
          linkBody
        ) : (
          <Tabs value={tab} onValueChange={setTab}>
            <TabsList className="w-full">
              <TabsTrigger value="link">
                <LinkIcon /> Link
              </TabsTrigger>
              <TabsTrigger value="people">
                <UsersIcon /> People
              </TabsTrigger>
            </TabsList>
            <TabsContent value="link" className="mt-3 min-w-0 space-y-4">
              {linkBody}
            </TabsContent>
            <TabsContent value="people" className="mt-3">
              <PeopleTab target={target} onDone={onClose} />
            </TabsContent>
          </Tabs>
        )}
      </DialogContent>
    </Dialog>
  )
}
