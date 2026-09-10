/* Copyright (c) 2026, s0up and the autobrr contributors.
 * SPDX-License-Identifier: GPL-2.0-or-later */

import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from "@/components/ui/alert-dialog"
import { useInstances } from "@/hooks/useInstances"
import { useCentralRSS, useRSSCandidates, useRSSDiscoveryStatus } from "@/hooks/useRSS"
import { api } from "@/lib/api"
import type { RacingConfiguration, RacingResource, RacingRule } from "@/types/racing"
import { RSSConfigurationForm, type RSSSelection } from "./RSSConfigurationForm"
import { RSSMigrationPreview } from "./RSSMigrationPreview"

export function CentralRSS({ activeTab, onTabChange }: { activeTab: "feeds" | "rules"; onTabChange: (tab: "feeds" | "rules") => void }) {
  const { t } = useTranslation("rss")
  const { instances } = useInstances()
  const client = useQueryClient()
  const config = useCentralRSS()
  const discoveries = useRSSDiscoveryStatus()
  const [editing, setEditing] = useState<RSSSelection | null>(null)
  const [deleting, setDeleting] = useState<{ resource: RacingResource; id: number; name: string } | null>(null)
  const [migration, setMigration] = useState(false)
  const [preview, setPreview] = useState(false)
  const deletion = useMutation({
    mutationFn: ({ resource, id }: { resource: RacingResource; id: number }) => api.deleteRacingResource(resource, id),
    onSuccess: () => { setDeleting(null); client.invalidateQueries({ queryKey: ["racing-configuration"] }) },
  })
  const reorder = useMutation({
    mutationFn: async ({ rule, direction }: { rule: RacingRule; direction: number }) => {
      const sorted = [...(config.data?.rules ?? [])]
      const index = sorted.findIndex(item => item.id === rule.id)
      const next = index + direction
      if (index < 0 || next < 0 || next >= sorted.length) return
      ;[sorted[index], sorted[next]] = [sorted[next], sorted[index]]
      await api.reorderRacingRules(sorted.map(item => item.id))
    },
    onSettled: () => client.invalidateQueries({ queryKey: ["racing-configuration"] }),
  })
  if (config.isError) return <p className="p-6 text-destructive" role="alert">{t("central.loadError")} <Button variant="outline" onClick={() => config.refetch()}>{t("central.retry")}</Button></p>
  if (!config.data) return <p className="p-6">{t("central.loading")}</p>
  const data = config.data
  const actions = (resource: RSSSelection["resource"], item: { id: number; name: string }) => <div className="flex gap-2 shrink-0">
    <Button size="sm" variant="outline" onClick={() => setEditing({ resource, id: item.id })}>{t("central.edit")}</Button>
    <Button size="sm" variant="ghost" onClick={() => { deletion.reset(); setDeleting({ resource, ...item }) }}>{t("central.delete")}</Button>
  </div>
  return <section className="p-6 space-y-4">
    <div className="flex flex-wrap justify-between items-center gap-3"><h1 className="text-2xl font-semibold">{t("pageTitle")}</h1><Button variant="outline" onClick={() => setMigration(true)}>{t("central.migration")}</Button></div>
    <p className="text-sm text-muted-foreground">{t("central.observeOnly")}</p>
    <Tabs value={activeTab} onValueChange={value => onTabChange(value as "feeds" | "rules")}>
      <TabsList><TabsTrigger value="feeds">{t("tabs.feeds")}</TabsTrigger><TabsTrigger value="rules">{t("tabs.rules")}</TabsTrigger></TabsList>
      <TabsContent value="feeds" className="space-y-4 mt-4">
        <div className="flex justify-between items-center gap-3"><h2 className="font-semibold">{t("central.sources")}</h2><Button onClick={() => setEditing({ resource: "sources", id: null })} disabled={data.sites.length === 0}>{t("central.addSource")}</Button></div>
        {data.sites.length === 0 && <p className="text-sm">{t("central.addSiteFirst")}</p>}
        {data.sources.length === 0 && <p className="text-muted-foreground">{t("central.emptySources")}</p>}
        {data.sources.map(source => {
          const status = discoveries.data?.sources.find(item => item.sourceId === source.id)
          const active = source.enabled && data.sites.some(site => site.id === source.siteId && site.enabled)
          return <div key={source.id} className="rounded-lg border p-4 flex flex-wrap items-center justify-between gap-3">
            <div className="min-w-0 space-y-1"><h3 className="font-medium break-all">{source.name}</h3><p className="text-sm text-muted-foreground">{data.sites.find(site => site.id === source.siteId)?.name} · {t(`central.kinds.${source.kind}`)} · {t(active ? "central.enabled" : "central.disabled")}</p>
              <p className="text-sm text-muted-foreground break-all">{source.urlOrigin}</p>
              {active && <p className="text-sm">{t(discoveries.isError ? "central.statusUnavailable" : status?.lastError ? "central.fetchError" : status?.inFlight ? "central.fetching" : "central.observing")}{status?.lastSuccessAt && ` · ${t("central.lastSuccess")}: ${new Date(status.lastSuccessAt).toLocaleString()}`}</p>}
            </div>{actions("sources", source)}
          </div>
        })}
        <details className="rounded-lg border p-4" open={data.sites.length === 0}>
          <summary className="cursor-pointer font-medium">{t("central.sites")}</summary>
          <div className="space-y-3 pt-4"><Button size="sm" variant="outline" onClick={() => setEditing({ resource: "sites", id: null })}>{t("central.addSite")}</Button>
            {data.sites.map(site => <div key={site.id} className="flex flex-wrap justify-between gap-3 border-t pt-3"><div className="min-w-0"><p className="font-medium break-all">{site.name}</p><p className="text-sm text-muted-foreground break-all">{site.baseUrl} · {t(site.enabled ? "central.enabled" : "central.disabled")} · {t(site.hasCredential ? "central.credentialSaved" : "central.noCredential")}</p></div>{actions("sites", site)}</div>)}
          </div>
        </details>
      </TabsContent>
      <TabsContent value="rules" className="space-y-4 mt-4">
        <div className="flex flex-wrap justify-between gap-3"><h2 className="font-semibold">{t("tabs.rules")}</h2><div className="flex gap-2"><Button variant="outline" onClick={() => setPreview(value => !value)}>{t("central.matchPreview")}</Button><Button onClick={() => setEditing({ resource: "rules", id: null })} disabled={data.sources.length === 0}>{t("central.addRule")}</Button></div></div>
        {data.rules.length === 0 && <p className="text-muted-foreground">{t("central.emptyRules")}</p>}
        {reorder.isError && <p role="alert" className="text-destructive">{t("central.saveError")}</p>}
        {data.rules.map((rule, index) => <div key={rule.id} className="rounded-lg border p-4 flex flex-wrap items-center justify-between gap-3">
          <div className="min-w-0 flex-1"><h3 className="font-medium break-all">{rule.name}</h3><p className="text-sm text-muted-foreground">{rule.acceptKinds.map(kind => t(`central.accept.${kind}`)).join(" / ")} · {t(rule.enabled ? "central.enabled" : "central.disabled")}</p><p className="text-sm break-all">{rule.sourceIds.map(id => data.sources.find(source => source.id === id)?.name ?? t("central.unknown")).join(", ")} → {rule.targetGroupId ? data.groups.find(group => group.id === rule.targetGroupId)?.name : instances?.find(instance => instance.id === rule.targetInstanceId)?.name}</p></div>
          <div className="flex flex-wrap gap-2"><Button size="sm" variant="ghost" disabled={index === 0 || reorder.isPending} onClick={() => reorder.mutate({ rule, direction: -1 })}>{t("central.moveUp")}</Button><Button size="sm" variant="ghost" disabled={index === data.rules.length - 1 || reorder.isPending} onClick={() => reorder.mutate({ rule, direction: 1 })}>{t("central.moveDown")}</Button>{actions("rules", rule)}</div>
        </div>)}
        {preview && <CandidatePreview config={data} />}
      </TabsContent>
    </Tabs>
    <Dialog open={editing !== null} onOpenChange={open => { if (!open) setEditing(null) }}><DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-xl"><DialogHeader><DialogTitle>{editing && t(`central.${editing.resource}`)}</DialogTitle><DialogDescription>{t("central.observeOnly")}</DialogDescription></DialogHeader>{editing && <RSSConfigurationForm key={`${editing.resource}:${editing.id}`} selection={editing} config={data} instances={instances ?? []} capabilities={discoveries.data?.capabilities ?? []} onClose={() => setEditing(null)} />}</DialogContent></Dialog>
    <AlertDialog open={deleting !== null} onOpenChange={open => { if (!open && !deletion.isPending) setDeleting(null) }}><AlertDialogContent><AlertDialogHeader><AlertDialogTitle>{t("central.deleteTitle", { name: deleting?.name })}</AlertDialogTitle><AlertDialogDescription>{t("central.deleteHelp")}</AlertDialogDescription></AlertDialogHeader>{deletion.isError && <p role="alert" className="text-destructive">{t("central.deleteError")}</p>}<AlertDialogFooter><AlertDialogCancel disabled={deletion.isPending}>{t("central.cancel")}</AlertDialogCancel><AlertDialogAction disabled={deletion.isPending} onClick={event => { event.preventDefault(); if (deleting) deletion.mutate(deleting) }}>{t("central.delete")}</AlertDialogAction></AlertDialogFooter></AlertDialogContent></AlertDialog>
    <Dialog open={migration} onOpenChange={setMigration}><DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-3xl"><DialogHeader><DialogTitle>{t("central.migration")}</DialogTitle><DialogDescription>{t("central.migrationHelp")}</DialogDescription></DialogHeader><RSSMigrationPreview instances={instances ?? []} /></DialogContent></Dialog>
  </section>
}

