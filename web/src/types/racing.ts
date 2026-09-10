/* Copyright (c) 2026, s0up and the autobrr contributors.
 * SPDX-License-Identifier: GPL-2.0-or-later */

export interface RacingGroupInput { name: string; enabled: boolean; instanceIds: number[] }
export interface RacingGroup extends RacingGroupInput { id: number; updatedAt: string }
export interface RacingStoragePoolInput { name: string }
export interface RacingStoragePool extends RacingStoragePoolInput { id: number; updatedAt: string }
export interface RacingPathMappingInput { instanceId: number; storagePoolId: number; path: string }
export interface RacingPathMapping extends RacingPathMappingInput { id: number; updatedAt: string }
export type RacingResource = "groups" | "storage-pools" | "path-mappings"
export type RacingResourceInput = RacingGroupInput | RacingStoragePoolInput | RacingPathMappingInput
export interface RacingConfiguration {
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
