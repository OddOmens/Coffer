import type { ReactNode } from "react"

import type { Action } from "@/components/file-list"
import { Sheet, SheetClose, SheetContent, SheetDescription, SheetTitle } from "@/components/ui/sheet"
import { cn } from "@/lib/utils"

/**
 * iOS-style action sheet: a card of actions above a separate Cancel button,
 * rising from the bottom of the screen. Phones use it in place of menus.
 */
export function ActionSheet({
  open,
  onOpenChange,
  title,
  description,
  icon,
  actions,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: ReactNode
  description?: ReactNode
  icon?: ReactNode
  actions: Action[]
}) {
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="bottom"
        showCloseButton={false}
        // Touch users don't need a focused first action.
        onOpenAutoFocus={(e) => e.preventDefault()}
        className="gap-2 border-none bg-transparent px-2 pt-0 pb-[calc(env(safe-area-inset-bottom)+0.5rem)] shadow-none select-none"
      >
        <div className="overflow-hidden rounded-2xl bg-popover">
          <div className="flex items-center gap-3 border-b px-4 py-3">
            {icon}
            <div className="min-w-0 flex-1">
              <SheetTitle className="truncate text-[0.9375rem] font-semibold">{title}</SheetTitle>
              <SheetDescription className={cn("truncate text-xs", !description && "sr-only")}>{description ?? "Actions"}</SheetDescription>
            </div>
          </div>
          <div className="max-h-[60dvh] overflow-y-auto overscroll-contain">
            {actions.map((a, i) => (
              <button
                key={a.label}
                type="button"
                onClick={() => {
                  onOpenChange(false)
                  a.onSelect()
                }}
                className={cn(
                  "flex h-12 w-full items-center gap-3 px-4 text-left text-base active:bg-muted [&_svg]:size-5 [&_svg]:shrink-0 [&_svg]:text-muted-foreground",
                  i > 0 && "border-t",
                  a.separator && i > 0 && "border-t-[6px] border-t-muted",
                  a.destructive && "text-destructive [&_svg]:text-destructive",
                )}
              >
                {a.icon}
                <span className="truncate">{a.label}</span>
              </button>
            ))}
          </div>
        </div>
        <SheetClose asChild>
          <button type="button" className="h-12 w-full rounded-2xl bg-popover text-base font-semibold text-brand active:bg-muted">
            Cancel
          </button>
        </SheetClose>
      </SheetContent>
    </Sheet>
  )
}
