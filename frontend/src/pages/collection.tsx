import { useEffect, useState } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { LibraryIcon, Link2Icon, PencilIcon, Trash2Icon, XIcon } from "lucide-react"
import { Link, useNavigate, useParams } from "react-router"
import { toast } from "sonner"

import { ItemList } from "@/components/item-list"
import { Page } from "@/components/page"
import { ShareDialog } from "@/components/share-dialog"
import { CenteredSpinner, EmptyState } from "@/components/states"
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
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Field, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { api, type Share } from "@/lib/api"
import { formatDay } from "@/lib/format"

export default function CollectionPage() {
  const { id = "" } = useParams()
  const qc = useQueryClient()
  const navigate = useNavigate()
  const cols = useQuery({ queryKey: ["collections"], queryFn: api.collections })
  const links = useQuery({ queryKey: ["shares"], queryFn: () => api.shares() })
  const c = cols.data?.find((x) => x.id === id)
  const [sharing, setSharing] = useState(false)
  const [editingLink, setEditingLink] = useState<Share | undefined>()
  const [editing, setEditing] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [name, setName] = useState("")
  const [desc, setDesc] = useState("")
  useEffect(() => {
    if (c && editing) {
      setName(c.name)
      setDesc(c.description)
    }
  }, [c, editing])

  if (cols.isLoading) return <CenteredSpinner />
  if (!c)
    return (
      <Page title="Collection not found">
        <EmptyState icon={<LibraryIcon />} title="This collection doesn't exist" description="It may have been deleted.">
          <Button variant="outline" asChild>
            <Link to="/collections">All collections</Link>
          </Button>
        </EmptyState>
      </Page>
    )

  const myLinks = (links.data ?? []).filter((s) => s.collection === c.id)
  const refresh = () => qc.invalidateQueries({ queryKey: ["collections"] })
  const remove = async (place: string, path: string) => {
    try {
      await api.removeFromCollection(c.id, place, path)
      refresh()
    } catch (e) {
      toast.error((e as Error).message)
    }
  }
  const save = async () => {
    try {
      await api.updateCollection(c.id, { name, description: desc })
      refresh()
      setEditing(false)
    } catch (e) {
      toast.error((e as Error).message)
    }
  }

  return (
    <Page
      title={c.name}
      description={c.description || `${c.items.length} ${c.items.length === 1 ? "item" : "items"}`}
      parents={[{ label: "Collections", to: "/collections" }]}
      actions={
        <>
          <Button variant="outline" size="icon" onClick={() => setEditing(true)} aria-label="Rename">
            <PencilIcon />
          </Button>
          <Button variant="outline" size="icon" onClick={() => setDeleting(true)} aria-label="Delete collection">
            <Trash2Icon />
          </Button>
          <Button onClick={() => setSharing(true)} disabled={!c.items.length}>
            <Link2Icon /> Share
          </Button>
        </>
      }
    >
      {myLinks.length > 0 && (
        <div className="mb-4 flex flex-wrap items-center gap-2 rounded-xl border bg-card px-3 py-2 text-sm">
          <Link2Icon className="size-4 text-muted-foreground" />
          {myLinks.map((s) => (
            <button key={s.id} type="button" onClick={() => setEditingLink(s)} className="flex items-center gap-1.5 rounded-md px-1.5 py-0.5 hover:bg-muted">
              <span className="max-w-48 truncate font-mono text-xs">{s.url.replace(/^https?:\/\//, "")}</span>
              {!s.active && <Badge variant="destructive">{s.disabled ? "Paused" : "Expired"}</Badge>}
              {s.expiresAt > 0 && <span className="text-xs text-muted-foreground">until {formatDay(s.expiresAt)}</span>}
            </button>
          ))}
        </div>
      )}
      <ItemList
        items={c.items}
        actions={(it) => (
          <Button variant="ghost" size="icon-sm" onClick={() => remove(it.place, it.path)} aria-label={`Remove ${it.name} from the collection`}>
            <XIcon />
          </Button>
        )}
        empty={
          <div className="rounded-xl border border-dashed bg-card/50 py-8">
            <EmptyState icon={<LibraryIcon />} title="Nothing here yet" description="In any folder, right-click files or folders and choose “Add to collection…”.">
              <Button variant="outline" onClick={() => navigate("/files")}>
                Browse files
              </Button>
            </EmptyState>
          </div>
        }
      />

      <ShareDialog
        open={sharing || !!editingLink}
        target={sharing ? { place: "", path: "", name: c.name, isDir: true, collection: c.id } : undefined}
        share={editingLink}
        canUpload={false}
        onClose={() => {
          setSharing(false)
          setEditingLink(undefined)
          qc.invalidateQueries({ queryKey: ["collections"] })
        }}
      />
      <Dialog open={editing} onOpenChange={setEditing}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>Edit collection</DialogTitle>
          </DialogHeader>
          <Field>
            <FieldLabel htmlFor="cname">Name</FieldLabel>
            <Input id="cname" value={name} maxLength={120} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field>
            <FieldLabel htmlFor="cdesc">Description</FieldLabel>
            <Textarea id="cdesc" rows={3} maxLength={2000} value={desc} placeholder="Shown on the link's page" onChange={(e) => setDesc(e.target.value)} />
          </Field>
          <DialogFooter>
            <Button variant="outline" onClick={() => setEditing(false)}>
              Cancel
            </Button>
            <Button onClick={save} disabled={!name.trim()}>
              Save
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <AlertDialog open={deleting} onOpenChange={setDeleting}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete “{c.name}”?</AlertDialogTitle>
            <AlertDialogDescription>
              The files stay where they are. {myLinks.length ? "Links to this collection stop working." : ""}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={async () => {
                await api.deleteCollection(c.id).catch((e) => toast.error(e.message))
                qc.invalidateQueries({ queryKey: ["collections"] })
                qc.invalidateQueries({ queryKey: ["shares"] })
                navigate("/collections")
              }}
            >
              Delete
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Page>
  )
}
