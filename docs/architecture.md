# Architecture Notes

Internal reference for agents and maintainers. Read this before changing cross-module data flow, service boundaries, API routing, or long-lived architecture. For user-facing docs, use `documentation/docs/`.

## Module Map

- `cmd/qui/main.go`: CLI entrypoint for serve, config generation, user creation, and other commands.
- `internal/api/`: HTTP handlers, middleware, and routing.
- `internal/qbittorrent/`: qBittorrent client pool and sync manager.
- `internal/services/`: domain services such as cross-seed, Jackett/Torznab, reannounce, and tracker rules.
- `internal/fsops/`: filesystem backend abstraction. Service callsites are migrating from direct `os.*` calls to an `fsops.Backend` (migration in #1915) resolved per instance via `fsops.Pool` — the local backend for instances with local filesystem access, a noop backend (every op returns `ErrNoFilesystemAccess`) otherwise. A future SSH-backed remote backend slots in at the pool (design: `docs/remote-backend-design.md`).
- `internal/proxy/`: reverse proxy support for external apps.
- `internal/backups/`: scheduled snapshots.
- `internal/database/`: SQLite/Postgres migrations and database setup.
- `internal/models/`: data models and store interfaces.
- `pkg/`: shared utilities.
- `web/src/`: React 19, Vite, TypeScript, and Tailwind frontend.

## Core Data Flow

1. The existing per-instance sync loops poll qBittorrent through `SyncManager` and `ClientPool`. Active instances have an application-owned background consumer as well as optional SSE consumers; closing the last page does not stop background sync.
2. Torrent state is cached in memory with delta updates.
3. Frontend reads state through REST APIs and receives live updates through SSE. Racing receives the same notifications and reads a small projection of the shared cache, without keeping another full torrent snapshot.
4. Cross-seed services react to torrent completion and search/match events.

## Frontend Live State Note

`web/src/components/torrents/TorrentDetailsPanel.tsx` live row state such as speed, progress, ratio, and state is stream-backed via `useSyncStream`. Polling only runs as a fallback while the stream is unavailable. Content/files and Peers tabs still poll on an interval, but polling is tab-scoped and visibility-gated, so streaming them is optional future work rather than a pending migration.

## Racing Source Observation

Racing owns an application-scoped worker per enabled source. RSS emits complete items and web adapters emit a page before fetching the next. Site request budgets serialize starts without waiting for prior responses. Each observation is committed separately with a configuration revision fence; encrypted transport data is separate from the API projection. Persistent source baselines survive restarts, while unknown event times remain ineligible. This layer has no downloader mutation capability. Identity correlation and rule selection consume each saved observation. Per-event locks serialize evaluation without blocking unrelated events. Durable evaluated revisions recover missed wakeups; bounded scans handle configuration changes and time-based reevaluation. A candidate preserves its first-seen time across source removal and restart.

Rule selection stores one complete rule and a read-only decision. It has no allocation or downloader mutation authority. Missing exact size or conflicting reported hashes can trigger an individual metainfo fetch through two bounded workers and the shared site budget. Public proofs contain name, size and hashes of the original info bytes; raw metainfo stays encrypted because it may contain private announce data. C08 must recheck current configuration, live capacity and existing operations before any reservation or add.

## Racing Reception

C08 adds explicit per-instance reception policies, disabled by default. The application-owned executor consumes the existing MainData cache through a transient projection. Free space and torrent progress are read from one atomic MainData copy; no second poller or retained full torrent cache is introduced. Pool budgets include paused/queued remaining writes and cross-disk final copies. A capacity sample must not precede the latest relevant member state.

Configuration writes increment a database revision under the same short lock used for intent transitions. Reservation and submission check the revision, candidate evidence, instance activation, deadlines and shared commitments. Neither lock spans network I/O. Raw metainfo is copied into encrypted intent storage, so later source changes cannot rewrite a submitted request. Cached candidate metainfo is fenced by source and site revisions.

Repeated identical candidate evaluations preserve the version used by reservations; changed evidence, decisions and refreshed metadata invalidate it.

Submission commits before calling qB. Racing uses a single-attempt qB request client sharing the existing transport and authenticated cookie jar; automatic POST retries and redirects are disabled for this path. Only a reserved intent can cross that boundary; submitted or unknown work is reconciled on its original instance after timeout/restart. Each instance has bounded independent execution slots. Already-confirmed tasks consume the shared snapshot without occupying these network slots; accepted/submitted requests reconcile once per second, while unknown/reserved retries remain bounded to once per five seconds. Exact hash and full site/account Tracker URL confirmation precedes task confirmation; HTTP acceptance, runnable state and observed transfer remain separate timestamps. Existing torrent-added notifications use the application's asynchronous notification pipeline.

Confirmed reservations remain as durable fallback promises. When a current budget snapshot contains the confirmed task, its actual remaining writes replace the original promise for that calculation. A missing/stale task cannot silently erase an unresolved commitment. Intent instance IDs deliberately retain historical identity after configuration removal; pool commitments restrict deletion of referenced storage pools.
