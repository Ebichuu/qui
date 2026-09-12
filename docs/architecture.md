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

## Automation Observation Recovery

Automation applies and explicit dry runs serialize per instance. Completed condition observations persist the rule definition hash, torrent hash/added-on generation, measured duration, last observation and counters. Restore excludes the unobserved restart gap; stale samples, counter rollback, rule edits and changed task generations invalidate qualification. Before a full scan, its old checkpoint is removed so a crash during incomplete evaluation cannot restore old maturity. Checkpoints are saved before live actions; database errors abort the evaluation. FREE_SPACE deletion attempts persist the existing cooldown before network I/O. This adds recovery to existing workflows, not a new reclaim executor or transfer of deletion authority.

## Reclaim Configuration

Reclaim settings are separate from reception opt-ins and RSS rules. A single instance override wins over all enabled group defaults; identical defaults deduplicate, different defaults return a conflict without a policy. Explicit disable suppresses inheritance, while removing the override restores it. Policy writes use the racing configuration revision transaction. Automation references use stable IDs and restrictive foreign keys. The current API stores and resolves configuration only: candidate-expression conversion, budget consumption, deletion ownership and execution are not supplied by a saved policy.

## Daily Trigger Separation

Delete actions optionally carry a `dailyTrigger` alongside the existing candidate `condition`. The processor sends only the candidate result through the sustained observation gate, then checks the daily trigger before scheduling deletion. Preview, condition dependency loading, grouping validation and FREE_SPACE cooldown classification use the conjunction of both trees. Legacy combined expressions remain unchanged. Rule edits continue to invalidate observation versions. The same workflow editor preserves both expressions; official candidate evaluation and shared deletion ownership remain subsequent C10 work.

## Automatic Delete Ownership

Automatic callers pass the evaluated hash and added-on generation through SyncManager.AutomaticDelete. BulkAction resolves variants, checks fresh state and persists a batch under the racing ledger lock before sending through the single-attempt client. Daily workflows and failed-export cleanup share this boundary. Accepted requests await fresh absence; unknown requests retain ownership for explicit reconciliation. Confirmed generations remain tombstones. Manual requests and the qB proxy are external changes. Task absence does not certify physical space release, and automatic requests do not optimistically remove the task from the observed cache.

### Official reclaim observations

Delete usage defaults to daily; official and both explicitly permit candidate reuse. A lightweight observer per target instance reuses the existing evaluator and qB cache, strips unrelated actions and the additional daily trigger, and returns before action dispatch. Its measured timers persist in `reclaim_condition_observations`, separate from daily/dry-run checkpoints. Target settings can reference a stable definition on another instance, without inheriting that instance's operation authority. Unknown generation, incomplete data, unsupported deletion expansion and mixed FREE_SPACE expressions cannot qualify. The candidate API observes only; logical bytes are not evidence of physical yield.

Downloader settings expose explicit overrides, group defaults and conflict state. Clearing an override restores inheritance; saving disabled keeps an explicit stop. These settings currently feed observation only, pending budgeted execution and site protections.

### Official shortage assessments

The reception runner can launch one independent, bounded reclaim assessment worker when an actual official event has reclaim permission but no direct-capacity target. Existing health, target membership, active-slot and layout checks remain in the shared target selector. The shared budget keeps negative balances so earlier overcommitment remains part of the deficit. The observer persists historical assessment JSON under event/instance with a configuration-revision check; it never reserves space or claims deletion ownership. Addition and reconciliation keep their existing worker slots.

Reclaim observations use a real successful qB snapshot, excluding optimistic UI state. Upload windows retain bounded persistent samples by task generation; gaps and observed counter regression invalidate coverage. Linux local-file evidence requires exclusive native extents and complete, fresh inventories across configured pool members. Unsupported filesystems and uncertain ownership remain unknown. These values are estimates for assessment, not confirmation of physical release. A feasible combination also needs complete Tracker protection evidence and a subsequent execution plan; no reclaim deletion is dispatched.

### Tracker observations

Reannounce jobs persist their latest queried Tracker snapshot per task, with the observed task generation and separate local-counter sample time. Exact URL and message digests preserve identity without storing private announce credentials. Older snapshots cannot replace newer rows. These records describe qB reports, not announce receipts or site-accounting confirmation. Read-only history survives restart but grants no deletion or reannounce authority; the shared account constraints are described below.

Bulk actions and the qB proxy dispatch through the same reannounce service. The existing instance monitoring scope determines which tasks join its queue; matching tasks never fall back to direct forwarding on waits or errors. Before a monitoring job sends, it rechecks current settings and task generation and claims a persistent per-instance/hash interval. The single-attempt client sends once after that claim; failures retain the interval. Account policies additionally constrain managed tasks as described below; operation ownership transfer remains pending.


### Exact Tracker account constraints

The reannounce store binds an instance and exact URL digest to an enabled RacingSite and host. Host matches without an exact account binding trigger conservative coverage failure. Each managed torrent requires complete real-Tracker coverage because qB sends torrent-wide requests. A disappearing previously observed bound Tracker blocks further operations. Site edits are validated when policies are read; invalid bindings are not treated as unmanaged.

Per-generation Tracker waits retain deadlines across observations, restart and shorter settings. The token combines the exact message digest with the persisted send epoch; this recognizes configured responses without retaining their raw text. The send boundary refreshes the task and Trackers, rereads policies, then claims the durable interval before one HTTP attempt. It cannot atomically condition qB network operations on a generation or eliminate external changes between observations.

AutomaticDelete invokes the shared service guard before its persistent ownership claim and rechecks local generation afterward. Unmanaged daily tasks preserve existing behavior. Reclaim assessment requires complete coverage and otherwise keeps candidates protected. The explicit reported_working condition accepts only qB state; blocked and accounted never infer site-accounting evidence. A feasible protected assessment advances only to awaiting_execution_plan, not deletion. Policy configuration has an API and an independent form in the existing reannounce settings. Listing includes invalid bindings for repair; execution uses the validated read. The editor verifies a new or changed site binding against a locally hashed Tracker address, and sends only digests for known responses. No removal endpoint exists.
