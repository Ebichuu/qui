/* Copyright (c) 2026, s0up and the autobrr contributors.
 * SPDX-License-Identifier: GPL-2.0-or-later */

import { useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation } from "@tanstack/react-query"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { api } from "@/lib/api"
import { previewQB, previewVertex, type MigrationEntry } from "@/lib/rss-migration"
import type { InstanceResponse } from "@/types"

export function RSSMigrationPreview({ instances }: { instances: InstanceResponse[] }) {
  const { t } = useTranslation("rss")
  const [instance, setInstance] = useState("")
  const [entries, setEntries] = useState<MigrationEntry[]>([])
  const [fileError, setFileError] = useState(false)
  const generation = useRef(0)
  const request = useMutation({
    mutationFn: async ({ id, version }: { id: number; version: number }) => {
      const [items, rules] = await Promise.all([api.getRSSItems(id, false), api.getRSSRules(id)])
      return { entries: previewQB(items, rules), version }
    },
    onSuccess: result => { if (result.version === generation.current) setEntries(result.entries) },
  })
  return <div className="space-y-4">
    <div className="flex flex-wrap items-end gap-3"><div className="space-y-2 flex-1"><Label htmlFor="migration-instance">{t("central.instances")}</Label><select id="migration-instance" className="w-full rounded-md border bg-background px-3 py-2 text-sm" value={instance} onChange={event => { generation.current++; setInstance(event.target.value); setEntries([]); request.reset() }}><option value="" />{instances.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></div><Button disabled={!instance || request.isPending} onClick={() => { setFileError(false); setEntries([]); request.mutate({ id: Number(instance), version: ++generation.current }) }}>{t("central.readQB")}</Button></div>
    <div className="space-y-2"><Label htmlFor="migration-file">{t("central.vertexFile")}</Label><Input id="migration-file" type="file" accept=".json,application/json" onChange={async event => {
      const file = event.target.files?.[0]
      if (!file) return
      const version = ++generation.current
      setFileError(false); setEntries([]); request.reset()
      try {
        if (file.size > 2 * 1024 * 1024) throw new Error("export-too-large")
        const result = previewVertex(JSON.parse(await file.text()))
        if (version === generation.current) setEntries(result)
      } catch { if (version === generation.current) setFileError(true) }
    }} /></div>
    <p className="text-sm text-muted-foreground">{t("central.vertexHelp")}</p>
    {(fileError || request.isError) && <p role="alert" className="text-destructive">{t(fileError ? "central.invalidExport" : "central.loadError")}</p>}
    {request.isPending && <p>{t("central.loading")}</p>}
    {entries.map((entry, index) => <section key={index} className="rounded-lg border p-3 space-y-3"><h3 className="font-medium break-all">{entry.name || t("central.unknown")} · {t(`central.entryKinds.${entry.kind}`)}</h3><p className="text-sm text-amber-600 dark:text-amber-400">{t("central.reviewRequired")}: {entry.required.map(field => t(`central.requirements.${field}`)).join(", ")}</p><div className="overflow-x-auto"><table className="w-full text-sm"><thead><tr className="text-left border-b"><th className="p-2">{t("central.originalField")}</th><th className="p-2">{t("central.originalValue")}</th><th className="p-2">{t("central.mapping")}</th></tr></thead><tbody>{entry.fields.map((field, fieldIndex) => <tr key={fieldIndex} className="border-b"><td className="p-2 align-top break-all">{field.field}</td><td className="p-2 align-top max-w-64 break-all whitespace-pre-wrap">{field.value}</td><td className="p-2 align-top">{t(`central.migrationStates.${field.status}`)}{field.target && <code className="block text-xs break-all">{field.target}</code>}</td></tr>)}</tbody></table></div></section>)}
  </div>
}
