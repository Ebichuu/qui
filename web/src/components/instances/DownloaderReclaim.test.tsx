/* Copyright (c) 2026, s0up and the autobrr contributors.
 * SPDX-License-Identifier: GPL-2.0-or-later */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { TooltipProvider } from "@/components/ui/tooltip"
import { api } from "@/lib/api"
import { ReclaimForm } from "./DownloaderReclaim"
import type { InstanceResponse } from "@/types"

vi.mock("@/lib/api", () => ({ api: { saveRacingReclaimPolicy: vi.fn(), clearRacingReclaimPolicy: vi.fn(), listAutomations: vi.fn() } }))
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }))
const instances = [{ id: 7, name: "Synthetic member" }] as InstanceResponse[]
const policy = { enabled: true, ruleIds: [23], maxDeletes: 2, maxReclaimBytes: 1000, maxRecentUploadBytes: 10, recentUploadWindowSeconds: 3600, maxOvershootBytes: 50 }
beforeEach(() => {
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} })
  vi.mocked(api.listAutomations).mockResolvedValue([])
  vi.mocked(api.saveRacingReclaimPolicy).mockResolvedValue(undefined)
  vi.mocked(api.clearRacingReclaimPolicy).mockResolvedValue(undefined)
})
afterEach(() => { cleanup(); vi.resetAllMocks(); vi.unstubAllGlobals() })
function mount(hasOverride: boolean) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(<QueryClientProvider client={client}><TooltipProvider><ReclaimForm target={{ scope: "instances", id: 7, name: "Synthetic member" }} initial={policy} hasOverride={hasOverride} instances={instances} onClose={() => {}} /></TooltipProvider></QueryClientProvider>)
}
describe("reclaim settings overrides", () => {
  it("saves explicit disable while preserving inherited budget and references", async () => {
    mount(false)
    fireEvent.click(screen.getByRole("checkbox", { name: "reclaim.enabled" }))
    fireEvent.click(screen.getByRole("button", { name: "reclaim.save" }))
    await waitFor(() => expect(api.saveRacingReclaimPolicy).toHaveBeenCalledWith("instances", 7, { ...policy, enabled: false }))
    expect(api.clearRacingReclaimPolicy).not.toHaveBeenCalled()
  })
  it("restores inheritance by removing the override", async () => {
    mount(true)
    fireEvent.click(screen.getByRole("button", { name: "reclaim.inherit" }))
    await waitFor(() => expect(api.clearRacingReclaimPolicy).toHaveBeenCalledWith("instances", 7))
    expect(api.saveRacingReclaimPolicy).not.toHaveBeenCalled()
  })
})
