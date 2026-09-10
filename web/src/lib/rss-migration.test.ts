/* Copyright (c) 2026, s0up and the autobrr contributors.
 * SPDX-License-Identifier: GPL-2.0-or-later */
import { describe, expect, it } from "vitest"
import { migrationValue, previewQB, previewVertex } from "./rss-migration"
import type { RSSAutoDownloadRule } from "@/types/rss"

describe("RSS migration preview", () => {
  it("preserves unsupported downloader options and rule conditions without enabling anything", () => {
    const rule: RSSAutoDownloadRule = { enabled: true, priority: 2, useRegex: true, mustContain: "Example.*", mustNotContain: "", affectedFeeds: ["https://example.invalid/rss?passkey=hidden-key"], ignoreDays: 3, smartFilter: true, episodeFilter: "1x01", addPaused: false, savePath: "D:\\Downloads", assignedCategory: "Example", torrentParams: { stopped: false, use_auto_tmm: true, save_path: "/data/example", category: "New", upload_limit: 2000 } }
    const result = previewQB({ Folder: { Feed: { uid: "feed-id", url: "https://example.invalid/rss?passkey=hidden-key", hasError: false, isLoading: false } } }, { Example: rule })
    expect(result).toHaveLength(2)
    const fields = result[1].fields
    for (const field of ["useRegex", "episodeFilter", "smartFilter", "ignoreDays", "addPaused", "savePath", "assignedCategory", "torrentParams.stopped", "torrentParams.use_auto_tmm", "torrentParams.category", "torrentParams.upload_limit"]) expect(fields.find(item => item.field === field)?.status).toBe("unsupported")
    expect(fields.find(item => item.field === "torrentParams.stopped")?.value).toBe("false")
    expect(result[1].required).toContain("handoff")
    expect(JSON.stringify(result)).not.toContain("hidden-key")
    expect(rule.enabled).toBe(true)
  })
  it("does not turn Vertex AND conditions or post-rule free checks into OR acceptance", () => {
    const result = previewVertex({ rss: [{ alias: "Task", rssUrls: ["https://example.invalid/rss"], acceptRules: ["r1"], rejectRules: ["r2"], scrapeFree: true, scheduleType: "cron", cron: "*/5 * * * *", unknownSetting: { cookie: "private-cookie" } }], rules: [{ id: "r1", alias: "Official revival", type: "normal", conditions: [{ key: "siteOfficial", compareType: "equals", value: "1" }, { key: "siteRevived", compareType: "equals", value: "1" }] }], webMonitors: [{ alias: "Web", pageUrl: "https://example.invalid/torrents?token=hidden", parserType: "chd", minIntervalSeconds: 11, maxIntervalSeconds: 61 }] })
    expect(result).toHaveLength(3)
    expect(result[0].fields.find(item => item.field === "scrapeFree")?.status).toBe("review")
    expect(result[0].fields.find(item => item.field === "scheduleType")?.status).toBe("unsupported")
    expect(result[0].required).toContain("ruleReferences")
    const conditions = result[1].fields.filter(item => item.field.startsWith("conditions["))
    expect(conditions.map(item => item.status)).toEqual(["review", "review"])
    expect(result[2].fields.find(item => item.field === "maxIntervalSeconds")?.status).toBe("review")
    expect(JSON.stringify(result)).not.toContain("private-cookie")
  })
  it("never evaluates scripts, and rejects unsupported bundle sections rather than dropping them", () => {
    expect(previewVertex({ alias: "Script", type: "javascript", code: "throw new Error('never run')" })[0].fields.find(item => item.field === "code")?.status).toBe("unsupported")
    expect(() => previewVertex({ rss: [], unknown: [] })).toThrow()
    expect(() => previewVertex({ rss: [{}] })).toThrow()
    expect(() => previewVertex([])).toThrow()
    expect(() => previewVertex({ rules: [{}] })).toThrow()
    expect(migrationValue("cookie=private-cookie", "unknownSetting")).not.toContain("private-cookie")
    expect(migrationValue({ password: "never-show", urls: ["https://user:password@example.invalid/secret?passkey=value"] })).not.toContain("password@example")
    expect(migrationValue({ password: "never-show" })).not.toContain("never-show")
  })
})
