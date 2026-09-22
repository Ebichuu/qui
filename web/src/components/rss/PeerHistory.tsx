/* Copyright (c) 2026, s0up and the autobrr contributors.
 * SPDX-License-Identifier: GPL-2.0-or-later */

import { useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"
import { api, APIError } from "@/lib/api"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Label } from "@/components/ui/label"
import { FieldHelp } from "@/components/ui/field-help"
import type { InstanceResponse } from "@/types"
import type { RacingAnalysisSetting } from "@/types/racing"

export function AnalysisSetting({ instances }: { instances: InstanceResponse[] }) {
  const { t } = useTranslation("rss")
  const client = useQueryClient()
  const settings = useQuery({ queryKey: ["racing-analysis-settings"], queryFn: () => api.getRacingAnalysisSettings() })
  const mutation = useMutation({ mutationFn: (setting: RacingAnalysisSetting) => api.saveRacingAnalysisSetting(setting), onSuccess: () => client.invalidateQueries({ queryKey: ["racing-analysis-settings"] }) })
  return <div className="space-y-3 border-t pt-3">
    {(settings.isError || mutation.isError) && <p role="alert">{t("execution.loadError")}</p>}
    {instances.map(instance => <div key={instance.id} className="flex gap-2 items-center">
      <Checkbox id={`analysis-${instance.id}`} disabled={!settings.data || mutation.isPending} checked={settings.data?.find(item => item.instanceId === instance.id)?.enabled === true} onCheckedChange={value => mutation.mutate({ instanceId: instance.id, enabled: value === true })} />
      <Label htmlFor={`analysis-${instance.id}`}>{instance.name} · {t("analysis.enable")}</Label><FieldHelp>{t("analysis.help")}</FieldHelp>
    </div>)}
  </div>
}

export function PeerHistory({ candidateKey }: { candidateKey: string }) {
  const { t } = useTranslation("rss")
  const [open, setOpen] = useState(false)
  const [page, setPage] = useState(0)
  const history = useQuery({ queryKey: ["racing-analysis-history", candidateKey], queryFn: () => api.getRacingAnalysisHistory(candidateKey), enabled: open, refetchInterval: open ? 10000 : false, retry: false })
  const data = history.data
  const peers = Object.entries(data?.peers ?? {}).sort(([a], [b]) => a.localeCompare(b))
  return <div className="space-y-2">
    <Button size="sm" variant="outline" aria-expanded={open} onClick={() => setOpen(value => !value)}>{t("analysis.show")}</Button>
    {open && <div className="space-y-2 text-sm">
      {history.isLoading && <p>{t("central.loading")}</p>}
      {history.isError && <p>{t(history.error instanceof APIError && history.error.status === 404 ? "analysis.empty" : "execution.loadError")}</p>}
      {data && <>
        <p>{t("analysis.coverage", { successful: data.successfulSamples, expected: data.expectedSamples, missing: data.missingSamples })}</p>
        <p>{t("analysis.lastVisible")}: {data.lastVisible ? new Date(data.lastVisible).toLocaleString() : t("execution.notObserved")}</p>
        <p>{t("analysis.lastSuccess")}: {data.lastSuccess ? new Date(data.lastSuccess).toLocaleString() : t("execution.notObserved")}</p>
        <p className="text-muted-foreground">{t("analysis.unknown")}</p>
        {data.truncated && <p>{t("analysis.truncated")}</p>}
        <div className="overflow-x-auto"><table className="w-full min-w-[32rem] text-left"><thead><tr><th>{t("analysis.endpoint")}</th><th>{t("analysis.firstSeen")}</th><th>{t("analysis.lastSeen")}</th><th>{t("analysis.asn")}</th></tr></thead><tbody>
          {peers.slice(page * 50, (page + 1) * 50).map(([endpoint, peer]) => <tr key={endpoint}><td className="whitespace-nowrap pr-3">{endpoint}</td><td className="pr-3">{new Date(peer.firstSeen).toLocaleString()}</td><td>{new Date(peer.lastSeen).toLocaleString()} · {t(peer.absentAt ? "analysis.absent" : "analysis.visible")}</td><td className="pl-3">{peer.asn?.state === "found" ? <><span>{t("analysis.asnValue", { number: peer.asn.number, organization: peer.asn.organization })}</span><p className="text-muted-foreground">{t("analysis.asnEvidence", { database: new Date(peer.asn.databaseBuiltAt).toLocaleDateString(), observed: new Date(peer.asn.observedAt).toLocaleString() })}</p></> : t(peer.asn?.state === "not_found" ? "analysis.asnMissing" : peer.asn?.state === "error" ? "analysis.asnError" : "analysis.asnUnavailable")}</td></tr>)}
        </tbody></table></div>
        {peers.length > 50 && <div className="flex gap-2"><Button size="sm" variant="outline" disabled={page === 0} onClick={() => setPage(value => value - 1)}>{t("central.previous")}</Button><Button size="sm" variant="outline" disabled={(page + 1) * 50 >= peers.length} onClick={() => setPage(value => value + 1)}>{t("central.next")}</Button></div>}
      </>}
    </div>}
  </div>
}
