/* Copyright (c) 2026, s0up and the autobrr contributors.
 * SPDX-License-Identifier: GPL-2.0-or-later */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { TooltipProvider } from "@/components/ui/tooltip"
import { api } from "@/lib/api"
import { ReceptionForm } from "./DownloaderReception"

vi.mock("@/lib/api", () => ({ api: { saveRacingReceptionPolicy: vi.fn().mockResolvedValue(undefined) } }))
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }))
beforeEach(() => vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} }))
afterEach(() => { cleanup(); vi.clearAllMocks(); vi.unstubAllGlobals() })
function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(<QueryClientProvider client={client}><TooltipProvider><ReceptionForm instanceId={7} onClose={() => {}} /></TooltipProvider></QueryClientProvider>)
}
describe("downloader reception policy", () => {
  it("requires explicit opt-in and saves disabled by default", async () => {
    mount()
    expect(screen.getByRole("checkbox", { name: "execution.enableLabel" }).getAttribute("aria-checked")).toBe("false")
    expect(screen.queryByText("execution.enableWarning")).toBeNull()
    fireEvent.click(screen.getByRole("button", { name: "central.save" }))
    await waitFor(() => expect(api.saveRacingReceptionPolicy).toHaveBeenCalledWith(expect.objectContaining({ instanceId: 7, enabled: false, maxConcurrentAdds: 1, maxActiveDownloads: 8 })))
  })
  it("shows takeover warning and clears manual path when automatic management is selected", async () => {
    mount()
    fireEvent.change(screen.getByRole("textbox", { name: "execution.savePath" }), { target: { value: "/data/films" } })
    fireEvent.click(screen.getByRole("checkbox", { name: "execution.enableLabel" }))
    expect(screen.getByText("execution.enableWarning")).not.toBeNull()
    fireEvent.click(screen.getByRole("checkbox", { name: "execution.autoTMM" }))
    expect(screen.queryByRole("textbox", { name: "execution.savePath" })).toBeNull()
    fireEvent.click(screen.getByRole("button", { name: "central.save" }))
    await waitFor(() => expect(api.saveRacingReceptionPolicy).toHaveBeenCalledWith(expect.objectContaining({ enabled: true, autoTMM: true, savePath: "" })))
  })
})
