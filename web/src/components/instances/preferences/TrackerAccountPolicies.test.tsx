import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { createHash, webcrypto } from "node:crypto"
import { TrackerAccountPolicies } from "./TrackerAccountPolicies"

const mock = vi.hoisted(() => ({
  policies: vi.fn(), observations: vi.fn(), config: vi.fn(), save: vi.fn(), success: vi.fn(),
}))
vi.mock("@/lib/api", () => ({ api: { getReannounceTrackerPolicies: mock.policies, getReannounceObservations: mock.observations, getRacingConfiguration: mock.config, saveReannounceTrackerPolicy: mock.save } }))
vi.mock("react-i18next", () => ({ useTranslation: () => ({ t: (key: string) => key }) }))
vi.mock("sonner", () => ({ toast: { success: mock.success } }))
vi.mock("@/components/ui/field-help", () => ({ FieldHelp: () => null }))

const addressA = "https://tracker.example.invalid/announce?account=synthetic-a"
const addressB = "https://tracker.example.invalid/announce?account=synthetic-b"
const trackerA = createHash("sha256").update(addressA).digest("hex")
const trackerB = createHash("sha256").update(addressB).digest("hex")

beforeEach(() => {
  vi.clearAllMocks()
  vi.stubGlobal("crypto", webcrypto)
  mock.policies.mockResolvedValue([])
  mock.observations.mockResolvedValue([{ hash: "synthetic-hash", addedOn: 100, observedAt: "2026-01-01T00:00:00Z", trackers: [{ key: trackerA, host: "tracker.example.invalid", state: "error_unknown" }, { key: trackerB, host: "tracker.example.invalid", state: "reported_working" }] }])
  mock.config.mockResolvedValue({ sites: [{ id: 1, name: "Synthetic account", enabled: true, trackerHosts: ["tracker.example.invalid"] }, { id: 2, name: "Unrelated", enabled: true, trackerHosts: ["other.invalid"] }] })
  mock.save.mockResolvedValue(undefined)
})
afterEach(() => { cleanup(); vi.unstubAllGlobals() })

function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const view = render(<QueryClientProvider client={client}><TrackerAccountPolicies instanceId={7} /></QueryClientProvider>)
  return { ...view, client }
}

async function selectTracker(key = trackerA) {
  fireEvent.click(screen.getByRole("button", { name: "trackerPolicies.title" }))
  fireEvent.change(await screen.findByLabelText("trackerPolicies.tracker"), { target: { value: key } })
}
function confirmAndSubmit(container: HTMLElement, address?: string) {
  const addressInput = screen.queryByLabelText("trackerPolicies.trackerAddress")
  if (addressInput) fireEvent.change(addressInput, { target: { value: address ?? ((screen.getByLabelText("trackerPolicies.tracker") as HTMLSelectElement).value === trackerA ? addressA : addressB) } })
  fireEvent.click(screen.getByLabelText("trackerPolicies.confirm"))
  fireEvent.submit(container.querySelector("form")!)
}

