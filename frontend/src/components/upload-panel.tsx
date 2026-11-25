import { useState } from "react"
import { CheckIcon, ChevronDownIcon, ChevronUpIcon, RotateCwIcon, UploadIcon, XIcon } from "lucide-react"

import { FileIcon } from "@/components/file-icon"
import { Button } from "@/components/ui/button"
import { Progress } from "@/components/ui/progress"
import { formatBytes } from "@/lib/format"
import { cancel, clearFinished, retry, useUploads } from "@/lib/upload"

export function UploadPanel() {
  const items = useUploads()
  const [open, setOpen] = useState(true)
  if (!items.length) return null

  const active = items.filter((i) => i.status === "uploading" || i.status === "queued")
  const total = items.reduce((n, i) => n + i.file.size, 0)
  const loaded = items.reduce((n, i) => n + (i.status === "done" ? i.file.size : i.loaded), 0)
  const failed = items.filter((i) => i.status === "error").length

  return (
    <div className="fixed right-3 bottom-[calc(var(--tabbar-h)+env(safe-area-inset-bottom)+0.5rem)] left-3 z-50 overflow-hidden rounded-2xl border bg-popover text-popover-foreground shadow-lg md:right-[calc(env(safe-area-inset-right)+1rem)] md:bottom-[calc(env(safe-area-inset-bottom)+1rem)] md:left-auto md:w-[min(24rem,calc(100vw-2rem))] md:rounded-xl">
      <div className="flex items-center gap-2 border-b px-3 py-2">
        <UploadIcon className="size-4 text-muted-foreground" />
        <div className="flex-1 text-sm font-medium">
          {active.length
            ? `Uploading ${active.length} file${active.length === 1 ? "" : "s"}`
            : failed
              ? `${failed} upload${failed === 1 ? "" : "s"} failed`
              : "Uploads complete"}
          <span className="ml-2 text-xs font-normal text-muted-foreground">
            {formatBytes(loaded)} / {formatBytes(total)}
          </span>
        </div>
        <Button variant="ghost" size="icon-sm" onClick={() => setOpen(!open)} aria-label={open ? "Collapse" : "Expand"}>
          {open ? <ChevronDownIcon /> : <ChevronUpIcon />}
        </Button>
        {!active.length && (
          <Button variant="ghost" size="icon-sm" onClick={clearFinished} aria-label="Close">
            <XIcon />
          </Button>
        )}
      </div>
      {!open ? (
        <Progress value={total ? (loaded / total) * 100 : 0} className="rounded-none" />
      ) : (
        <ul className="max-h-[40dvh] divide-y overflow-y-auto overscroll-contain md:max-h-72">
          {items.map((it) => (
            <li key={it.key} className="flex items-center gap-3 px-3 py-2">
              <FileIcon entry={{ name: it.file.name, isDir: false }} className="size-4 shrink-0" />
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm" title={it.name}>
                  {it.name}
                </div>
                {it.status === "error" ? (
                  <div className="truncate text-xs text-destructive">{it.error}</div>
                ) : it.status === "done" ? (
                  <div className="text-xs text-muted-foreground">{it.skipped ? "Already there, skipped" : formatBytes(it.file.size)}</div>
                ) : (
                  <Progress value={it.file.size ? (it.loaded / it.file.size) * 100 : 0} className="mt-1.5" />
                )}
              </div>
              {it.status === "done" && <CheckIcon className="size-4 text-emerald-500" />}
              {it.status === "error" && (
                <Button variant="ghost" size="icon-xs" onClick={() => retry(it.key)} aria-label="Retry">
                  <RotateCwIcon />
                </Button>
              )}
              {(it.status === "uploading" || it.status === "queued" || it.status === "error") && (
                <Button variant="ghost" size="icon-xs" onClick={() => cancel(it.key)} aria-label="Cancel">
                  <XIcon />
                </Button>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
