import {
  FileArchiveIcon,
  FileAudioIcon,
  FileChartColumnIcon,
  FileCodeIcon,
  FileIcon as GenericFileIcon,
  FileImageIcon,
  FileTextIcon,
  FileTypeIcon,
  FileVideoIcon,
  FolderIcon,
  LayersIcon,
  PresentationIcon,
  SheetIcon,
  type LucideIcon,
} from "lucide-react"

import type { Kind } from "@/lib/api"
import { fileKind } from "@/lib/format"
import { cn } from "@/lib/utils"

const icons: Record<Kind, [LucideIcon, string]> = {
  folder: [FolderIcon, "text-sky-500 fill-sky-500/15"],
  image: [FileImageIcon, "text-emerald-600 dark:text-emerald-400"],
  video: [FileVideoIcon, "text-violet-600 dark:text-violet-400"],
  audio: [FileAudioIcon, "text-pink-600 dark:text-pink-400"],
  pdf: [FileTextIcon, "text-red-600 dark:text-red-400"],
  document: [FileTextIcon, "text-blue-600 dark:text-blue-400"],
  spreadsheet: [SheetIcon, "text-green-600 dark:text-green-400"],
  presentation: [PresentationIcon, "text-orange-600 dark:text-orange-400"],
  text: [FileTextIcon, "text-muted-foreground"],
  code: [FileCodeIcon, "text-cyan-600 dark:text-cyan-400"],
  archive: [FileArchiveIcon, "text-amber-600 dark:text-amber-400"],
  design: [LayersIcon, "text-indigo-600 dark:text-indigo-400"],
  font: [FileTypeIcon, "text-orange-500"],
  other: [GenericFileIcon, "text-muted-foreground"],
}

type E = { name: string; isDir: boolean; kind?: Kind }

export function FileIcon({ entry, className }: { entry: E; className?: string }) {
  const [Icon, color] = icons[fileKind(entry)]
  return <Icon className={cn("size-5", color, className)} />
}

/** The square icon tile used in lists and cards. */
export function FileTile({ entry, className, size = "md" }: { entry: E; className?: string; size?: "sm" | "md" | "lg" }) {
  const [Icon, color] = icons[fileKind(entry)]
  const dims = { sm: "size-8 rounded-lg [&_svg]:size-4", md: "size-9 rounded-lg [&_svg]:size-[1.125rem]", lg: "size-11 rounded-xl [&_svg]:size-[1.375rem]" }[size]
  return (
    <div className={cn("flex shrink-0 items-center justify-center border border-border/80 bg-secondary/70", dims, className)}>
      <Icon className={cn("stroke-[1.75]", fileKind(entry) === "other" || fileKind(entry) === "text" ? "text-foreground/70" : color)} />
    </div>
  )
}

/** Dark tile for quick actions, as in the design. */
export function ActionTile({ icon: Icon, className }: { icon: LucideIcon; className?: string }) {
  return (
    <div className={cn("flex size-8 items-center justify-center rounded-lg bg-tile text-tile-foreground shadow-xs", className)}>
      <Icon className="size-4 stroke-[1.75]" />
    </div>
  )
}

export { FileChartColumnIcon }
