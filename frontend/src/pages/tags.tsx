import { useQuery } from "@tanstack/react-query"
import { TagIcon } from "lucide-react"
import { Link, useParams } from "react-router"

import { ItemList } from "@/components/item-list"
import { useTagColors } from "@/components/organize-dialogs"
import { Page } from "@/components/page"
import { EmptyState } from "@/components/states"
import { swatch, TagChip } from "@/components/tag-chip"
import { Skeleton } from "@/components/ui/skeleton"
import { api } from "@/lib/api"
import { cn } from "@/lib/utils"

export default function TagsPage() {
  const { tag } = useParams()
  return tag ? <TagItems tag={tag} /> : <AllTags />
}

function AllTags() {
  const tags = useQuery({ queryKey: ["tags"], queryFn: api.tags })
  return (
    <Page title="Tags" description="Labels your team puts on files and folders, across every drive.">
      {tags.isLoading ? (
        <Skeleton className="h-24 rounded-xl" />
      ) : !tags.data?.length ? (
        <EmptyState icon={<TagIcon />} title="No tags yet" description="Right-click a file or folder and choose “Tags…”." />
      ) : (
        <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
          {tags.data.map((t) => (
            <Link
              key={t.tag}
              to={`/tags/${encodeURIComponent(t.tag)}`}
              className="flex items-center justify-between gap-2 rounded-xl border bg-card px-3 py-2.5 transition-colors hover:border-ring/50"
            >
              <TagChip tag={t.tag} color={t.color} />
              <span className="text-xs text-muted-foreground tabular-nums">
                {t.count} {t.count === 1 ? "item" : "items"}
              </span>
            </Link>
          ))}
        </div>
      )}
    </Page>
  )
}

function TagItems({ tag }: { tag: string }) {
  const items = useQuery({ queryKey: ["tagged", tag], queryFn: () => api.tagged(tag) })
  const color = useTagColors()
  return (
    <Page
      title={
        <span className="flex items-center gap-2.5">
          <span aria-hidden className={cn("size-2.5 shrink-0 rounded-full", swatch[color(tag) ?? "gray"] ?? swatch.gray)} />
          <span className="truncate">{tag}</span>
        </span>
      }
      parents={[{ label: "Tags", to: "/tags" }]}
      crumb={tag}
      description={items.data ? `${items.data.length} ${items.data.length === 1 ? "item" : "items"}` : undefined}
    >
      {items.isLoading ? (
        <Skeleton className="h-40 rounded-xl" />
      ) : (
        <ItemList items={items.data ?? []} empty={<EmptyState icon={<TagIcon />} title="Nothing has this tag" description="It may have been removed." />} />
      )}
    </Page>
  )
}