describe("TrackerAccountPolicies", () => {
  it("loads on expansion and keeps same-host accounts separate with blocked deletion by default", async () => {
    const view = mount()
    expect(mock.policies).not.toHaveBeenCalled()
    await selectTracker(trackerB)
    expect(screen.getByRole("option", { name: new RegExp(trackerA.slice(0, 12)) })).toBeTruthy()
    expect(screen.getByRole("option", { name: new RegExp(trackerB.slice(0, 12)) })).toBeTruthy()
    expect(screen.queryByRole("option", { name: "Unrelated" })).toBeNull()
    fireEvent.change(screen.getByLabelText("trackerPolicies.site"), { target: { value: "1" } })
    confirmAndSubmit(view.container)
    await waitFor(() => expect(mock.save).toHaveBeenCalledWith(7, { trackerKey: trackerB, trackerHost: "tracker.example.invalid", siteId: 1, intervalSeconds: 120, waitMessageDigest: "", waitSeconds: 0, deleteProtection: "blocked" }))
    expect(view.container.querySelector("form form")).toBeNull()
  })

  it("hashes an exact known response locally and never sends the original text", async () => {
    const view = mount()
    await selectTracker()
    fireEvent.change(screen.getByLabelText("trackerPolicies.site"), { target: { value: "1" } })
    fireEvent.click(screen.getByLabelText("trackerPolicies.knownWait"))
    const text = " Known synthetic wait "
    fireEvent.change(screen.getByLabelText("trackerPolicies.message"), { target: { value: text } })
    confirmAndSubmit(view.container)
    await waitFor(() => expect(mock.save).toHaveBeenCalledOnce())
    const expected = await webcrypto.subtle.digest("SHA-256", new TextEncoder().encode(text))
    expect(mock.save.mock.calls[0][1].waitMessageDigest).toBe(Buffer.from(expected).toString("hex"))
    expect(JSON.stringify(mock.save.mock.calls[0])).not.toContain(text)
  })

  it("preserves an existing response match and explicitly clears it when disabled", async () => {
    const policy = { trackerKey: trackerA, trackerHost: "tracker.example.invalid", siteId: 1, intervalSeconds: 20, waitMessageDigest: "c".repeat(64), waitSeconds: 30, deleteProtection: "accounted" }
    mock.policies.mockResolvedValue([policy])
    const view = mount()
    await selectTracker()
    confirmAndSubmit(view.container)
    await waitFor(() => expect(mock.save).toHaveBeenCalledWith(7, policy))
    await waitFor(() => expect(mock.success).toHaveBeenCalledOnce())
    fireEvent.click(screen.getByLabelText("trackerPolicies.knownWait"))
    confirmAndSubmit(view.container)
    await waitFor(() => expect(mock.save).toHaveBeenLastCalledWith(7, { ...policy, waitSeconds: 0, waitMessageDigest: "" }))
  })

  it("rejects fractional intervals even if native form validation is bypassed", async () => {
    const view = mount()
    await selectTracker()
    fireEvent.change(screen.getByLabelText("trackerPolicies.site"), { target: { value: "1" } })
    fireEvent.change(screen.getByLabelText("trackerPolicies.interval"), { target: { value: "1.5" } })
    confirmAndSubmit(view.container)
    expect((await screen.findByRole("alert")).textContent).toBe("trackerPolicies.invalid")
    expect(mock.save).not.toHaveBeenCalled()
  })

  it("retains edits and reports a failed save without publishing success", async () => {
    mock.save.mockRejectedValue(new Error("Synthetic failure"))
    const view = mount()
    await selectTracker()
    fireEvent.change(screen.getByLabelText("trackerPolicies.site"), { target: { value: "1" } })
    confirmAndSubmit(view.container)
    expect((await screen.findByRole("alert")).textContent).toBe("trackerPolicies.saveFailed")
    expect(mock.success).not.toHaveBeenCalled()
    expect((screen.getByLabelText("trackerPolicies.site") as HTMLSelectElement).value).toBe("1")
  })
  it("rejects a different account address before sending any policy", async () => {
    const view = mount()
    await selectTracker()
    fireEvent.change(screen.getByLabelText("trackerPolicies.site"), { target: { value: "1" } })
    confirmAndSubmit(view.container, addressB)
    expect((await screen.findByRole("alert")).textContent).toBe("trackerPolicies.invalid")
    expect(mock.save).not.toHaveBeenCalled()
  })

  it("keeps an unsaved draft when refreshed server values change", async () => {
    const policy = { trackerKey: trackerA, trackerHost: "tracker.example.invalid", siteId: 1, intervalSeconds: 20, waitMessageDigest: "", waitSeconds: 0, deleteProtection: "blocked" }
    mock.policies.mockResolvedValue([policy])
    mount()
    await selectTracker()
    fireEvent.change(screen.getByLabelText("trackerPolicies.interval"), { target: { value: "42" } })
    mock.policies.mockResolvedValue([{ ...policy, intervalSeconds: 99 }])
    fireEvent.click(screen.getByRole("button", { name: "trackerPolicies.refresh" }))
    await waitFor(() => expect(mock.policies).toHaveBeenCalledTimes(2))
    expect((screen.getByLabelText("trackerPolicies.interval") as HTMLInputElement).value).toBe("42")
  })

})
