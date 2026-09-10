/* Copyright (c) 2026, s0up and the autobrr contributors.
 * SPDX-License-Identifier: GPL-2.0-or-later */

import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { api } from "@/lib/api"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Checkbox } from "@/components/ui/checkbox"
import { FieldHelp } from "@/components/ui/field-help"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from "@/components/ui/alert-dialog"
import type { InstanceResponse } from "@/types"
import type { RacingConfiguration, RacingResource, RacingResourceInput } from "@/types/racing"
import { formatBytes } from "@/lib/utils"

type Selection = { resource: RacingResource; id: number | null }
const titles = { groups: "groups", "storage-pools": "pools", "path-mappings": "mappings" } as const
const addLabels = { groups: "addGroup", "storage-pools": "addPool", "path-mappings": "addMapping" } as const

export function DownloaderGroups({ instances }: { instances: InstanceResponse[] }) {
  const { t } = useTranslation("settings")
  const queryClient = useQueryClient()
  const [now, setNow] = useState(Date.now)
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [])
  const [editing, setEditing] = useState<Selection | null>(null)
  const [deleting, setDeleting] = useState<(Selection & { id: number; name: string }) | null>(null)
  const config = useQuery({ queryKey: ["racing-configuration"], queryFn: () => api.getRacingConfiguration() })
  const observations = useQuery({ queryKey: ["racing-observations"], queryFn: () => api.getRacingObservations(), refetchInterval: 3000 })
  const deletion = useMutation({
    mutationFn: ({ resource, id }: Selection & { id: number }) => api.deleteRacingResource(resource, id),
    onSuccess: () => { setDeleting(null); queryClient.invalidateQueries({ queryKey: ["racing-configuration"] }) },
  })
  if (config.isError) return <p role="alert">{t("racingGroups.loadError")}</p>
  if (!config.data) return null
  const observationsAvailable = !observations.isError && now - observations.dataUpdatedAt <= 5000
  const data = config.data
  const instanceName = (id: number) => instances.find(instance => instance.id === id)?.name ?? t("racingGroups.unknown")
  const rows = {
    groups: data.groups.map(group => ({
      id: group.id, name: group.name,
      detail: `${t(group.enabled ? "racingGroups.enabled" : "racingGroups.disabled")} · ${group.instanceIds.map(instanceName).join(", ") || t("racingGroups.emptyGroup")}`,
    })),
    "storage-pools": data.storagePools.map(pool => {
      const free = observations.data?.storagePools.find(item => item.storagePoolId === pool.id)?.freeBytes
      return { id: pool.id, name: pool.name, detail: `${t("racingGroups.freeSpace")}: ${free === undefined || !observationsAvailable ? t("racingGroups.unknown") : formatBytes(free)}` }
    }),
    "path-mappings": data.pathMappings.map(mapping => ({ id: mapping.id, name: `${instanceName(mapping.instanceId)} · ${mapping.path}`, detail: data.storagePools.find(pool => pool.id === mapping.storagePoolId)?.name ?? t("racingGroups.unknown") })),
  }
  return <section className="space-y-4">
    <p className="text-sm text-muted-foreground">{t("racingGroups.observeOnly")}</p>
    {(["groups", "storage-pools", "path-mappings"] as const).map(resource => <div key={resource} className="rounded-lg border p-4 space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="font-medium flex items-center gap-2">{t(`racingGroups.${titles[resource]}`)}
          {resource !== "path-mappings" && <FieldHelp>{t(resource === "groups" ? "racingGroups.groupHelp" : "racingGroups.poolHelp")}</FieldHelp>}
        </h3>
        <Button size="sm" variant="outline" onClick={() => setEditing({ resource, id: null })}>{t(`racingGroups.${addLabels[resource]}`)}</Button>
      </div>
      {rows[resource].length === 0 && <p className="text-sm text-muted-foreground">{t("racingGroups.empty")}</p>}
      {rows[resource].map(row => <div key={row.id} className="flex flex-wrap items-center justify-between gap-2 border-t pt-3">
        <div className="min-w-0 flex-1"><p className="font-medium break-all">{row.name}</p><p className="text-sm text-muted-foreground break-all">{row.detail}</p></div>
        <div className="flex gap-2">
          <Button size="sm" variant="outline" onClick={() => setEditing({ resource, id: row.id })}>{t("racingGroups.edit")}</Button>
          <Button size="sm" variant="ghost" onClick={() => { deletion.reset(); setDeleting({ resource, id: row.id, name: row.name }) }}>{t("racingGroups.delete")}</Button>
        </div>
      </div>)}
    </div>)}
    {instances.length > 0 && <div className="grid gap-2 sm:grid-cols-2">
      {instances.map(instance => {
        const state = observations.data?.instances.find(item => item.instanceId === instance.id)
        const fresh = instance.isActive && observationsAvailable && state?.fresh
        return <div key={instance.id} className="rounded-lg border p-3 text-sm space-y-1">
          <p className="font-medium">{instance.name} · <span className={fresh ? "text-green-600 dark:text-green-400" : "text-muted-foreground"}>{t(fresh ? "racingGroups.fresh" : "racingGroups.stale")}</span></p>
          {state?.observedAt && <p className="text-muted-foreground">{new Date(state.observedAt).toLocaleString()}</p>}
          {state?.version && <p>{state.version}</p>}
          {state?.savePath && <p className="break-all">{state.savePath}</p>}
        </div>
      })}
    </div>}
    <Dialog open={editing !== null} onOpenChange={open => { if (!open) setEditing(null) }}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto">
        <DialogHeader><DialogTitle>{editing && t(`racingGroups.${titles[editing.resource]}`)}</DialogTitle><DialogDescription>{t("racingGroups.observeOnly")}</DialogDescription></DialogHeader>
        {editing && <ConfigurationForm key={`${editing.resource}:${editing.id}`} selection={editing} config={data} instances={instances} onClose={() => setEditing(null)} />}
      </DialogContent>
    </Dialog>
    <AlertDialog open={deleting !== null} onOpenChange={open => { if (!open && !deletion.isPending) setDeleting(null) }}>
      <AlertDialogContent>
        <AlertDialogHeader><AlertDialogTitle>{t("racingGroups.deleteTitle", { name: deleting?.name })}</AlertDialogTitle><AlertDialogDescription>{t("racingGroups.deleteHelp")}</AlertDialogDescription></AlertDialogHeader>
        {deletion.isError && <p role="alert" className="text-sm text-destructive">{t("racingGroups.error")}</p>}
        <AlertDialogFooter>
          <AlertDialogCancel disabled={deletion.isPending}>{t("racingGroups.cancel")}</AlertDialogCancel>
          <AlertDialogAction disabled={deletion.isPending} onClick={event => { event.preventDefault(); if (deleting) deletion.mutate(deleting) }}>{t("racingGroups.delete")}</AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </section>
}

