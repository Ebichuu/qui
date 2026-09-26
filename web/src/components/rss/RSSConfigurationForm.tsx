/* Copyright (c) 2026, s0up and the autobrr contributors.
 * SPDX-License-Identifier: GPL-2.0-or-later */

import { useState, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Checkbox } from "@/components/ui/checkbox"
import { Textarea } from "@/components/ui/textarea"
import { FieldHelp } from "@/components/ui/field-help"
import { api } from "@/lib/api"
import type { InstanceResponse } from "@/types"
import type { RacingAcceptKind, RacingCapability, RacingConfiguration, RacingKind, RacingResourceInput } from "@/types/racing"

export type RSSSelection = { resource: "sites" | "sources" | "rules"; id: number | null }
const selectClass = "w-full rounded-md border bg-background px-3 py-2 text-sm"
const lines = (value: string) => value.split(/\r?\n/).map(item => item.trim()).filter(Boolean)

function Field({ id, label, help, children }: { id: string; label: string; help?: string; children: ReactNode }) {
  return <div className="space-y-2"><div className="flex items-center gap-2"><Label htmlFor={id}>{label}</Label>{help && <FieldHelp>{help}</FieldHelp>}</div>{children}</div>
}

export function RSSConfigurationForm({ selection, config, instances, capabilities, onClose }: { selection: RSSSelection; config: RacingConfiguration; instances: InstanceResponse[]; capabilities: RacingCapability[]; onClose: () => void }) {
  const { t } = useTranslation("rss")
  const client = useQueryClient()
  const { resource, id } = selection
  const site = resource === "sites" ? config.sites.find(item => item.id === id) : undefined
  const source = resource === "sources" ? config.sources.find(item => item.id === id) : undefined
  const rule = resource === "rules" ? config.rules.find(item => item.id === id) : undefined
  const [name, setName] = useState(site?.name ?? source?.name ?? rule?.name ?? "")
  const [enabled, setEnabled] = useState(site?.enabled ?? source?.enabled ?? rule?.enabled ?? false)
  const [baseUrl, setBaseUrl] = useState(site?.baseUrl ?? "")
  const [hosts, setHosts] = useState(site?.trackerHosts.join("\n") ?? "")
  const [credential, setCredential] = useState("")
  const [clearCredential, setClearCredential] = useState(false)
  const [requestInterval, setRequestInterval] = useState(String(site?.requestIntervalSeconds ?? 5))
  const [siteId, setSiteId] = useState(source ? String(source.siteId) : "")
  const [url, setURL] = useState("")
  const [kind, setKind] = useState<RacingKind>(source?.kind ?? "rss")
  const [adapter, setAdapter] = useState(source?.adapter ?? "generic-rss")
  const [interval, setInterval] = useState(String(source?.intervalSeconds ?? 60))
  const [pages, setPages] = useState(String(source?.pageCount ?? 1))
  const [lookback, setLookback] = useState(String(source?.initialLookbackSeconds ?? 0))
  const [sourceIds, setSourceIds] = useState(rule?.sourceIds ?? [])
  const [accepted, setAccepted] = useState<RacingAcceptKind[]>(rule?.acceptKinds ?? [])
  const [target, setTarget] = useState(rule?.targetGroupId ? `group:${rule.targetGroupId}` : rule?.targetInstanceId ? `instance:${rule.targetInstanceId}` : "")
  const [windowSeconds, setWindowSeconds] = useState(rule ? String(rule.receiveWindowSeconds) : "")
  const [minimum, setMinimum] = useState(rule?.filters.minSizeBytes === undefined ? "" : String(rule.filters.minSizeBytes))
  const [maximum, setMaximum] = useState(rule?.filters.maxSizeBytes === undefined ? "" : String(rule.filters.maxSizeBytes))
  const [include, setInclude] = useState(rule?.filters.includeKeywords.join("\n") ?? "")
  const [exclude, setExclude] = useState(rule?.filters.excludeKeywords.join("\n") ?? "")
  const [reclaim, setReclaim] = useState(rule?.allowOfficialReclaim ?? false)
  const mutation = useMutation({
    mutationFn: (input: RacingResourceInput) => api.saveRacingResource(resource, id, input),
    onSuccess: () => { client.invalidateQueries({ queryKey: ["racing-configuration"] }); client.invalidateQueries({ queryKey: ["racing-candidates"] }); onClose() },
  })
  const supported = capabilities.find(item => item.id === adapter)?.kinds.includes(kind) ?? false
  const revivalAvailable = sourceIds.some(sourceID => {
    const selected = config.sources.find(item => item.id === sourceID)
    return selected?.enabled && config.sites.some(site => site.id === selected.siteId && site.enabled)
      && capabilities.some(item => item.id === selected.adapter && item.revival && item.kinds.includes(selected.kind))
  })
  const ruleInvalid = resource === "rules" && (sourceIds.length === 0 || accepted.length === 0 || !target || !windowSeconds || Number(windowSeconds) <= 0)
  const targetID = Number(target.split(":")[1])
  const unavailableTarget = target.startsWith("group:")
    ? !config.groups.some(group => group.id === targetID && group.enabled && group.instanceIds.some(id => instances.some(instance => instance.id === id && instance.isActive)))
    : !instances.some(instance => instance.id === targetID && instance.isActive)
  return <form className="space-y-4" onSubmit={event => {
    event.preventDefault()
    if (ruleInvalid || (resource === "sources" && !supported)) return
    let input: RacingResourceInput
    if (resource === "sites") input = { name, enabled, baseUrl, trackerHosts: lines(hosts), requestIntervalSeconds: Number(requestInterval), ...(clearCredential ? { credential: "" } : credential !== "" ? { credential } : {}) }
    else if (resource === "sources") input = { name, enabled, siteId: Number(siteId), kind, adapter, intervalSeconds: Number(interval), pageCount: kind === "web" ? Number(pages) : 1, initialLookbackSeconds: Number(lookback), ...(url !== "" ? { url } : {}) }
    else input = { name, enabled, sortOrder: rule?.sortOrder ?? Math.max(-1, ...config.rules.map(item => item.sortOrder)) + 1, sourceIds, acceptKinds: accepted, filters: { includeKeywords: lines(include), excludeKeywords: lines(exclude), ...(minimum !== "" ? { minSizeBytes: Number(minimum) } : {}), ...(maximum !== "" ? { maxSizeBytes: Number(maximum) } : {}) }, receiveWindowSeconds: Number(windowSeconds), allowOfficialReclaim: reclaim, ...(target.startsWith("group:") ? { targetGroupId: Number(target.split(":")[1]) } : { targetInstanceId: Number(target.split(":")[1]) }) }
    mutation.mutate(input)
  }}>
    <Field id="central-name" label={t("central.name")}><Input id="central-name" required maxLength={200} value={name} onChange={event => setName(event.target.value)} /></Field>
    <label className="flex items-center gap-2 text-sm"><Checkbox checked={enabled} onCheckedChange={value => setEnabled(value === true)} />{t("central.enabled")}</label>
    {resource === "sites" && <>
      <Field id="central-base" label={t("central.baseUrl")} help={t("central.baseHelp")}><Input id="central-base" required type="url" value={baseUrl} onChange={event => setBaseUrl(event.target.value)} /></Field>
      <Field id="central-cookie" label={t("central.credential")} help={t("central.secretHelp")}><Input id="central-cookie" type="password" autoComplete="new-password" disabled={clearCredential} value={credential} onChange={event => setCredential(event.target.value)} /></Field>
      {site?.hasCredential && <label className="flex items-center gap-2 text-sm"><Checkbox checked={clearCredential} onCheckedChange={value => setClearCredential(value === true)} />{t("central.clearCredential")}</label>}
      <Field id="central-hosts" label={t("central.trackerHosts")} help={t("central.hostsHelp")}><Textarea id="central-hosts" value={hosts} onChange={event => setHosts(event.target.value)} /></Field>
      <Field id="central-request-interval" label={t("central.requestInterval")} help={t("central.requestHelp")}><Input id="central-request-interval" type="number" required min={1} max={86400} value={requestInterval} onChange={event => setRequestInterval(event.target.value)} /></Field>
    </>}
    {resource === "sources" && <>
      <Field id="central-site" label={t("central.site")}><select id="central-site" className={selectClass} required value={siteId} onChange={event => setSiteId(event.target.value)}><option value="" />{config.sites.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></Field>
      <div className="grid grid-cols-2 gap-3">
        <Field id="central-kind" label={t("central.kind")}><select id="central-kind" className={selectClass} value={kind} onChange={event => { const value = event.target.value as RacingKind; setKind(value); if (value !== "rss") setAdapter("chd") }}>
          {(["rss", "web", "revival"] as const).map(value => <option key={value} value={value}>{t(`central.kinds.${value}`)}</option>)}
        </select></Field>
        <Field id="central-adapter" label={t("central.adapter")}><select id="central-adapter" className={selectClass} required value={adapter} onChange={event => setAdapter(event.target.value)}>{capabilities.filter(item => item.kinds.includes(kind)).map(item => <option key={item.id} value={item.id}>{item.site}</option>)}</select></Field>
      </div>
      {!supported && <p role="alert" className="text-sm text-destructive">{t("central.adapterUnavailable")}</p>}
      <Field id="central-url" label={t("central.url")} help={t(id === null ? "central.urlHelp" : "central.secretHelp")}><Input id="central-url" type="url" required={id === null} autoComplete="off" value={url} onChange={event => setURL(event.target.value)} /></Field>
      <Field id="central-interval" label={t("central.interval")}><Input id="central-interval" type="number" required min={1} max={86400} value={interval} onChange={event => setInterval(event.target.value)} /></Field>
      {kind === "web" && <Field id="central-pages" label={t("central.pages")}><Input id="central-pages" type="number" required min={1} max={5} value={pages} onChange={event => setPages(event.target.value)} /></Field>}
      <Field id="central-lookback" label={t("central.lookback")} help={t("central.lookbackHelp")}><Input id="central-lookback" type="number" required min={0} max={604800} value={lookback} onChange={event => setLookback(event.target.value)} /></Field>
      <p className="text-sm text-muted-foreground">{t("central.adapterValidation")}</p>
    </>}
    {resource === "rules" && <>
      <fieldset className="space-y-2"><legend className="text-sm font-medium mb-2">{t("central.sources")}</legend><div className="max-h-40 overflow-y-auto space-y-2">{config.sources.map(item => <label key={item.id} className="flex items-center gap-2 text-sm"><Checkbox checked={sourceIds.includes(item.id)} onCheckedChange={checked => setSourceIds(current => checked === true ? [...current, item.id] : current.filter(value => value !== item.id))} />{item.name}{!item.enabled && ` · ${t("central.disabled")}`}</label>)}</div></fieldset>
      <fieldset><legend className="text-sm font-medium mb-2 flex items-center gap-2">{t("central.acceptKinds")}<FieldHelp>{t("central.acceptHelp")}</FieldHelp></legend><div className="flex flex-wrap gap-4">{(["official", "free", "revival"] as const).map(value => <label key={value} className="flex items-center gap-2 text-sm"><Checkbox checked={accepted.includes(value)} onCheckedChange={checked => setAccepted(current => checked === true ? [...current, value] : current.filter(item => item !== value))} />{t(`central.accept.${value}`)}</label>)}</div></fieldset>
      {accepted.length === 0 && <p className="text-sm text-destructive">{t("central.chooseKind")}</p>}
      {accepted.includes("revival") && !revivalAvailable && <p role="alert" className="text-sm text-amber-600 dark:text-amber-400">{t("central.revivalUnavailable")}</p>}
      <Field id="central-target" label={t("central.target")} help={t("central.targetHelp")}><select id="central-target" className={selectClass} required value={target} onChange={event => setTarget(event.target.value)}><option value="" /><optgroup label={t("central.groups")}>{config.groups.map(group => <option key={group.id} value={`group:${group.id}`}>{group.name}</option>)}</optgroup><optgroup label={t("central.instances")}>{instances.map(instance => <option key={instance.id} value={`instance:${instance.id}`}>{instance.name}</option>)}</optgroup></select></Field>
      {unavailableTarget && <p className="text-sm text-amber-600 dark:text-amber-400">{t("central.targetUnavailable")}</p>}
      <a href="/settings?tab=instances" className="text-sm underline">{t("central.manageGroups")}</a>
      <details className="rounded-md border p-3" open={!rule || undefined}><summary className="cursor-pointer text-sm font-medium">{t("central.options")}</summary><div className="space-y-4 pt-4">
        <Field id="central-window" label={t("central.window")} help={t("central.windowHelp")}><Input id="central-window" required type="number" min={1} max={9223372036} value={windowSeconds} onChange={event => setWindowSeconds(event.target.value)} /></Field>
        <div className="grid grid-cols-2 gap-3"><Field id="central-min" label={t("central.minSize")}><Input id="central-min" type="number" min={0} max={Number.MAX_SAFE_INTEGER} value={minimum} onChange={event => setMinimum(event.target.value)} /></Field><Field id="central-max" label={t("central.maxSize")}><Input id="central-max" type="number" min={minimum || 0} max={Number.MAX_SAFE_INTEGER} value={maximum} onChange={event => setMaximum(event.target.value)} /></Field></div>
        <Field id="central-include" label={t("central.include")} help={t("central.includeHelp")}><Textarea id="central-include" value={include} onChange={event => setInclude(event.target.value)} /></Field>
        <Field id="central-exclude" label={t("central.exclude")} help={t("central.excludeHelp")}><Textarea id="central-exclude" value={exclude} onChange={event => setExclude(event.target.value)} /></Field>
        <div className="flex items-center gap-2"><label className="flex items-center gap-2 text-sm"><Checkbox checked={reclaim} onCheckedChange={value => setReclaim(value === true)} />{t("central.reclaim")}</label><FieldHelp>{t("central.reclaimHelp")}</FieldHelp></div>
      </div></details>
    </>}
    {mutation.isError && <p role="alert" className="text-sm text-destructive">{t("central.saveError")}</p>}
    <div className="flex justify-end gap-2"><Button type="button" variant="outline" onClick={onClose} disabled={mutation.isPending}>{t("central.cancel")}</Button><Button type="submit" disabled={mutation.isPending || ruleInvalid || (resource === "sources" && !supported)}>{t("central.save")}</Button></div>
  </form>
}
