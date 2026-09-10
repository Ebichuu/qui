/* Copyright (c) 2026, s0up and the autobrr contributors.
 * SPDX-License-Identifier: GPL-2.0-or-later */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { cleanup, render, screen } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { TooltipProvider } from "@/components/ui/tooltip"
import { RSSPage } from "@/pages/RSSPage"

const state = vi.hoisted(() => ({ instances: [], data: { sites: [], sources: [], rules: [], groups: [], storagePools: [], pathMappings: [] }, getRSSItems: vi.fn(), getRSSRules: vi.fn() }))
vi.mock("@/hooks/useInstances", () => ({ useInstances: () => ({ instances: state.instances }) }))
vi.mock("react-i18next", async importOriginal => ({ ...await importOriginal<typeof import("react-i18next")>(), useTranslation: () => ({ t: (key: string) => key }) }))
vi.mock("@/lib/api", () => ({ api: { getRacingConfiguration: async () => state.data, getRacingDiscoveries: async () => ({ sources: [], capabilities: [] }), getRSSItems: state.getRSSItems, getRSSRules: state.getRSSRules } }))
beforeEach(() => vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} }))
afterEach(() => { cleanup(); vi.clearAllMocks(); vi.unstubAllGlobals() })
describe("RSS page scope", () => {
  it("opens central sources without a downloader and never starts per-instance RSS reads", async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><TooltipProvider><RSSPage activeTab="feeds" onTabChange={() => {}} onFeedSelect={() => {}} onRuleSelect={() => {}} /></TooltipProvider></QueryClientProvider>)
    expect(await screen.findByRole("button", { name: "central.addSite" })).toBeTruthy()
    expect(screen.queryByText("noInstances.title")).toBeNull()
    expect(state.getRSSItems).not.toHaveBeenCalled()
    expect(state.getRSSRules).not.toHaveBeenCalled()
    expect((screen.getByLabelText("central.scope") as HTMLSelectElement).value).toBe("central")
  })
})
