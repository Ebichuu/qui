/* Copyright (c) 2026, s0up and the autobrr contributors.
 * SPDX-License-Identifier: GPL-2.0-or-later */
import { afterEach, describe, expect, it, vi } from "vitest"
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { PeerHistory } from "./PeerHistory"

const state = vi.hoisted(() => ({ history: vi.fn() }))
vi.mock("@/lib/api", () => ({ api: { getRacingAnalysisHistory: state.history }, APIError: class extends Error {} }))
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string, values?: { number: number; organization: string }) => key === "analysis.asnValue" && values ? `AS${values.number} · ${values.organization}` : key }) }))
afterEach(() => { cleanup(); vi.resetAllMocks() })

describe("Peer history requests", () => {
  it("distinguishes ASN matches, missing records, failures and absent evidence", async () => {
    const when = "2026-09-01T00:00:00Z"
    const peer = { firstSeen: when, lastSeen: when }
    state.history.mockResolvedValue({ peers: {
      "192.0.2.1:6881": { ...peer, asn: { state: "found", number: 64512, organization: "Synthetic ASN", observedAt: when, databaseBuiltAt: when } },
      "192.0.2.2:6881": { ...peer, asn: { state: "not_found" } },
      "192.0.2.3:6881": { ...peer, asn: { state: "error" } },
      "192.0.2.4:6881": peer,
    } })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
    render(<QueryClientProvider client={client}><PeerHistory candidateKey="synthetic:asn" /></QueryClientProvider>)
    fireEvent.click(screen.getByRole("button", { name: "analysis.show" }))
    expect(await screen.findByText("AS64512 · Synthetic ASN")).toBeTruthy()
    expect(screen.getByText("analysis.asnMissing")).toBeTruthy()
    expect(screen.getByText("analysis.asnError")).toBeTruthy()
    expect(screen.getByText("analysis.asnUnavailable")).toBeTruthy()
    expect(screen.getByText("analysis.asnEvidence")).toBeTruthy()
    client.clear()
  })

  it("only reads the selected candidate while expanded", async () => {
    state.history.mockResolvedValue({ peers: {}, successfulSamples: 0, expectedSamples: 10, missingSamples: 10 })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
    render(<QueryClientProvider client={client}><PeerHistory candidateKey="synthetic:candidate" /></QueryClientProvider>)
    expect(state.history).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole("button", { name: "analysis.show" }))
    expect(await screen.findByText("analysis.coverage")).toBeTruthy()
    expect(state.history).toHaveBeenCalledWith("synthetic:candidate")
    await client.invalidateQueries({ queryKey: ["racing-analysis-history"] })
    await waitFor(() => expect(state.history).toHaveBeenCalledTimes(2))
    fireEvent.click(screen.getByRole("button", { name: "analysis.show" }))
    await client.invalidateQueries({ queryKey: ["racing-analysis-history"] })
    expect(state.history).toHaveBeenCalledTimes(2)
    expect(screen.queryByText("analysis.coverage")).toBeNull()
    client.clear()
  })

  it("reports a failed history request without pretending no peers were observed", async () => {
    state.history.mockRejectedValue(new Error("synthetic failure"))
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
    render(<QueryClientProvider client={client}><PeerHistory candidateKey="synthetic:failed" /></QueryClientProvider>)
    fireEvent.click(screen.getByRole("button", { name: "analysis.show" }))
    expect(await screen.findByText("execution.loadError")).toBeTruthy()
    expect(screen.queryByText("analysis.coverage")).toBeNull()
    expect(screen.queryByText("analysis.empty")).toBeNull()
    client.clear()
  })
})
