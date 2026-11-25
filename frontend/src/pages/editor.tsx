import { lazy, Suspense, useCallback, useEffect, useRef, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { ArrowLeftIcon, CheckIcon, DownloadIcon, EyeIcon, Loader2Icon, SaveIcon } from "lucide-react"
import { basicSetup, EditorView } from "codemirror"
import { indentWithTab } from "@codemirror/commands"
import { HighlightStyle, LanguageDescription, syntaxHighlighting } from "@codemirror/language"
import { languages } from "@codemirror/language-data"
import { Compartment, EditorState } from "@codemirror/state"
import { keymap } from "@codemirror/view"
import { tags as t } from "@lezer/highlight"
import { useNavigate, useParams } from "react-router"
import { toast } from "sonner"

import { FileTile } from "@/components/file-icon"
import { EmptyState, Spinner } from "@/components/states"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { Toggle } from "@/components/ui/toggle"
import { api, ApiError } from "@/lib/api"
import { baseName, extOf, filesUrl, formatBytes, parentPath, relativeTime } from "@/lib/format"

const MarkdownView = lazy(() => import("@/components/office-preview").then((m) => ({ default: m.MarkdownView })))
const CsvTable = lazy(() => import("@/components/office-preview").then((m) => ({ default: m.CsvTable })))

const highlight = HighlightStyle.define([
  { tag: [t.keyword, t.operatorKeyword, t.modifier, t.controlKeyword], color: "var(--hl-keyword)" },
  { tag: [t.string, t.special(t.string), t.regexp], color: "var(--hl-string)" },
  { tag: [t.number, t.bool, t.null, t.atom], color: "var(--hl-number)" },
  { tag: [t.comment, t.blockComment, t.lineComment], color: "var(--hl-comment)", fontStyle: "italic" },
  { tag: [t.propertyName, t.attributeName, t.variableName], color: "var(--hl-name)" },
  { tag: [t.typeName, t.className, t.tagName], color: "var(--hl-type)" },
  { tag: [t.heading, t.heading1, t.heading2, t.heading3], color: "var(--hl-heading)", fontWeight: "700" },
  { tag: [t.link, t.url], color: "var(--hl-link)", textDecoration: "underline" },
  { tag: [t.meta, t.processingInstruction, t.punctuation], color: "var(--hl-meta)" },
  { tag: t.emphasis, fontStyle: "italic" },
  { tag: t.strong, fontWeight: "700" },
])

const theme = EditorView.theme({
  "&": { height: "100%", backgroundColor: "var(--background)", color: "var(--foreground)", fontSize: "13.5px" },
  "&.cm-focused": { outline: "none" },
  ".cm-scroller": { fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace", lineHeight: "1.6" },
  ".cm-content": { caretColor: "var(--foreground)", padding: "12px 0" },
  ".cm-gutters": { backgroundColor: "var(--background)", color: "var(--muted-foreground)", border: "none" },
  ".cm-activeLine, .cm-activeLineGutter": { backgroundColor: "color-mix(in oklab, var(--muted) 60%, transparent)" },
  "&.cm-focused .cm-cursor": { borderLeftColor: "var(--foreground)" },
  "&.cm-focused .cm-selectionBackground, .cm-selectionBackground, ::selection": {
    backgroundColor: "color-mix(in oklab, var(--tint) 25%, transparent) !important",
  },
  ".cm-panels": { backgroundColor: "var(--muted)", color: "var(--foreground)" },
  ".cm-searchMatch": { backgroundColor: "color-mix(in oklab, var(--chart-3) 35%, transparent)" },
  ".cm-tooltip": { backgroundColor: "var(--popover)", border: "1px solid var(--border)" },
})

type Doc = { content: string; etag: string; size: number; modTime: number; writable: boolean }

export default function EditorPage() {
  const params = useParams()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const place = params.place ?? ""
  const path = "/" + (params["*"] ?? "")
  const name = baseName(path)
  const ext = extOf(name)
  const previewable = ext === "md" || ext === "markdown" || ext === "csv" || ext === "tsv"

  const host = useRef<HTMLDivElement>(null)
  const view = useRef<EditorView | null>(null)
  const lang = useRef(new Compartment())
  const readOnly = useRef(new Compartment())
  const [doc, setDoc] = useState<Doc | null>(null)
  const [error, setError] = useState<ApiError | null>(null)
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const [text, setText] = useState("")
  const [preview, setPreview] = useState(previewable)
  const [conflict, setConflict] = useState(false)
  const etag = useRef("")

  useEffect(() => {
    let cancelled = false
    api.readText(place, path).then(
      (d) => {
        if (cancelled) return
        etag.current = d.etag
        setDoc(d)
        setText(d.content)
      },
      (e) => !cancelled && setError(e),
    )
    return () => {
      cancelled = true
    }
  }, [place, path])

  const save = useCallback(async () => {
    if (!view.current || !doc?.writable || saving) return
    setSaving(true)
    try {
      const r = await api.writeText({ place, path, content: view.current.state.doc.toString(), etag: etag.current })
      etag.current = r.etag
      setDirty(false)
      qc.invalidateQueries({ queryKey: ["list", place] })
      qc.invalidateQueries({ queryKey: ["recent"] })
      setDoc((d) => d && { ...d, modTime: r.modTime, size: r.size })
    } catch (e) {
      if (e instanceof ApiError && e.code === "changed") setConflict(true)
      else toast.error((e as Error).message)
    } finally {
      setSaving(false)
    }
  }, [doc?.writable, place, path, qc, saving])

  const saveRef = useRef(save)
  saveRef.current = save

  // Create the editor once the document has loaded.
  useEffect(() => {
    if (!doc || !host.current || view.current) return
    const v = new EditorView({
      parent: host.current,
      state: EditorState.create({
        doc: doc.content,
        extensions: [
          basicSetup,
          keymap.of([indentWithTab, { key: "Mod-s", preventDefault: true, run: () => (saveRef.current(), true) }]),
          EditorView.lineWrapping,
          theme,
          syntaxHighlighting(highlight),
          lang.current.of([]),
          readOnly.current.of(EditorState.readOnly.of(!doc.writable)),
          EditorView.updateListener.of((u) => {
            if (u.docChanged) {
              setDirty(true)
              setText(u.state.doc.toString())
            }
          }),
        ],
      }),
    })
    view.current = v
    const desc = LanguageDescription.matchFilename(languages, name)
    desc?.load().then((support) => v.dispatch({ effects: lang.current.reconfigure(support) }))
    v.focus()
    return () => {
      v.destroy()
      view.current = null
    }
  }, [doc, name])

  useEffect(() => {
    const warn = (e: BeforeUnloadEvent) => {
      if (dirty) e.preventDefault()
    }
    window.addEventListener("beforeunload", warn)
    return () => window.removeEventListener("beforeunload", warn)
  }, [dirty])

  const back = () => {
    if (dirty && !window.confirm("You have unsaved changes. Leave without saving?")) return
    navigate(filesUrl(place, parentPath(path)))
  }

  const overwrite = async () => {
    setConflict(false)
    const latest = await api.readText(place, path)
    etag.current = latest.etag
    await save()
  }

  const reload = async () => {
    setConflict(false)
    const latest = await api.readText(place, path)
    etag.current = latest.etag
    view.current?.dispatch({ changes: { from: 0, to: view.current.state.doc.length, insert: latest.content } })
    setDirty(false)
  }

  const saveCopy = async () => {
    setConflict(false)
    const dot = name.lastIndexOf(".")
    const copyName = dot > 0 ? `${name.slice(0, dot)} (my copy)${name.slice(dot)}` : `${name} (my copy)`
    const p = parentPath(path) === "/" ? `/${copyName}` : `${parentPath(path)}/${copyName}`
    try {
      await api.writeText({ place, path: p, content: view.current!.state.doc.toString(), create: true })
      setDirty(false)
      toast.success(`Saved as “${copyName}”`)
      navigate(`/edit/${encodeURIComponent(place)}${p.split("/").map(encodeURIComponent).join("/")}`, { replace: true })
    } catch (e) {
      toast.error((e as Error).message)
    }
  }

  if (error) {
    return (
      <div className="flex min-h-full items-center justify-center p-6">
        <EmptyState
          icon={<FileTile entry={{ name, isDir: false }} />}
          title={error.code === "binary" ? "This file isn't text" : error.code === "too_large" ? "Too large to edit here" : "Could not open the file"}
          description={error.message}
        >
          <Button variant="outline" onClick={() => navigate(filesUrl(place, parentPath(path)))}>
            Back to folder
          </Button>
        </EmptyState>
      </div>
    )
  }

  return (
    <div className="flex h-svh flex-col pb-[env(safe-area-inset-bottom)] md:h-[calc(100svh-1rem)] md:pb-0">
      <header className="box-content flex h-14 shrink-0 items-center gap-2 border-b px-3 pt-[env(safe-area-inset-top)] md:pt-0">
        <Button variant="ghost" size="icon-sm" onClick={back} aria-label="Back to folder">
          <ArrowLeftIcon />
        </Button>
        <FileTile entry={{ name, isDir: false }} size="sm" />
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-semibold">{name}</div>
          <div className="text-xs text-muted-foreground">
            {!doc ? "Loading…" : !doc.writable ? "Read only" : saving ? "Saving…" : dirty ? "Unsaved changes" : `Saved · ${formatBytes(doc.size)} · ${relativeTime(doc.modTime)}`}
          </div>
        </div>
        {previewable && (
          <Toggle variant="outline" size="sm" pressed={preview} onPressedChange={setPreview} aria-label="Toggle preview">
            <EyeIcon /> <span className="hidden sm:inline">Preview</span>
          </Toggle>
        )}
        <Button variant="ghost" size="icon-sm" asChild aria-label="Download">
          <a href={api.downloadUrl(place, path)} download>
            <DownloadIcon />
          </a>
        </Button>
        {doc?.writable && (
          <Button size="sm" onClick={save} disabled={!dirty || saving}>
            {saving ? <Loader2Icon className="animate-spin" /> : dirty ? <SaveIcon /> : <CheckIcon />}
            <span className="hidden sm:inline">{dirty ? "Save" : "Saved"}</span>
          </Button>
        )}
      </header>
      <div className="flex min-h-0 flex-1">
        <div className={preview ? "hidden min-w-0 flex-1 md:block" : "min-w-0 flex-1"}>
          {!doc && (
            <div className="flex h-full items-center justify-center">
              <Spinner />
            </div>
          )}
          <div ref={host} className="h-full" />
        </div>
        {preview && (
          <div className="min-w-0 flex-1 overflow-auto border-l bg-background">
            <Suspense fallback={<Spinner />}>
              {ext === "csv" || ext === "tsv" ? <CsvTable text={text} /> : <MarkdownView text={text} className="mx-auto max-w-3xl px-8 py-6" />}
            </Suspense>
          </div>
        )}
      </div>
      <AlertDialog open={conflict} onOpenChange={setConflict}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>“{name}” changed while you were editing</AlertDialogTitle>
            <AlertDialogDescription>
              Someone else (or another device, Finder or SMB) saved a newer version. Choose what to keep.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter className="sm:justify-between">
            <AlertDialogCancel onClick={reload}>Discard mine and reload</AlertDialogCancel>
            <div className="flex gap-2">
              <Button variant="outline" onClick={saveCopy}>
                Save mine as a copy
              </Button>
              <AlertDialogAction variant="destructive" onClick={overwrite}>
                Overwrite theirs
              </AlertDialogAction>
            </div>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
