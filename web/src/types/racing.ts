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
  instanceId: number; enabled: boolean; reclaimEnabled?: boolean; maxConcurrentAdds: number; maxActiveDownloads: number
  minFreeBytes: number; savePath: string; category: string; autoTMM: boolean; startPaused: boolean; updatedAt?: string
}
export type RacingIntentState = "reserved" | "submitted" | "unknown" | "accepted" | "confirmed" | "cancelled" | "retired"
export interface RacingDecisionEvidence {
  algorithm: string; candidate: Record<string, unknown>; selection: Record<string, unknown>; selectedRank: number
  targets: { instanceId: number; observedAt: string; downloadSpeed: number; uploadSpeed: number; active: number; pending: number; availableBytes: number; lastReserved: string; poolIds: number[] }[]
}
export interface RacingAddIntent {
  candidateKey: string; instanceId: number; state: RacingIntentState; reason: string
  plan: { name?: string; decision?: RacingDecisionEvidence; siteId: number; rule: RacingRule; policy: RacingInstancePolicy; sizeBytes: number; hashV1?: string; hashV2?: string; poolIds: number[]; firstSeenAt: string; deadline: string }
  reservedAt: string; submittedAt?: string; acceptedAt?: string; confirmedAt?: string; runnableAt?: string; transferredAt?: string; observedAt?: string; updatedAt: string
}
export interface RacingAddIntents { items: RacingAddIntent[]; nextCursor?: string }

export interface RacingReclaimPolicy {
  enabled: boolean; ruleIds: number[]; maxDeletes: number; maxReclaimBytes: number
  maxRecentUploadBytes: number; recentUploadWindowSeconds: number; maxOvershootBytes: number
}
export interface RacingReclaimConfiguration {
  revision: number
  settings: { scope: "instances" | "groups"; targetId: number; policy: RacingReclaimPolicy }[]
  effective: { instanceId: number; state: "unconfigured" | "explicit" | "inherited" | "disabled" | "conflict"; groupIds: number[]; policy?: RacingReclaimPolicy }[]
}

export interface RacingAnalysisSetting { instanceId: number; enabled: boolean }
export interface RacingASNObservation {
  state: "found" | "not_found" | "error"; number?: number; organization?: string; network?: string
  observedAt: string; databaseBuiltAt: string; databaseSHA256: string
}
export interface RacingPeerHistory {
  asn?: RacingASNObservation
  ip: string; port: number; firstSeen: string; lastSeen: string; absentAt?: string
  samples: number; maxProgress?: number; firstComplete?: string; downloaded: number; uploaded: number; counterResets: number
}
export interface RacingAnalysisHistory {
  candidateKey: string; instanceId: number; hash: string; hashV1: string; hashV2: string; addedOn: number
  startedAt: string; lastVisible?: string; lastSuccess?: string; truncated: boolean
  expectedSamples: number; successfulSamples: number; missingSamples: number
  asnState: "unavailable" | "partial" | "available"; rankingState: "unsupported"
  samples: { at: string; state: string }[]; peers: Record<string, RacingPeerHistory>
}
