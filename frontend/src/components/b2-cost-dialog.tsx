import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { CloudIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { api } from "@/lib/api"
import { B2_DEFAULT_GB_PER_CENT, b2Cap, b2Cost, formatGB, formatMoney, isB2, setB2Rate, toGB, useB2Rate } from "@/lib/b2"

type Unit = "GB" | "TB"

/** Estimates what Backblaze B2 charges to store an amount, and the daily cap it needs. */
export function B2CostDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const drives = useQuery({ queryKey: ["drives"], queryFn: api.drives, enabled: open })
  const rate = useB2Rate()
  const [typed, setTyped] = useState<string | null>(null)
  const [unit, setUnit] = useState<Unit>("GB")
  const [rateText, setRateText] = useState<string | null>(null)

  // What each Backblaze drive holds now.
  const buckets = (drives.data?.drives ?? [])
    .filter((d) => d.kind === "s3" && isB2(d.s3?.endpoint))
    .map((d) => ({ id: d.id, name: d.name, gb: toGB(d.scan.bytes) }))
  const totalGB = buckets.reduce((n, b) => n + b.gb, 0)

  const show = (gb: number, u: Unit) => String(Math.round((u === "TB" ? gb / 1000 : gb) * 100) / 100)
  const value = typed ?? show(totalGB, unit)
  const gb = Math.max(0, Number(value) || 0) * (unit === "TB" ? 1000 : 1)
  const cost = b2Cost(gb, rate)
  const cap = b2Cap(gb, rate)
  const roomy = b2Cap(gb * 2, rate)

  const pick = (g: number) => {
    const u = g >= 1000 ? "TB" : "GB"
    setUnit(u)
    setTyped(show(g, u))
  }
  const switchUnit = (u: Unit) => {
    if (!u || u === unit) return
    setUnit(u)
    setTyped(show(gb, u))
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        if (!o) {
          setTyped(null)
          setRateText(null)
          onClose()
        }
      }}
    >
      <DialogContent className="max-h-[92svh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Backblaze storage costs</DialogTitle>
          <DialogDescription>
            Backblaze B2 charges by the day for what your buckets hold. Uploading is free, and so is downloading up to three times what you
            store each month.
          </DialogDescription>
        </DialogHeader>

        <div className="grid gap-5">
          <Field>
            <FieldLabel htmlFor="b2-amount">Storage</FieldLabel>
            <div className="flex gap-2">
              <Input
                id="b2-amount"
                type="number"
                inputMode="decimal"
                min={0}
                step="any"
                className="tabular-nums"
                value={value}
                onChange={(e) => setTyped(e.target.value)}
              />
              <ToggleGroup type="single" variant="outline" value={unit} onValueChange={(v) => switchUnit(v as Unit)}>
                <ToggleGroupItem value="GB" className="px-3">
                  GB
                </ToggleGroupItem>
                <ToggleGroupItem value="TB" className="px-3">
                  TB
                </ToggleGroupItem>
              </ToggleGroup>
            </div>
            {buckets.length > 0 && (
              <div className="flex flex-wrap gap-2">
                {buckets.map((b) => (
                  <Button key={b.id} type="button" variant="outline" size="sm" onClick={() => pick(b.gb)}>
                    <CloudIcon /> {b.name}: {formatGB(b.gb)}
                  </Button>
                ))}
                {buckets.length > 1 && (
                  <Button type="button" variant="outline" size="sm" onClick={() => pick(totalGB)}>
                    All: {formatGB(totalGB)}
                  </Button>
                )}
              </div>
            )}
            <FieldDescription>
              Backblaze counts a GB as a billion bytes, so this reads a little higher than sizes elsewhere in Coffer.
            </FieldDescription>
          </Field>

          <div className="grid grid-cols-3 gap-2 rounded-xl border bg-muted/40 p-3 text-center">
            {(
              [
                ["Per day", cost.day],
                ["Per month", cost.month],
                ["Per year", cost.year],
              ] as const
            ).map(([label, v]) => (
              <div key={label}>
                <div className="text-xs text-muted-foreground">{label}</div>
                <div className="text-lg font-semibold tabular-nums">{formatMoney(v)}</div>
              </div>
            ))}
          </div>

          <p className="text-sm">
            Set Backblaze's daily storage cap (<b>Caps &amp; Alerts</b>) to at least <b className="tabular-nums">{formatMoney(cap)}</b>. Uploads stop
            once your buckets hold more than the cap allows; <span className="tabular-nums">{formatMoney(roomy)}</span> leaves room to double.
            The cap is a ceiling, not your bill.
          </p>

          <Field>
            <FieldLabel htmlFor="b2-rate">Backblaze's rate</FieldLabel>
            <div className="flex items-center gap-2 text-sm">
              <span className="shrink-0">$0.01 a day stores</span>
              <Input
                id="b2-rate"
                type="number"
                inputMode="decimal"
                min={1}
                step="any"
                className="w-24 tabular-nums"
                value={rateText ?? rate}
                onChange={(e) => {
                  setRateText(e.target.value)
                  if (Number(e.target.value) > 0) setB2Rate(Number(e.target.value))
                }}
              />
              <span>GB</span>
            </div>
            <FieldDescription>
              Caps &amp; Alerts in Backblaze shows how much storage a $0.01 cap allows; change this if their price changes. It comes to{" "}
              <span className="tabular-nums">{formatMoney(b2Cost(1000, rate).month)}</span> per TB a month.
              {rate !== B2_DEFAULT_GB_PER_CENT && (
                <>
                  {" "}
                  <button type="button" className="underline underline-offset-2" onClick={() => {
                      setRateText(null)
                      setB2Rate(B2_DEFAULT_GB_PER_CENT)
                    }}>
                    Reset to {B2_DEFAULT_GB_PER_CENT} GB
                  </button>
                </>
              )}
            </FieldDescription>
          </Field>
        </div>
      </DialogContent>
    </Dialog>
  )
}
