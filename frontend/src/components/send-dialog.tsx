import { useEffect, useMemo, useRef, useState, type DragEvent } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { CheckIcon, CopyIcon, FileIcon, PlusIcon, SendIcon, UploadCloudIcon, XIcon } from "lucide-react"
import { toast } from "sonner"

import { usePlaces, useUser } from "@/components/app-layout"
import { EmailsInput, QrButton, useCopy } from "@/components/share-dialog"
import { Spinner } from "@/components/states"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Field, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Progress } from "@/components/ui/progress"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { api, NOTIFY_DOWNLOAD, type Share } from "@/lib/api"
import { formatBytes, formatDay } from "@/lib/format"
import { enqueue, filesFromDrop, filesFromInput, useUploads, type QueueFile } from "@/lib/upload"
import { cn } from "@/lib/utils"

const DAY = 86400

/**
 * Send files in one step, like WeTransfer: pick files and people, and Coffer
 * uploads them to a new folder under Sent, makes a link and emails it.
 */
export function SendDialog({ open, onClose, initial }: { open: boolean; onClose: () => void; initial?: QueueFile[] }) {
  const me = useUser()
  const places = usePlaces()
  const qc = useQueryClient()
  const uploads = useUploads()
  const [files, setFiles] = useState<QueueFile[]>([])
  const [title, setTitle] = useState("")
  const [emails, setEmails] = useState<string[]>([])
  const [message, setMessage] = useState("")
  const [days, setDays] = useState("7")
  const [password, setPassword] = useState("")
  const [usePassword, setUsePassword] = useState(false)
  const [stage, setStage] = useState<"compose" | "uploading" | "done">("compose")
  const [keys, setKeys] = useState<number[]>([])
  const [target, setTarget] = useState<{ place: string; path: string } | null>(null)
  const [share, setShare] = useState<Share | null>(null)
  const [over, setOver] = useState(false)
  const input = useRef<HTMLInputElement>(null)
  const { copied, copy } = useCopy()

  useEffect(() => {
    if (!open) return
    setFiles(initial ?? [])
    setTitle("")
    setEmails([])
    setMessage("")
    setDays("7")
    setPassword("")
    setUsePassword(false)
    setStage("compose")
    setKeys([])
    setTarget(null)
    setShare(null)
  }, [open, initial])

  const home = places.data?.places.find((p) => p.id === places.data?.home)
  const total = files.reduce((n, f) => n + f.file.size, 0)
  const mine = uploads.filter((u) => keys.includes(u.key))
  const loaded = mine.reduce((n, u) => n + (u.status === "done" ? u.file.size : u.loaded), 0)
  const failed = mine.filter((u) => u.status === "error" || u.status === "canceled")
  const finished = keys.length > 0 && mine.length === keys.length && mine.every((u) => u.status === "done")
  const autoTitle = useMemo(() => {
    if (!files.length) return ""
    const first = files[0].name.split("/")[0]
    return files.length === 1 ? first : `${first} and ${files.length - 1} more`
  }, [files])

  const addFiles = (list: QueueFile[]) => setFiles((f) => [...f, ...list.filter((n) => !f.some((x) => x.name === n.name))])

  const start = async () => {
    if (!home?.canWrite) return toast.error("You need a home folder you can write to")
    if (usePassword && password.length < 10) return toast.error("Passwords need at least 10 characters")
    const name = (title.trim() || autoTitle).replace(/[/\\]/g, "-").slice(0, 80)
    const stamp = new Date().toISOString().slice(0, 10)
    try {
      const root = await api.list(home.id, "/")
      if (!root.entries.some((e) => e.isDir && e.name === "Sent")) await api.mkdir(home.id, "/", "Sent")
      const taken = new Set((await api.list(home.id, "/Sent")).entries.map((e) => e.name))
      let n = `${stamp} ${name}`
      for (let i = 2; taken.has(n); i++) n = `${stamp} ${name} (${i})`
      const folder = (await api.mkdir(home.id, "/Sent", n)).path
      setTarget({ place: home.id, path: folder })
      setStage("uploading")
      setKeys(enqueue(files, { dir: folder, base: "/api/uploads", place: home.id }))
    } catch (e) {
      toast.error((e as Error).message)
    }
  }

  // Once every file is up, make the link and send it.
  useEffect(() => {
    if (stage !== "uploading" || !finished || !target || share) return
    const exp = Number(days) ? Math.floor(Date.now() / 1000) + Number(days) * DAY : 0
    api
      .createShare({
        place: target.place,
        path: target.path,
        canView: true,
        canDownload: true,
        notify: NOTIFY_DOWNLOAD,
        expiresAt: exp,
        title: title.trim() || autoTitle,
        message,
        password: usePassword ? password : undefined,
        sent: true,
        ...(emails.length ? { invite: { emails, message } } : {}),
      })
      .then((s) => {
        setShare(s)
        setStage("done")
        qc.invalidateQueries({ queryKey: ["shares"] })
        qc.invalidateQueries({ queryKey: ["list"] })
      })
      .catch((e) => toast.error((e as Error).message))
  }, [stage, finished, target, share]) // eslint-disable-line react-hooks/exhaustive-deps

  const onDrop = async (ev: DragEvent) => {
    ev.preventDefault()
    setOver(false)
    addFiles(await filesFromDrop(ev.dataTransfer))
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && stage !== "uploading" && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{stage === "done" ? "Sent" : "Send files"}</DialogTitle>
          <DialogDescription>
            {stage === "done"
              ? emails.length
                ? `We emailed the link to ${emails.length} ${emails.length === 1 ? "person" : "people"}. You'll be told when they download.`
                : "Here's the link. You'll be told when someone downloads."
              : "Upload files and get a download link, sent by email if you like. Recipients don't need an account."}
          </DialogDescription>
        </DialogHeader>

        {stage === "compose" && (
          <div className="space-y-3">
            <div
              onDragOver={(e) => {
                e.preventDefault()
                setOver(true)
              }}
              onDragLeave={() => setOver(false)}
              onDrop={onDrop}
              className={cn("rounded-lg border border-dashed p-3 transition-colors", over && "border-brand bg-brand/5")}
            >
              {files.length === 0 ? (
                <button type="button" onClick={() => input.current?.click()} className="flex w-full flex-col items-center gap-1.5 py-5 text-sm text-muted-foreground">
                  <UploadCloudIcon className="size-6" />
                  Drop files here or <span className="font-medium text-brand">choose files</span>
                </button>
              ) : (
                <div className="space-y-1">
                  <div className="max-h-40 space-y-0.5 overflow-y-auto">
                    {files.map((f) => (
                      <div key={f.name} className="flex items-center gap-2 rounded px-1 py-0.5 text-sm">
                        <FileIcon className="size-3.5 shrink-0 text-muted-foreground" />
                        <span className="min-w-0 flex-1 truncate">{f.name}</span>
                        <span className="shrink-0 text-xs text-muted-foreground tabular-nums">{formatBytes(f.file.size)}</span>
                        <button type="button" onClick={() => setFiles(files.filter((x) => x !== f))} aria-label={`Remove ${f.name}`} className="text-muted-foreground hover:text-foreground">
                          <XIcon className="size-3.5" />
                        </button>
                      </div>
                    ))}
                  </div>
                  <div className="flex items-center justify-between border-t pt-1.5 text-xs text-muted-foreground">
                    <span>
                      {files.length} {files.length === 1 ? "file" : "files"} · {formatBytes(total)}
                    </span>
                    <Button variant="ghost" size="xs" onClick={() => input.current?.click()}>
                      <PlusIcon /> Add more
                    </Button>
                  </div>
                </div>
              )}
            </div>
            <Field>
              <FieldLabel htmlFor="send-title">Title</FieldLabel>
              <Input id="send-title" placeholder={autoTitle || "e.g. Acme final renders"} value={title} maxLength={80} onChange={(e) => setTitle(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel>Send to</FieldLabel>
              {me.mailReady ? (
                <EmailsInput value={emails} onChange={setEmails} placeholder="Email addresses (optional)" />
              ) : (
                <p className="text-xs text-muted-foreground">Email isn't set up, so you'll get a link to send yourself.</p>
              )}
            </Field>
            <Textarea rows={2} maxLength={2000} placeholder="Message (optional)" value={message} onChange={(e) => setMessage(e.target.value)} />
            <div className="grid grid-cols-2 gap-3">
              <Field>
                <FieldLabel>Link expires</FieldLabel>
                <Select value={days} onValueChange={setDays}>
                  <SelectTrigger className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {[["1", "1 day"], ["7", "7 days"], ["14", "14 days"], ["30", "30 days"], ["0", "Never"]].map(([v, l]) => (
                      <SelectItem key={v} value={v}>
                        {l}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              <Field>
                <FieldLabel htmlFor="send-pw" className="flex items-center justify-between">
                  Password <Switch checked={usePassword} onCheckedChange={setUsePassword} aria-label="Require a password" />
                </FieldLabel>
                <Input id="send-pw" type="password" autoComplete="new-password" disabled={!usePassword} placeholder={usePassword ? "At least 10 characters" : "Off"} value={password} onChange={(e) => setPassword(e.target.value)} />
              </Field>
            </div>
            <p className="text-xs text-muted-foreground">Files go to Sent in {home?.name ?? "your files"}, so you can find them later.</p>
          </div>
        )}

        {stage === "uploading" && (
          <div className="space-y-3 py-2">
            <div className="flex items-center justify-between text-sm">
              <span>{finished ? "Creating the link…" : `Uploading ${files.length} ${files.length === 1 ? "file" : "files"}…`}</span>
              <span className="text-muted-foreground tabular-nums">
                {formatBytes(loaded)} of {formatBytes(total)}
              </span>
            </div>
            <Progress value={total ? (loaded / total) * 100 : 0} />
            {failed.length > 0 && (
              <p className="text-sm text-destructive">
                {failed.length} {failed.length === 1 ? "file" : "files"} failed. Retry from the uploads panel, then the link is created.
              </p>
            )}
            <p className="text-xs text-muted-foreground">You can close other windows, but keep this tab open until it finishes.</p>
          </div>
        )}

        {stage === "done" && share && (
          <div className="space-y-2">
            <div className="flex items-center gap-1.5">
              <Input readOnly value={share.url} className="font-mono text-xs" onFocus={(e) => e.target.select()} />
              <Button variant="outline" size="icon" onClick={() => copy(share.url)} aria-label="Copy link">
                {copied ? <CheckIcon /> : <CopyIcon />}
              </Button>
              <QrButton url={share.url} size="icon" />
            </div>
            <p className="text-xs text-muted-foreground">
              {share.expiresAt ? `Works until ${formatDay(share.expiresAt)}.` : "Never expires."} {share.hasPassword ? "Send the password separately." : ""}
            </p>
          </div>
        )}

        <DialogFooter>
          {stage === "compose" && (
            <>
              <Button variant="outline" onClick={onClose}>
                Cancel
              </Button>
              <Button onClick={start} disabled={!files.length}>
                <SendIcon /> {emails.length ? "Upload and send" : "Upload and get link"}
              </Button>
            </>
          )}
          {stage === "uploading" && (
            <Button disabled>
              <Spinner /> Working…
            </Button>
          )}
          {stage === "done" && <Button onClick={onClose}>Done</Button>}
        </DialogFooter>
        <input
          ref={input}
          type="file"
          multiple
          hidden
          onChange={(e) => {
            if (e.target.files) addFiles(filesFromInput(e.target.files))
            e.target.value = ""
          }}
        />
      </DialogContent>
    </Dialog>
  )
}
