import { VaultIcon } from "lucide-react"

import { useConfig } from "@/App"
import { cn } from "@/lib/utils"

export function BrandMark({ className }: { className?: string }) {
  const { data } = useConfig()
  if (data?.hasLogo) {
    return <img src={`/api/branding/logo?v=${data.logoVersion}`} alt="" className={cn("size-8 rounded-lg object-contain", className)} />
  }
  return (
    <div className={cn("flex size-8 items-center justify-center rounded-lg bg-black text-white dark:ring-1 dark:ring-white/15", className)}>
      <VaultIcon className="size-4.5" />
    </div>
  )
}

export function Brand({ className, small }: { className?: string; small?: boolean }) {
  const { data } = useConfig()
  return (
    <div className={cn("flex items-center", small ? "gap-2 text-sm" : "gap-2.5", className)}>
      <BrandMark className={cn(small && "size-7 rounded-md [&>svg]:size-4")} />
      <span className="truncate font-semibold tracking-tight">{data?.appName ?? "Coffer"}</span>
    </div>
  )
}
