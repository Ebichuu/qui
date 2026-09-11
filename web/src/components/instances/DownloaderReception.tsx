/* Copyright (c) 2026, s0up and the autobrr contributors.
 * SPDX-License-Identifier: GPL-2.0-or-later */

import { useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"
import { api } from "@/lib/api"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { FieldHelp } from "@/components/ui/field-help"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import type { InstanceResponse } from "@/types"
import type { RacingInstancePolicy } from "@/types/racing"

function useReceptionPolicies() {
  return useQuery({ queryKey: ["racing-reception-policies"], queryFn: () => api.getRacingReceptionPolicies(), refetchInterval: 5000 })
}

export function ReceptionStatus() {
  const { t } = useTranslation("rss")
  const policies = useReceptionPolicies()
  return <p className="text-sm text-muted-foreground">{t(policies.isError ? "execution.statusUnavailable" : policies.data?.some(policy => policy.enabled) ? "execution.enabledIntro" : "execution.disabledIntro")}</p>
}

export function DownloaderReception({ instances }: { instances: InstanceResponse[] }) {
  const { t } = useTranslation("rss")
  const policies = useReceptionPolicies()
  const [editing, setEditing] = useState<number | null>(null)
  const instance = instances.find(item => item.id === editing)
  return <section className="rounded-lg border p-4 space-y-3">
    <h3 className="font-medium">{t("execution.title")}</h3>
    <p className="text-sm text-muted-foreground">{t("execution.setupHelp")}</p>
    {policies.isError && <p role="alert" className="text-destructive">{t("execution.loadError")}</p>}
    {instances.length === 0 && <p>{t("execution.noInstances")}</p>}
    {instances.map(item => <div key={item.id} className="flex flex-wrap items-center justify-between gap-2 border-t pt-3"><p>{item.name} · {t(policies.isError || !policies.data ? "execution.statusUnavailable" : policies.data.find(policy => policy.instanceId === item.id)?.enabled ? "execution.enabled" : "execution.disabled")}</p><Button size="sm" variant="outline" disabled={!policies.data || policies.isError} onClick={() => setEditing(item.id)}>{t("central.edit")}</Button></div>)}
    <Dialog open={!!instance} onOpenChange={open => { if (!open) setEditing(null) }}><DialogContent className="max-h-[90dvh] overflow-y-auto"><DialogHeader><DialogTitle>{t("execution.editTitle", { name: instance?.name })}</DialogTitle><DialogDescription>{t("execution.setupHelp")}</DialogDescription></DialogHeader>{instance && <ReceptionForm key={instance.id} instanceId={instance.id} initial={policies.data?.find(policy => policy.instanceId === instance.id)} onClose={() => setEditing(null)} />}</DialogContent></Dialog>
  </section>
}

export function ReceptionForm({ instanceId, initial, onClose }: { instanceId: number; initial?: RacingInstancePolicy; onClose: () => void }) {
  const { t } = useTranslation("rss")
  const client = useQueryClient()
  const [policy, setPolicy] = useState<RacingInstancePolicy>(() => initial ?? { instanceId, enabled: false, maxConcurrentAdds: 1, maxActiveDownloads: 8, minFreeBytes: 0, savePath: "", category: "", autoTMM: false, startPaused: false })
  const mutation = useMutation({ mutationFn: () => api.saveRacingReceptionPolicy(policy), onSuccess: () => { client.invalidateQueries({ queryKey: ["racing-reception-policies"] }); client.invalidateQueries({ queryKey: ["racing-configuration"] }); onClose() } })
  const id = (field: string) => `reception-${instanceId}-${field}`
  return <form className="space-y-4" onSubmit={event => { event.preventDefault(); mutation.mutate() }}>
    <div className="flex items-center gap-2"><Checkbox id={id("enabled")} checked={policy.enabled} onCheckedChange={value => setPolicy({ ...policy, enabled: value === true })} /><Label htmlFor={id("enabled")}>{t("execution.enableLabel")}</Label></div>
    {policy.enabled && <p className="rounded-md border p-3 text-sm">{t("execution.enableWarning")}</p>}
    {(["maxConcurrentAdds", "maxActiveDownloads", "minFreeBytes"] as const).map(field => <div key={field} className="space-y-2"><Label htmlFor={id(field)} className="flex gap-2 items-center">{t(`execution.${field}`)}<FieldHelp>{t(`execution.${field}Help`)}</FieldHelp></Label><Input id={id(field)} type="number" required min={field === "minFreeBytes" ? 0 : 1} max={field === "maxConcurrentAdds" ? 8 : Number.MAX_SAFE_INTEGER} step={1} value={policy[field]} onChange={event => setPolicy({ ...policy, [field]: event.target.valueAsNumber })} /></div>)}
    <div className="space-y-2"><Label htmlFor={id("category")}>{t("execution.category")}</Label><Input id={id("category")} value={policy.category} onChange={event => setPolicy({ ...policy, category: event.target.value })} /></div>
    <div className="flex items-center gap-2"><Checkbox id={id("autoTMM")} checked={policy.autoTMM} onCheckedChange={value => setPolicy({ ...policy, autoTMM: value === true, savePath: value === true ? "" : policy.savePath })} /><Label htmlFor={id("autoTMM")}>{t("execution.autoTMM")}</Label></div>
    {!policy.autoTMM && <div className="space-y-2"><Label htmlFor={id("savePath")} className="flex items-center gap-2">{t("execution.savePath")}<FieldHelp>{t("execution.savePathHelp")}</FieldHelp></Label><Input id={id("savePath")} value={policy.savePath} onChange={event => setPolicy({ ...policy, savePath: event.target.value })} /></div>}
    <div className="flex items-center gap-2"><Checkbox id={id("startPaused")} checked={policy.startPaused} onCheckedChange={value => setPolicy({ ...policy, startPaused: value === true })} /><Label htmlFor={id("startPaused")}>{t("execution.startPaused")}</Label></div>
    {mutation.isError && <p role="alert" className="text-destructive">{t("central.saveError")}</p>}
    <div className="flex justify-end gap-2"><Button type="button" variant="outline" onClick={onClose}>{t("central.cancel")}</Button><Button type="submit" disabled={mutation.isPending}>{t("central.save")}</Button></div>
  </form>
}
