/* Copyright (c) 2026, s0up and the autobrr contributors.
 * SPDX-License-Identifier: GPL-2.0-or-later */

import { isRSSFeed, type RSSItems, type RSSRules } from "@/types/rss"

export type MigrationStatus = "mapped" | "review" | "unsupported" | "private"
export interface MigrationField { field: string; value: string; target?: string; status: MigrationStatus }
export interface MigrationEntry { name: string; kind: "source" | "rule" | "task"; fields: MigrationField[]; required: string[] }
type RecordValue = Record<string, unknown>
const object = (value: unknown): value is RecordValue => !!value && typeof value === "object" && !Array.isArray(value)
const privateField = /cookie|passkey|credential|password|secret|token|apikey|api_key/i

// The preview stays in browser memory. Never render credentials or private URL
// paths/queries, including strings nested inside unknown imported settings.
export function migrationValue(value: unknown, field = ""): string {
  if (privateField.test(field)) return "••••"
  if (Array.isArray(value)) return `[${value.map(item => migrationValue(item, field)).join(", ")}]`
  if (object(value)) return `{${Object.entries(value).map(([key, item]) => `${key}: ${migrationValue(item, key)}`).join(", ")}}`
  if (/\b(cookie|passkey|password|secret|token|api[_-]?key)\s*[:=]/i.test(String(value))) return "••••"
  return String(value ?? "").replace(/https?:\/\/[^\s"'<>\],}]+/gi, address => {
    try { return `${new URL(address).origin}/…` } catch { return "…" }
  })
}

function fields(input: RecordValue, mappings: Record<string, string>, reviews: Record<string, string> = {}): MigrationField[] {
  return Object.entries(input).map(([field, value]) => ({ field, value: migrationValue(value, field), target: mappings[field] ?? reviews[field], status: privateField.test(field) ? "private" : mappings[field] ? "mapped" : reviews[field] ? "review" : "unsupported" }))
}

export function previewQB(items: RSSItems, rules: RSSRules): MigrationEntry[] {
  const entries: MigrationEntry[] = []
  function visit(tree: RSSItems, prefix = "") {
    for (const [name, item] of Object.entries(tree)) {
      const path = prefix ? `${prefix}/${name}` : name
      if (isRSSFeed(item)) {
        // Runtime article data is not configuration and is deliberately excluded.
        const feed = Object.fromEntries(Object.entries(item).filter(([key]) => key !== "articles"))
        entries.push({ name: path, kind: "source", fields: fields(feed, { url: "url", title: "name" }, { uid: "sourceIds" }), required: ["site", "adapter", "interval", "lookback"] })
      } else visit(item, path)
    }
  }
  visit(items)
  for (const [name, rule] of Object.entries(rules)) {
    const { torrentParams, ...input } = rule
    const result = fields(input as unknown as RecordValue, {}, { enabled: "enabled", priority: "sortOrder", affectedFeeds: "sourceIds", mustContain: "includeKeywords", mustNotContain: "excludeKeywords" })
    if (torrentParams) result.push(...fields(torrentParams as RecordValue, {}).map(field => ({ ...field, field: `torrentParams.${field.field}` })))
    entries.push({ name, kind: "rule", fields: result, required: ["acceptKinds", "window", "target", "handoff"] })
  }
  return entries
}

function vertexEntry(input: RecordValue, kind: MigrationEntry["kind"], web = false): MigrationEntry {
  const mappings: Record<string, string> = kind === "source" ? { alias: "name", parserType: "adapter", pageUrl: "url", pageCount: "pageCount" } : { alias: "name" }
  const reviews: Record<string, string> = kind === "source" ? { enable: "enabled", targetSharedSource: "sourceIds", minIntervalSeconds: "intervalSeconds", maxIntervalSeconds: "intervalSeconds" } : kind === "task" ? { enable: "enabled", rssUrls: "sources", intervalSeconds: "intervalSeconds", clientArr: "targetGroupId", client: "targetInstanceId", acceptRules: "rules", rejectRules: "excludeKeywords", sharedSource: "sources", sharedSourcePriority: "sortOrder", scrapeFree: "acceptKinds" } : { priority: "sortOrder", client: "targetInstanceId", conditions: "filters" }
  const output = fields(input, mappings, reviews)
  if (kind === "rule" && Array.isArray(input.conditions)) {
    for (const [index, condition] of input.conditions.entries()) {
      if (!object(condition)) continue
      let target: string | undefined
      if (condition.compareType === "equals" && ((condition.key === "siteOfficial" && String(condition.value) === "1") || (condition.key === "chdCategory" && condition.value === "官种"))) target = "acceptKinds.official"
      if (condition.compareType === "equals" && ((condition.key === "siteRevived" && String(condition.value) === "1") || (condition.key === "chdCategory" && condition.value === "复活区"))) target = "acceptKinds.revival"
      if (condition.key === "name" && condition.compareType === "contain") target = "filters.includeKeywords"
      if (condition.key === "name" && condition.compareType === "notContain") target = "filters.excludeKeywords"
      if (condition.key === "size" && ["bigger", "smaller"].includes(String(condition.compareType))) target = condition.compareType === "bigger" ? "filters.minSizeBytes" : "filters.maxSizeBytes"
      // Conditions are AND in Vertex, but accepted types are OR in qui. Even
      // recognisable terms need whole-rule review; never auto-enable a subset.
      output.push({ field: `conditions[${index}]`, value: migrationValue(condition), target, status: target ? "review" : "unsupported" })
    }
  }
  return { name: String(input.alias ?? input.id ?? ""), kind, fields: output, required: web ? ["site", "interval", "lookback", "sharedSource"] : kind === "task" ? ["site", "adapter", "acceptKinds", "window", "target", "ruleReferences", "handoff"] : ["sources", "acceptKinds", "window", "target", "conditionSemantics", "handoff"] }
}

// Accept individual files or a bundle assembled from the three Vertex config
// directories. No scripts/expressions execute, and no source is enabled here.
export function previewVertex(value: unknown): MigrationEntry[] {
  if (!object(value)) throw new Error("invalid-export")
  const entries: MigrationEntry[] = []
  const add = (item: unknown) => {
    if (!object(item)) throw new Error("invalid-export")
    if ("pageUrl" in item) entries.push(vertexEntry(item, "source", true))
    else if ("rssUrls" in item) entries.push(vertexEntry(item, "task"))
    else if ((Array.isArray(item.conditions) || typeof item.code === "string") && (typeof item.alias === "string" || typeof item.id === "string")) entries.push(vertexEntry(item, "rule"))
    else throw new Error("invalid-export")
  }
  if ("rss" in value || "rules" in value || "webMonitors" in value) {
    if (Object.keys(value).some(key => !["rss", "rules", "webMonitors"].includes(key))) throw new Error("invalid-export")
    for (const key of ["rss", "rules", "webMonitors"] as const) {
      if (value[key] === undefined) continue
      if (!Array.isArray(value[key])) throw new Error("invalid-export")
      for (const item of value[key]) add(item)
    }
  } else add(value)
  if (entries.length === 0) throw new Error("invalid-export")
  return entries
}
