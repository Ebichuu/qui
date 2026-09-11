/* Copyright (c) 2026, s0up and the autobrr contributors.
 * SPDX-License-Identifier: GPL-2.0-or-later */

export interface RacingGroupInput { name: string; enabled: boolean; instanceIds: number[] }
export interface RacingGroup extends RacingGroupInput { id: number; updatedAt: string }
export interface RacingStoragePoolInput { name: string }
export interface RacingStoragePool extends RacingStoragePoolInput { id: number; updatedAt: string }
export interface RacingPathMappingInput { instanceId: number; storagePoolId: number; path: string }
export interface RacingPathMapping extends RacingPathMappingInput { id: number; updatedAt: string }
export type RacingResource = "groups" | "storage-pools" | "path-mappings" | "sites" | "sources" | "rules"
export type RacingResourceInput = RacingGroupInput | RacingStoragePoolInput | RacingPathMappingInput | RacingSiteInput | RacingSourceInput | RacingRuleInput
export interface RacingConfiguration {
  sites: RacingSite[]
  sources: RacingSource[]
  rules: RacingRule[]
  groups: RacingGroup[]
  storagePools: RacingStoragePool[]
  pathMappings: RacingPathMapping[]
}
export interface RacingObservation {
  instanceId: number
  observedAt?: string
  fresh: boolean
  healthy: boolean
  downloadSpeed?: number
  uploadSpeed?: number
  defaultPathFreeBytes?: number
  version: string
  webAPIVersion: string
  metadataObservedAt?: string
  preferencesObservedAt?: string
  preferencesFresh: boolean
  savePath: string
  tempPath: string
  tempPathEnabled: boolean
  categories: Record<string, { name: string; savePath: string }>
}
export interface RacingObservations {
  revision: number
  instances: RacingObservation[]
  storagePools: { storagePoolId: number; freeBytes?: number; observedAt?: string }[]
}

export type RacingKind = "rss" | "web" | "revival"
export type RacingAcceptKind = "official" | "free" | "revival"
export interface RacingSiteInput {
  name: string; baseUrl: string; enabled: boolean; trackerHosts: string[]; requestIntervalSeconds: number; credential?: string
}
export interface RacingSite extends Omit<RacingSiteInput, "credential"> { id: number; hasCredential: boolean; updatedAt: string }
export interface RacingSourceInput {
  siteId: number; name: string; kind: RacingKind; enabled: boolean; intervalSeconds: number
  adapter: string; pageCount: number; initialLookbackSeconds: number; url?: string
}
export interface RacingSource extends Omit<RacingSourceInput, "url"> { id: number; urlOrigin: string; updatedAt: string }
export interface RacingRuleInput {
  name: string; enabled: boolean; sortOrder: number; sourceIds: number[]; acceptKinds: RacingAcceptKind[]
  filters: { minSizeBytes?: number; maxSizeBytes?: number; includeKeywords: string[]; excludeKeywords: string[] }
  receiveWindowSeconds: number; targetGroupId?: number; targetInstanceId?: number; allowOfficialReclaim: boolean
}
export interface RacingRule extends RacingRuleInput { id: number; updatedAt: string }
export interface RacingCapability { id: string; site: string; kinds: RacingKind[]; pagination: boolean; official: boolean; free: boolean; revival: boolean; validation: string }
export interface RacingDiscoveries {
  sources: { sourceId: number; lastSuccessAt?: string; nextAttemptAt?: string; inFlight: boolean; lastError?: string; lastItemCount: number }[]
  capabilities: RacingCapability[]
}
export interface RacingCandidateRecord {
  key: string; siteId: number; firstSeenAt: string; updatedAt: string
  candidate: { sourceIds: number[]; item: { title: string; sizeBytes?: number; official: { value: string }; free: { value: string }; revival: { value: string } } }
  selection: { state: "rejected" | "waiting_metadata" | "waiting_target" | "expired" | "ready"; reason: string; priority: string; rule?: RacingRule; missingFields: string[]; deadline?: string }
}
export interface RacingCandidates { items: RacingCandidateRecord[]; nextCursor?: string }

export interface RacingInstancePolicy {
  instanceId: number; enabled: boolean; maxConcurrentAdds: number; maxActiveDownloads: number
  minFreeBytes: number; savePath: string; category: string; autoTMM: boolean; startPaused: boolean; updatedAt?: string
}
export type RacingIntentState = "reserved" | "submitted" | "unknown" | "accepted" | "confirmed" | "cancelled" | "retired"
export interface RacingAddIntent {
  candidateKey: string; instanceId: number; state: RacingIntentState; reason: string
  plan: { siteId: number; rule: RacingRule; policy: RacingInstancePolicy; sizeBytes: number; hashV1?: string; hashV2?: string; poolIds: number[]; firstSeenAt: string; deadline: string }
  reservedAt: string; submittedAt?: string; acceptedAt?: string; confirmedAt?: string; runnableAt?: string; transferredAt?: string; observedAt?: string; updatedAt: string
}
export interface RacingAddIntents { items: RacingAddIntent[]; nextCursor?: string }