function CandidatePreview({ config }: { config: RacingConfiguration }) {
  const { t } = useTranslation("rss")
  const [cursors, setCursors] = useState<string[]>([""])
  const candidates = useRSSCandidates(cursors[cursors.length - 1])
  return <section className="rounded-lg border p-4 space-y-3"><h3 className="font-medium">{t("central.matchPreview")}</h3><p className="text-sm text-muted-foreground">{t("central.previewHelp")}</p>
    {candidates.isError && <p role="alert" className="text-destructive">{t("central.loadError")}</p>}
    {candidates.isLoading && <p>{t("central.loading")}</p>}
    {candidates.data?.items.length === 0 && <p>{t("central.emptyCandidates")}</p>}
    {candidates.data?.items.map(record => <div key={record.key} className="border-t pt-3 space-y-1"><h4 className="font-medium break-all">{record.candidate.item.title || t("central.unknown")}</h4><p className="text-sm">{config.sites.find(site => site.id === record.siteId)?.name} · {record.selection.rule?.name ?? t("central.noRule")} · {t(`central.states.${record.selection.state}`)}</p><p className="text-sm text-muted-foreground">{t("central.firstSeen")}: {new Date(record.firstSeenAt).toLocaleString()}</p>{record.selection.missingFields.length > 0 && <p className="text-sm">{t("central.missingEvidence")}: {record.selection.missingFields.map(field => t(`central.evidence.${evidenceKey(field)}`)).join(", ")}</p>}</div>)}
    <div className="flex gap-2"><Button size="sm" variant="outline" disabled={cursors.length < 2} onClick={() => setCursors(current => current.slice(0, -1))}>{t("central.previous")}</Button><Button size="sm" variant="outline" disabled={!candidates.data?.nextCursor} onClick={() => { if (candidates.data?.nextCursor) setCursors(current => [...current, candidates.data!.nextCursor!]) }}>{t("central.next")}</Button></div>
  </section>
}

function evidenceKey(field: string) {
  const known = ["identity", "official", "free", "revival", "title", "size", "published_at", "reported_hash", "verified_hash_mismatch"] as const
  return known.find(value => value === field) ?? "unknown"
}
