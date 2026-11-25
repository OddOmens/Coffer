import type { ReactNode } from "react"
import { Loader2Icon } from "lucide-react"

import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"

export function CenteredSpinner() {
  return (
    <div className="flex min-h-svh items-center justify-center">
      <Loader2Icon className="size-6 animate-spin text-muted-foreground" />
    </div>
  )
}

export function Spinner({ className = "" }: { className?: string }) {
  return <Loader2Icon className={`size-4 animate-spin ${className}`} />
}

export function EmptyState({
  icon,
  title,
  description,
  children,
}: {
  icon: ReactNode
  title: string
  description?: ReactNode
  children?: ReactNode
}) {
  return (
    <Empty>
      <EmptyHeader>
        <EmptyMedia variant="icon">{icon}</EmptyMedia>
        <EmptyTitle>{title}</EmptyTitle>
        {description && <EmptyDescription>{description}</EmptyDescription>}
      </EmptyHeader>
      {children}
    </Empty>
  )
}
