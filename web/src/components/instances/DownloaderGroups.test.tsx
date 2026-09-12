/* Copyright (c) 2026, s0up and the autobrr contributors.
 * SPDX-License-Identifier: GPL-2.0-or-later */

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { TooltipProvider } from "@/components/ui/tooltip"
import { api } from "@/lib/api"
import { DownloaderGroups } from "./DownloaderGroups"
import type { InstanceResponse } from "@/types"

vi.mock("@/lib/api", () => ({ api: { getRacingConfiguration: vi.fn(), getRacingObservations: vi.fn(), saveRacingResource: vi.fn(), deleteRacingResource: vi.fn() } }))
vi.mock("./DownloaderReclaim", () => ({ DownloaderReclaim: () => null }))

vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }))
const instances = [{ id: 7, name: "Member A", isActive: true }, { id: 9, name: "Member B", isActive: false }] as InstanceResponse[]
const config = { sites: [], sources: [], rules: [], groups: [{ id: 2, name: "Group A", enabled: true, instanceIds: [7], updatedAt: "" }], storagePools: [], pathMappings: [] }
beforeEach(() => {
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} })
  vi.mocked(api.getRacingConfiguration).mockResolvedValue(config)
  vi.mocked(api.getRacingObservations).mockResolvedValue({ revision: 1, instances: [], storagePools: [] })
  vi.mocked(api.saveRacingResource).mockResolvedValue({ id: 2 })
})
afterEach(() => { cleanup(); vi.resetAllMocks(); vi.unstubAllGlobals() })
function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(<QueryClientProvider client={client}><TooltipProvider><DownloaderGroups instances={instances} /></TooltipProvider></QueryClientProvider>)
}
describe("DownloaderGroups", () => {
  it("keeps the group ID while saving renamed and disabled members", async () => {
    mount()
    fireEvent.click(await screen.findByRole("button", { name: "racingGroups.edit" }))
    const dialog = screen.getByRole("dialog")
    fireEvent.change(within(dialog).getByLabelText("racingGroups.name"), { target: { value: "Renamed" } })
    fireEvent.click(within(dialog).getByRole("checkbox", { name: "racingGroups.enabled" }))
    fireEvent.click(within(dialog).getByRole("checkbox", { name: /Member B/ }))
    fireEvent.click(within(dialog).getByRole("button", { name: "racingGroups.save" }))
    await waitFor(() => expect(api.saveRacingResource).toHaveBeenCalledWith("groups", 2, { name: "Renamed", enabled: false, instanceIds: [7, 9] }))
  })
  it("keeps a referenced group's deletion dialog open with an error", async () => {
    vi.mocked(api.deleteRacingResource).mockRejectedValue(new Error("Referenced"))
    mount()
    fireEvent.click(await screen.findByRole("button", { name: "racingGroups.delete" }))
    fireEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "racingGroups.delete" }))
    expect(await screen.findByRole("alert")).toBeTruthy()
    expect(screen.getByRole("alertdialog")).toBeTruthy()
    expect(api.deleteRacingResource).toHaveBeenCalledWith("groups", 2)
  })
  it("allows an empty group without inventing a fallback downloader", async () => {
    mount()
    fireEvent.click(await screen.findByRole("button", { name: "racingGroups.addGroup" }))
    const dialog = screen.getByRole("dialog")
    fireEvent.change(within(dialog).getByLabelText("racingGroups.name"), { target: { value: "Empty" } })
    fireEvent.click(within(dialog).getByRole("button", { name: "racingGroups.save" }))
    await waitFor(() => expect(api.saveRacingResource).toHaveBeenCalledWith("groups", null, { name: "Empty", enabled: true, instanceIds: [] }))
  })
})
