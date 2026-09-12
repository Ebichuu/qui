/* Copyright (c) 2026, s0up and the autobrr contributors.
 * SPDX-License-Identifier: GPL-2.0-or-later */

import { useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { FieldHelp } from "@/components/ui/field-help"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { api } from "@/lib/api"
import type { RacingSite } from "@/types/racing"
import type { ReannounceTrackerPolicy } from "@/types/reannounce-policy"

class PolicyInputError extends Error {}

const queryKey = (instanceId: number) => ["instance-reannounce-tracker-policies", instanceId]

export function TrackerAccountPolicies({ instanceId }: { instanceId: number }) {
  const { t } = useTranslation("instances")
  const [open, setOpen] = useState(false)
  return (
    <section className="mt-6 space-y-4 border-t pt-4">
      <Button type="button" variant="outline" aria-expanded={open} onClick={() => setOpen(!open)}>{t("trackerPolicies.title")}</Button>
      {open && <PolicyList key={instanceId} instanceId={instanceId} />}
    </section>
  )
}

function PolicyList({ instanceId }: { instanceId: number }) {
  const { t } = useTranslation("instances")
  const [selected, setSelected] = useState("")
  const policies = useQuery({ queryKey: queryKey(instanceId), queryFn: () => api.getReannounceTrackerPolicies(instanceId) })
  const observations = useQuery({ queryKey: ["instance-reannounce-observations", instanceId], queryFn: () => api.getReannounceObservations(instanceId) })
  const configuration = useQuery({ queryKey: ["racing-configuration"], queryFn: () => api.getRacingConfiguration() })
  const fetching = policies.isFetching || observations.isFetching || configuration.isFetching
  const options = new Map<string, string>()
  for (const observation of observations.data ?? []) {
    for (const tracker of observation.trackers) options.set(tracker.key, tracker.host)
  }
  for (const policy of policies.data ?? []) options.set(policy.trackerKey, policy.trackerHost)
  const entries = [...options].sort((a, b) => a[1].localeCompare(b[1]) || a[0].localeCompare(b[0]))
  if (selected && !options.has(selected) && !policies.isPending && !observations.isPending) setSelected("")
  const policy = policies.data?.find(item => item.trackerKey === selected)
  const host = options.get(selected)
  const failed = policies.isError || observations.isError || configuration.isError
  return (
    <div className="space-y-4">
      <p className="text-sm text-muted-foreground">{t("trackerPolicies.intro")}</p>
      <p className="text-sm">{t("trackerPolicies.warning")}</p>
      <Button type="button" variant="outline" disabled={fetching} onClick={() => { void policies.refetch(); void observations.refetch(); void configuration.refetch() }}>{t("trackerPolicies.refresh")}</Button>
      {failed ? <p role="alert">{t("trackerPolicies.loadFailed")}</p> : policies.isPending || observations.isPending || configuration.isPending ? <p role="status">{t("trackerPolicies.loading")}</p> : (
        <>
          {entries.length === 0 ? <p>{t("trackerPolicies.empty")}</p> : (
            <div className="space-y-2">
              <Label htmlFor={`tracker-account-${instanceId}`}>{t("trackerPolicies.tracker")}</Label>
              <select id={`tracker-account-${instanceId}`} className="h-10 w-full rounded-md border bg-background px-3 text-sm" value={selected} onChange={event => setSelected(event.target.value)}>
                <option value="">{t("trackerPolicies.choose")}</option>
                {entries.map(([key, domain]) => <option key={key} value={key}>{domain} · {key.slice(0, 12)}</option>)}
              </select>
            </div>
          )}
          {host && <PolicyEditor key={selected} instanceId={instanceId} trackerKey={selected} host={host} policy={policy} sites={configuration.data?.sites ?? []} />}
        </>
      )}
    </div>
  )
}

function PolicyEditor({ instanceId, trackerKey, host, policy: initialPolicy, sites }: { instanceId: number; trackerKey: string; host: string; policy?: ReannounceTrackerPolicy; sites: RacingSite[] }) {
  const { t } = useTranslation("instances")
  const client = useQueryClient()
  const [policy, setPolicy] = useState(initialPolicy)
  const [trackerAddress, setTrackerAddress] = useState("")
  const eligibleSites = sites.filter(site => site.enabled && site.trackerHosts.includes(host))
  const [siteId, setSiteId] = useState(policy?.siteId.toString() ?? "")
  const needsIdentity = !policy || policy.siteId !== Number(siteId)
  const [interval, setInterval] = useState(String(policy?.intervalSeconds ?? 120))
  const [waitEnabled, setWaitEnabled] = useState(Boolean(policy?.waitSeconds))
  const [wait, setWait] = useState(String(policy?.waitSeconds || 60))
  const [message, setMessage] = useState("")
  const [protection, setProtection] = useState<ReannounceTrackerPolicy["deleteProtection"]>(policy?.deleteProtection ?? "blocked")
  const [error, setError] = useState("")
  const [confirmed, setConfirmed] = useState(false)
  const mutation = useMutation({
    mutationFn: async () => {
      const intervalSeconds = Number(interval)
      const waitSeconds = waitEnabled ? Number(wait) : 0
      if (!confirmed || !eligibleSites.some(site => site.id === Number(siteId)) || !Number.isInteger(intervalSeconds) || intervalSeconds < 1 || intervalSeconds > 2592000 || !Number.isInteger(waitSeconds) || (waitEnabled && waitSeconds < 1) || waitSeconds > 2592000 || (waitEnabled && !message && !policy?.waitMessageDigest)) {
        throw new PolicyInputError(t("trackerPolicies.invalid"))
      }
      if (needsIdentity) {
        if (!globalThis.crypto?.subtle) throw new PolicyInputError(t("trackerPolicies.secureContext"))
        const identity = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(trackerAddress))
        if (Array.from(new Uint8Array(identity), byte => byte.toString(16).padStart(2, "0")).join("") !== trackerKey) throw new PolicyInputError(t("trackerPolicies.invalid"))
      }
      let waitMessageDigest = waitEnabled ? policy?.waitMessageDigest ?? "" : ""
      if (waitEnabled && message) {
        if (!globalThis.crypto?.subtle) throw new PolicyInputError(t("trackerPolicies.secureContext"))
        const hash = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(message))
        waitMessageDigest = Array.from(new Uint8Array(hash), byte => byte.toString(16).padStart(2, "0")).join("")
      }
      const next: ReannounceTrackerPolicy = { trackerKey, trackerHost: host, siteId: Number(siteId), intervalSeconds, waitMessageDigest, waitSeconds, deleteProtection: protection }
      await api.saveReannounceTrackerPolicy(instanceId, next)
      return next
    },
    onSuccess: next => {
      client.setQueryData<ReannounceTrackerPolicy[]>(queryKey(instanceId), previous => [...(previous ?? []).filter(item => item.trackerKey !== trackerKey), next])
      setPolicy(next)
      setTrackerAddress("")
      setMessage("")
      setConfirmed(false)
      toast.success(t("trackerPolicies.saved"))
    },
    onError: error => setError(error instanceof PolicyInputError ? error.message : t("trackerPolicies.saveFailed")),
  })
  const prefix = `policy-${instanceId}`
  return (
    <form className="space-y-4" onSubmit={event => { event.preventDefault(); setError(""); mutation.mutate() }}>
      <fieldset disabled={mutation.isPending} className="space-y-4">
        <div className="space-y-2">
          <Label htmlFor={`${prefix}-site`}>{t("trackerPolicies.site")}</Label>
          <select id={`${prefix}-site`} className="h-10 w-full rounded-md border bg-background px-3 text-sm" value={siteId} onChange={event => setSiteId(event.target.value)} required>
            <option value="">{t("trackerPolicies.choose")}</option>
            {eligibleSites.map(site => <option key={site.id} value={site.id}>{site.name}</option>)}
          </select>
          {!eligibleSites.length && <p role="alert">{t("trackerPolicies.noSites")}</p>}
        </div>
        {needsIdentity && <div className="space-y-2">
          <Label htmlFor={`${prefix}-address`}>{t("trackerPolicies.trackerAddress")} <FieldHelp>{t("trackerPolicies.trackerAddressHelp")}</FieldHelp></Label>
          <Input id={`${prefix}-address`} type="password" autoComplete="off" required value={trackerAddress} onChange={event => setTrackerAddress(event.target.value)} />
        </div>}
        <div className="space-y-2">
          <Label htmlFor={`${prefix}-interval`}>{t("trackerPolicies.interval")} <FieldHelp>{t("trackerPolicies.intervalHelp")}</FieldHelp></Label>
          <Input id={`${prefix}-interval`} type="number" min={1} max={2592000} step={1} required value={interval} onChange={event => setInterval(event.target.value)} />
        </div>
        <label className="flex items-center gap-2"><input type="checkbox" checked={waitEnabled} onChange={event => setWaitEnabled(event.target.checked)} />{t("trackerPolicies.knownWait")}</label>
        {waitEnabled && (
          <div className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor={`${prefix}-message`}>{t("trackerPolicies.message")} <FieldHelp>{t("trackerPolicies.messageHelp")}</FieldHelp></Label>
              <Input id={`${prefix}-message`} type="password" autoComplete="off" value={message} required={!policy?.waitMessageDigest} onChange={event => setMessage(event.target.value)} />
              {policy?.waitMessageDigest && <p className="text-sm text-muted-foreground">{t("trackerPolicies.keepMessage")}</p>}
            </div>
            <div className="space-y-2">
              <Label htmlFor={`${prefix}-wait`}>{t("trackerPolicies.wait")}</Label>
              <Input id={`${prefix}-wait`} type="number" min={1} max={2592000} step={1} required value={wait} onChange={event => setWait(event.target.value)} />
            </div>
          </div>
        )}
        <div className="space-y-2">
          <Label htmlFor={`${prefix}-protection`}>{t("trackerPolicies.protection")}</Label>
          <select id={`${prefix}-protection`} className="h-10 w-full rounded-md border bg-background px-3 text-sm" value={protection} onChange={event => setProtection(event.target.value as ReannounceTrackerPolicy["deleteProtection"])}>
            <option value="blocked">{t("trackerPolicies.blocked")}</option>
            <option value="reported_working">{t("trackerPolicies.working")}</option>
            <option value="accounted">{t("trackerPolicies.accounted")}</option>
          </select>
        </div>
        <label className="flex items-start gap-2 text-sm"><input type="checkbox" checked={confirmed} required onChange={event => setConfirmed(event.target.checked)} />{t("trackerPolicies.confirm")}</label>
        {error && <p role="alert">{error}</p>}
        <Button type="submit" disabled={!confirmed || !eligibleSites.length || mutation.isPending}>{t(mutation.isPending ? "trackerPolicies.saving" : "trackerPolicies.save")}</Button>
      </fieldset>
    </form>
  )
}
