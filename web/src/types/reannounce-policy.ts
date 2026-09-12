/* Copyright (c) 2026, s0up and the autobrr contributors.
 * SPDX-License-Identifier: GPL-2.0-or-later */

export interface ReannounceTrackerPolicy {
  trackerKey: string
  siteId: number
  trackerHost: string
  intervalSeconds: number
  waitMessageDigest: string
  waitSeconds: number
  deleteProtection: "blocked" | "reported_working" | "accounted"
}

export interface ReannounceObservation {
  hash: string
  addedOn: number
  observedAt: string
  trackers: { key: string; host: string; state: string; messageDigest?: string }[]
}
