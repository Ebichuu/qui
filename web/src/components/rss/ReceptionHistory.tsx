/* Copyright (c) 2026, s0up and the autobrr contributors.
 * SPDX-License-Identifier: GPL-2.0-or-later */

import { PeerHistory } from "@/components/rss/PeerHistory"
import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"
import { api } from "@/lib/api"
import { formatBytes } from "@/lib/utils"
import { Button } from "@/components/ui/button"
import type { InstanceResponse } from "@/types"
import type { RacingConfiguration } from "@/types/racing"

export function ReceptionHistory({ config, instances }: { config: RacingConfiguration; instances: InstanceResponse[] }) {
  const { t } = useTranslation("rss")
  const [cursors, setCursors] = useState<string[]>([""])
  const cursor = cursors[cursors.length - 1]
  const history = useQuery({ queryKey: ["racing-add-intents", cursor], queryFn: () => api.getRacingAddIntents(cursor), refetchInterval: 3000 })
  const reasons = ["task_removed", "reevaluation_required", "target_unavailable", "tracker_identity_unverified", "existing_tracker_mismatch", "metainfo_unavailable", "awaiting_task"]
  return <section className="rounded-lg border p-4 space-y-3"><h3 className="font-medium">{t("execution.history")}</h3><p className="text-sm text-muted-foreground">{t("execution.historyHelp")}</p>
    {history.isError && <p role="alert" className="text-destructive">{t("execution.loadError")}</p>}
    {history.isLoading && <p>{t("central.loading")}</p>}
    {history.data?.items.length === 0 && <p>{t("execution.emptyHistory")}</p>}
    {history.data?.items.map(intent => <div key={intent.candidateKey} className="border-t pt-3 space-y-2">
      <h4 className="font-medium">{config.sites.find(site => site.id === intent.plan.siteId)?.name ?? t("central.unknown")} · {intent.plan.rule.name} → {instances.find(instance => instance.id === intent.instanceId)?.name ?? t("central.unknown")}</h4>
      <p className="text-sm">{t(`execution.states.${intent.state}`)} · {formatBytes(intent.plan.sizeBytes)}</p>
      <p className="text-xs text-muted-foreground break-all">{intent.plan.hashV1 || intent.plan.hashV2}</p>
      {intent.reason && <p className="text-sm">{t(`execution.reasons.${reasons.includes(intent.reason) ? intent.reason : "unknown"}`)}</p>}
      <PeerHistory candidateKey={intent.candidateKey} />
      <dl className="grid gap-x-4 gap-y-1 text-sm sm:grid-cols-2">{(["reservedAt", "submittedAt", "acceptedAt", "confirmedAt", "runnableAt", "transferredAt"] as const).map(stage => <div key={stage}><dt className="inline text-muted-foreground">{t(`execution.stages.${stage}`)}: </dt><dd className="inline">{intent[stage] ? new Date(intent[stage]).toLocaleString() : t("execution.notObserved")}</dd></div>)}</dl>
    </div>)}
    <div className="flex gap-2"><Button size="sm" variant="outline" disabled={cursors.length < 2} onClick={() => setCursors(current => current.slice(0, -1))}>{t("central.previous")}</Button><Button size="sm" variant="outline" disabled={!history.data?.nextCursor} onClick={() => { if (history.data?.nextCursor) setCursors(current => [...current, history.data!.nextCursor!]) }}>{t("central.next")}</Button></div>
  </section>
}
