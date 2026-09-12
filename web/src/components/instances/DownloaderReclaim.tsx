/* Copyright (c) 2026, s0up and the autobrr contributors.
 * SPDX-License-Identifier: GPL-2.0-or-later */

import { useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"
import { api } from "@/lib/api"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { FieldHelp } from "@/components/ui/field-help"
import { Label } from "@/components/ui/label"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import type { InstanceResponse } from "@/types"
import type { RacingConfiguration, RacingReclaimPolicy } from "@/types/racing"

type Target = { scope: "instances" | "groups"; id: number; name: string }

export function DownloaderReclaim({ instances, groups }: { instances: InstanceResponse[]; groups: RacingConfiguration["groups"] }) {
  const { t } = useTranslation("instances")
  const [editing, setEditing] = useState<Target | null>(null)
  const config = useQuery({ queryKey: ["racing-reclaim-settings"], queryFn: () => api.getRacingReclaimConfiguration(), refetchInterval: 5000 })
  const targets: Target[] = [...instances.map(item => ({ scope: "instances" as const, id: item.id, name: item.name })), ...groups.map(item => ({ scope: "groups" as const, id: item.id, name: item.name }))]
  const setting = config.data?.settings.find(item => item.scope === editing?.scope && item.targetId === editing.id)
  const effective = config.data?.effective.find(item => editing?.scope === "instances" && item.instanceId === editing.id)
  return <section className="rounded-lg border p-4 space-y-3">
    <h3 className="font-medium">{t("reclaim.title")}</h3>
    <p className="text-sm text-muted-foreground">{t("reclaim.intro")}</p>
    {config.isError && <p role="alert">{t("reclaim.loadError")}</p>}
    {targets.map(target => {
      const own = config.data?.settings.find(item => item.scope === target.scope && item.targetId === target.id)
      const state = target.scope === "instances" ? config.data?.effective.find(item => item.instanceId === target.id)?.state : own ? own.policy.enabled ? "explicit" : "disabled" : "unconfigured"
      return <div key={`${target.scope}:${target.id}`} className="flex flex-wrap items-center justify-between gap-2 border-t pt-3">
        <p>{t(`reclaim.scopes.${target.scope}`)} · {target.name} · {t(`reclaim.states.${state ?? "unavailable"}`)}</p>
        <Button variant="outline" size="sm" disabled={!config.data || config.isError} onClick={() => setEditing(target)}>{t("reclaim.edit")}</Button>
      </div>
    })}
    <Dialog open={editing !== null} onOpenChange={open => { if (!open) setEditing(null) }}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto"><DialogHeader><DialogTitle>{t("reclaim.editTitle", { name: editing?.name })}</DialogTitle><DialogDescription>{t("reclaim.intro")}</DialogDescription></DialogHeader>
        {editing && <ReclaimForm key={`${editing.scope}:${editing.id}`} target={editing} initial={setting?.policy ?? effective?.policy} hasOverride={!!setting} instances={instances} onClose={() => setEditing(null)} />}
      </DialogContent>
    </Dialog>
  </section>
}

export function ReclaimForm({ target, initial, hasOverride, instances, onClose }: { target: Target; initial?: RacingReclaimPolicy; hasOverride: boolean; instances: InstanceResponse[]; onClose: () => void }) {
  const { t } = useTranslation("instances")
  const client = useQueryClient()
  const [policy, setPolicy] = useState<RacingReclaimPolicy>(() => initial ?? { enabled: false, ruleIds: [], maxDeletes: 1, maxReclaimBytes: 0, maxRecentUploadBytes: 0, recentUploadWindowSeconds: 3600, maxOvershootBytes: 0 })
  const rules = useQuery({ queryKey: ["reclaim-rule-definitions", instances.map(item => item.id)], queryFn: async () => (await Promise.all(instances.map(async instance => (await api.listAutomations(instance.id)).map(rule => ({ ...rule, instanceName: instance.name }))))).flat() })
  const mutation = useMutation({ mutationFn: (clear: boolean) => clear ? api.clearRacingReclaimPolicy(target.scope, target.id) : api.saveRacingReclaimPolicy(target.scope, target.id, policy), onSuccess: () => { client.invalidateQueries({ queryKey: ["racing-reclaim-settings"] }); onClose() } })
  const id = (field: string) => `reclaim-${target.scope}-${target.id}-${field}`
  return <form className="space-y-4" onSubmit={event => { event.preventDefault(); mutation.mutate(false) }}>
    <label className="flex items-center gap-2"><Checkbox checked={policy.enabled} onCheckedChange={value => setPolicy({ ...policy, enabled: value === true })} />{t("reclaim.enabled")}</label>
    <fieldset className="space-y-2"><legend className="font-medium flex items-center gap-2">{t("reclaim.rules")}<FieldHelp>{t("reclaim.rulesHelp")}</FieldHelp></legend>
      {rules.isError && <p role="alert">{t("reclaim.loadError")}</p>}
      {rules.data?.map(rule => <label key={rule.id} className="flex items-center gap-2 text-sm"><Checkbox checked={policy.ruleIds.includes(rule.id)} onCheckedChange={checked => setPolicy({ ...policy, ruleIds: checked === true ? [...policy.ruleIds, rule.id] : policy.ruleIds.filter(item => item !== rule.id) })} />{rule.instanceName} · {rule.name} · {t(`preferences.workflowDialog.delete.usageOptions.${rule.conditions?.delete?.usage ?? "daily"}`)}</label>)}
    </fieldset>
    {(["maxDeletes", "maxReclaimBytes", "maxRecentUploadBytes", "recentUploadWindowSeconds", "maxOvershootBytes"] as const).map(field => <div key={field} className="space-y-2"><Label htmlFor={id(field)}>{t(`reclaim.${field}`)}</Label><Input id={id(field)} type="number" min={policy.enabled && (field === "recentUploadWindowSeconds" || field === "maxDeletes" || field === "maxReclaimBytes") ? 1 : 0} max={field === "maxOvershootBytes" ? policy.maxReclaimBytes : Number.MAX_SAFE_INTEGER} step={1} required value={policy[field]} onChange={event => setPolicy({ ...policy, [field]: event.target.valueAsNumber })} /></div>)}
    {mutation.isError && <p role="alert">{t("reclaim.saveError")}</p>}
    <div className="flex flex-wrap justify-end gap-2">
      {hasOverride && <Button type="button" variant="outline" disabled={mutation.isPending} onClick={() => mutation.mutate(true)}>{t(target.scope === "instances" ? "reclaim.inherit" : "reclaim.clearDefault")}</Button>}
      <Button type="button" variant="outline" onClick={onClose}>{t("reclaim.cancel")}</Button>
      <Button type="submit" disabled={mutation.isPending || (policy.enabled && (!rules.data || rules.isError || policy.ruleIds.length === 0))}>{t("reclaim.save")}</Button>
    </div>
  </form>
}
