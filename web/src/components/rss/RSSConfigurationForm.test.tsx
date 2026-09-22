/* Copyright (c) 2026, s0up and the autobrr contributors.
 * SPDX-License-Identifier: GPL-2.0-or-later */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { TooltipProvider } from "@/components/ui/tooltip"
import { api } from "@/lib/api"
import type { RacingConfiguration, RacingCapability } from "@/types/racing"
import { RSSConfigurationForm, type RSSSelection } from "./RSSConfigurationForm"

vi.mock("@/lib/api", () => ({ api: { saveRacingResource: vi.fn() } }))
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }))
const config: RacingConfiguration = { sites: [{ id: 1, name: "Site", baseUrl: "https://example.invalid", enabled: true, trackerHosts: [], requestIntervalSeconds: 5, hasCredential: true, updatedAt: "" }], sources: [{ id: 2, siteId: 1, name: "Feed", kind: "rss", enabled: true, intervalSeconds: 30, adapter: "chd", pageCount: 1, initialLookbackSeconds: 0, urlOrigin: "https://example.invalid", updatedAt: "" }], rules: [], groups: [{ id: 3, name: "Group", enabled: true, instanceIds: [7], updatedAt: "" }], storagePools: [], pathMappings: [] }
const capabilities: RacingCapability[] = [{ id: "chd", site: "CHDBits", kinds: ["rss", "web", "revival"], pagination: true, official: true, free: true, revival: true, validation: "synthetic_fixtures" }]
beforeEach(() => { vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} }); vi.mocked(api.saveRacingResource).mockResolvedValue({ id: 1 }) })
afterEach(() => { cleanup(); vi.resetAllMocks(); vi.unstubAllGlobals() })
function mount(selection: RSSSelection, configuration = config) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(<QueryClientProvider client={client}><TooltipProvider><RSSConfigurationForm selection={selection} config={configuration} instances={[]} capabilities={capabilities} onClose={() => {}} /></TooltipProvider></QueryClientProvider>)
}
describe("central RSS configuration", () => {
  it.each([
    { sourceEnabled: false, siteEnabled: true, warning: true },
    { sourceEnabled: true, siteEnabled: false, warning: true },
    { sourceEnabled: true, siteEnabled: true, warning: false },
  ])("checks the enabled source and site for revival: %j", ({ sourceEnabled, siteEnabled, warning }) => {
    mount({ resource: "rules", id: null }, {
      ...config,
      sites: config.sites.map(site => ({ ...site, enabled: siteEnabled })),
      sources: config.sources.map(source => ({ ...source, enabled: sourceEnabled })),
    })
    fireEvent.click(screen.getByRole("checkbox", { name: /Feed/ }))
    fireEvent.click(screen.getByRole("checkbox", { name: "central.accept.revival" }))
    expect(screen.queryByText("central.revivalUnavailable") !== null).toBe(warning)
  })

  it("keeps revival available when another selected source is disabled", () => {
    mount({ resource: "rules", id: null }, {
      ...config,
      sources: [...config.sources, { ...config.sources[0], id: 4, name: "Disabled source", enabled: false }],
    })
    fireEvent.click(screen.getByRole("checkbox", { name: "Feed" }))
    fireEvent.click(screen.getByRole("checkbox", { name: /Disabled source/ }))
    fireEvent.click(screen.getByRole("checkbox", { name: "central.accept.revival" }))
    expect(screen.queryByText("central.revivalUnavailable")).toBeNull()
    fireEvent.click(screen.getByRole("checkbox", { name: "Feed" }))
    expect(screen.getByText("central.revivalUnavailable")).toBeTruthy()
  })

  it("requires explicit reception types and window, and saves one complete group target", async () => {
    mount({ resource: "rules", id: null })
    expect((screen.getByRole("button", { name: "central.save" }) as HTMLButtonElement).disabled).toBe(true)
    fireEvent.change(screen.getByLabelText("central.name"), { target: { value: "Official or free" } })
    fireEvent.click(screen.getByRole("checkbox", { name: "Feed" }))
    fireEvent.click(screen.getByRole("checkbox", { name: "central.accept.official" }))
    fireEvent.click(screen.getByRole("checkbox", { name: "central.accept.free" }))
    fireEvent.change(screen.getByLabelText("central.target"), { target: { value: "group:3" } })
    expect((screen.getByRole("button", { name: "central.save" }) as HTMLButtonElement).disabled).toBe(true)
    fireEvent.change(screen.getByLabelText("central.window"), { target: { value: "720" } })
    fireEvent.click(screen.getByRole("button", { name: "central.save" }))
    await waitFor(() => expect(api.saveRacingResource).toHaveBeenCalledWith("rules", null, expect.objectContaining({ sourceIds: [2], acceptKinds: ["official", "free"], receiveWindowSeconds: 720, targetGroupId: 3, allowOfficialReclaim: false, enabled: false })))
    expect(vi.mocked(api.saveRacingResource).mock.calls[0][2]).not.toHaveProperty("targetInstanceId")
  })
  it("preserves a stored source URL on edit without sending the masked origin as the full URL", async () => {
    mount({ resource: "sources", id: 2 })
    fireEvent.change(screen.getByLabelText("central.name"), { target: { value: "Renamed" } })
    fireEvent.click(screen.getByRole("button", { name: "central.save" }))
    await waitFor(() => expect(api.saveRacingResource).toHaveBeenCalled())
    expect(vi.mocked(api.saveRacingResource).mock.calls[0][2]).not.toHaveProperty("url")
  })
  it("only clears a stored credential through the explicit clear control", async () => {
    mount({ resource: "sites", id: 1 })
    fireEvent.click(screen.getByRole("button", { name: "central.save" }))
    await waitFor(() => expect(api.saveRacingResource).toHaveBeenCalledTimes(1))
    expect(vi.mocked(api.saveRacingResource).mock.calls[0][2]).not.toHaveProperty("credential")
    fireEvent.click(screen.getByRole("checkbox", { name: "central.clearCredential" }))
    fireEvent.click(screen.getByRole("button", { name: "central.save" }))
    await waitFor(() => expect(api.saveRacingResource).toHaveBeenCalledTimes(2))
    expect(vi.mocked(api.saveRacingResource).mock.calls[1][2]).toHaveProperty("credential", "")
  })
})
