import { useQuery } from "@tanstack/react-query"
import { CircleAlertIcon, TriangleAlertIcon } from "lucide-react"
import { Link } from "react-router"

import { api, type HealthProblem } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"

/** Current server problems, for admins. */
export function useHealth(enabled = true) {
  return useQuery({ queryKey: ["health"], queryFn: api.health, enabled, refetchInterval: 60_000, staleTime: 30_000 })
}

/** One line per problem, each linking to the page that deals with it. Renders nothing when all is well. */
export function HealthList({ problems, className }: { problems?: HealthProblem[]; className?: string }) {
  if (!problems?.length) return null
  return (
    <div className={cn("overflow-hidden rounded-xl border bg-card", className)}>
      {problems.map((p) => {
        const Icon = p.severity === "critical" ? CircleAlertIcon : TriangleAlertIcon
        return (
          <Link
            key={p.key}
            to={p.link}
            title={p.detail}
            className="flex h-10 items-center gap-2.5 border-b px-3 text-sm last:border-b-0 hover:bg-muted"
          >
            <Icon className={cn("size-4 shrink-0", p.severity === "critical" ? "text-destructive" : "text-amber-500")} />
            <span className="min-w-0 truncate font-medium">{p.title}</span>
            {p.detail && <span className="hidden min-w-0 flex-1 truncate text-muted-foreground md:block">{p.detail}</span>}
            <span className="ml-auto shrink-0 pl-2 text-xs text-muted-foreground">{relativeTime(p.since)}</span>
          </Link>
        )
      })}
    </div>
  )
}