function ConfigurationForm({ selection, config, instances, onClose }: { selection: Selection; config: RacingConfiguration; instances: InstanceResponse[]; onClose: () => void }) {
  const { t } = useTranslation("settings")
  const queryClient = useQueryClient()
  const { resource, id } = selection
  const group = resource === "groups" ? config.groups.find(item => item.id === id) : undefined
  const pool = resource === "storage-pools" ? config.storagePools.find(item => item.id === id) : undefined
  const mapping = resource === "path-mappings" ? config.pathMappings.find(item => item.id === id) : undefined
  const [name, setName] = useState(group?.name ?? pool?.name ?? "")
  const [enabled, setEnabled] = useState(group?.enabled ?? true)
  const [members, setMembers] = useState<number[]>(group?.instanceIds ?? [])
  const [instanceId, setInstanceId] = useState(mapping ? String(mapping.instanceId) : "")
  const [poolId, setPoolId] = useState(mapping ? String(mapping.storagePoolId) : "")
  const [path, setPath] = useState(mapping?.path ?? "")
  const mutation = useMutation({
    mutationFn: (input: RacingResourceInput) => api.saveRacingResource(resource, id, input),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["racing-configuration"] }); onClose() },
  })
  const selectClass = "w-full rounded-md border bg-background px-3 py-2 text-sm"
  return <form className="space-y-4" onSubmit={event => {
    event.preventDefault()
    mutation.mutate(resource === "groups" ? { name, enabled, instanceIds: members } : resource === "storage-pools" ? { name } : { instanceId: Number(instanceId), storagePoolId: Number(poolId), path })
  }}>
    {resource !== "path-mappings" && <div className="space-y-2"><Label htmlFor="racing-name">{t("racingGroups.name")}</Label><Input id="racing-name" required maxLength={200} value={name} onChange={event => setName(event.target.value)} /></div>}
    {resource === "groups" && <>
      <label className="flex items-center gap-2"><Checkbox checked={enabled} onCheckedChange={value => setEnabled(value === true)} />{t("racingGroups.enabled")}</label>
      <fieldset className="space-y-2 max-h-56 overflow-y-auto"><legend className="text-sm font-medium mb-2">{t("racingGroups.members")}</legend>
        {instances.length === 0 && <p className="text-sm text-muted-foreground">{t("racingGroups.emptyGroup")}</p>}
        {instances.map(instance => <label key={instance.id} className="flex items-center gap-2 text-sm"><Checkbox checked={members.includes(instance.id)} onCheckedChange={checked => setMembers(current => checked === true ? [...current, instance.id] : current.filter(member => member !== instance.id))} />{instance.name}{!instance.isActive && ` · ${t("racingGroups.disabled")}`}</label>)}
      </fieldset>
    </>}
    {resource === "path-mappings" && <>
      <div className="space-y-2"><Label htmlFor="racing-instance">{t("racingGroups.instance")}</Label><select id="racing-instance" className={selectClass} required value={instanceId} onChange={event => setInstanceId(event.target.value)}><option value="" />{instances.map(instance => <option key={instance.id} value={instance.id}>{instance.name}</option>)}</select></div>
      <div className="space-y-2"><Label htmlFor="racing-pool">{t("racingGroups.pool")}</Label><select id="racing-pool" className={selectClass} required value={poolId} onChange={event => setPoolId(event.target.value)}><option value="" />{config.storagePools.map(pool => <option key={pool.id} value={pool.id}>{pool.name}</option>)}</select></div>
      <div className="space-y-2"><Label htmlFor="racing-path">{t("racingGroups.path")}</Label><Input id="racing-path" required value={path} onChange={event => setPath(event.target.value)} /></div>
    </>}
    {mutation.isError && <p role="alert" className="text-sm text-destructive">{t("racingGroups.error")}</p>}
    <div className="flex justify-end gap-2"><Button type="button" variant="outline" onClick={onClose} disabled={mutation.isPending}>{t("racingGroups.cancel")}</Button><Button type="submit" disabled={mutation.isPending}>{t("racingGroups.save")}</Button></div>
  </form>
}
