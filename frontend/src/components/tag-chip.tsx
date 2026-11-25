import { cn } from "@/lib/utils"

export const TAG_COLORS = ["gray", "red", "orange", "amber", "green", "teal", "blue", "violet", "pink"] as const

const styles: Record<string, string> = {
  gray: "bg-secondary text-secondary-foreground",
  red: "bg-red-500/12 text-red-700 dark:text-red-300",
  orange: "bg-orange-500/12 text-orange-700 dark:text-orange-300",
  amber: "bg-amber-500/15 text-amber-800 dark:text-amber-300",
  green: "bg-emerald-500/12 text-emerald-700 dark:text-emerald-300",
  teal: "bg-teal-500/12 text-teal-700 dark:text-teal-300",
  blue: "bg-blue-500/12 text-blue-700 dark:text-blue-300",
  violet: "bg-violet-500/12 text-violet-700 dark:text-violet-300",
  pink: "bg-pink-500/12 text-pink-700 dark:text-pink-300",
}

export const swatch: Record<string, string> = {
  gray: "bg-slate-400", red: "bg-red-500", orange: "bg-orange-500", amber: "bg-amber-500", green: "bg-emerald-500",
  teal: "bg-teal-500", blue: "bg-blue-500", violet: "bg-violet-500", pink: "bg-pink-500",
}

/** A small coloured label for a tag. */
export function TagChip({ tag, color, className, onClick }: { tag: string; color?: string; className?: string; onClick?: () => void }) {
  const Comp = onClick ? "button" : "span"
  return (
    <Comp
      type={onClick ? "button" : undefined}
      onClick={onClick}
      className={cn("inline-flex h-[1.125rem] max-w-32 items-center truncate rounded px-1.5 text-[0.6875rem] font-medium", styles[color ?? ""] ?? styles.gray, onClick && "hover:opacity-80", className)}
    >
      {tag}
    </Comp>
  )
}
