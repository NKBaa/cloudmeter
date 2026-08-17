# State machines

## Deployment

queued -> pulling -> starting -> health_checking -> switching_route -> succeeded

Any pre-switch execution failure may enter rolling_back. A failed health check remains in health_checking and retries at the product-defined interval, up to 8 attempts; the final diagnostic is retained on the deployment job before rolling back. rolling_back always finishes as failed after restoring the last successful route. Terminal states never transition. A database row lock and the transition validator must be used together.

A required product dependency is checked before the API queues create, update, or rollback work, and again when the Worker processes the job in `pulling`. The Worker check requires both the active route to reference the dependency application's last successful Release and its tracked Docker container to be healthy. If the dependency disappeared while the job was queued, the job and target Release move directly to `failed`; an application with a previous successful Release returns to `running` and keeps its existing route.

Application Secret names are selected only from keys declared by a published version of the application's product. Each Release references immutable Secret versions only for its target runtime declaration. Before container creation, the Worker rebuilds that declared set, fails closed on a missing or malformed declared reference, verifies each referenced version belongs to the same application and key, and ignores every undeclared legacy entry in the snapshot.

The user slug and application slug form the permanent public route identity. The application owner, product, stable service slug, and creation time are immutable, and `last_successful_release_id` can only reference a Release owned by that application. An active route can only reference that same successful Release while it is `active` and the application is `running` or `updating`; its public path, release alias, port, and tracked container must be derivable from protected identity and the immutable snapshot.

## Product version test

queued -> pulling -> starting -> health_checking -> succeeded

`queued`、`pulling` 或 `starting` 中的执行错误，以及连续 8 次未通过的健康检查，都会进入 `failed`。`health_checking` 在未通过且尚未达到上限时保持自身状态，并按版本声明的检查间隔重试。`succeeded` 和 `failed` 是终态，测试快照与完成记录不可修改。

每个产品版本最多有一个非终态测试。发布转换要求该版本至少有一条 `succeeded` 测试记录；这一规则同时由 API 和数据库触发器强制。测试 Secret 仅能在终态转换时被清空，永不从读接口返回。

## Product catalog lifecycle

`draft -> testing -> published -> retired`。`draft`、`testing` 和 `published` 都可以软下架为 `retired`；恢复时根据不可变历史回到 `published`（存在已发布版本）、`testing`（只有测试历史）或 `draft`（从未测试）。测试进行中禁止下架；已上架依赖方仍引用该产品时禁止下架。恢复 `published` 产品前重新校验所有已发布版本的依赖。下架只改变目录可用性，不修改或删除现有应用、Release 或产品版本。名称可以审计地修改；产品 `id`、标识与创建时间由数据库触发器保持稳定。

## Backup and restore

A backup follows `queued -> running -> succeeded | failed`. A restore normally follows `queued -> running -> succeeded | failed`; subscription expiry may cancel a queued restore directly as `queued -> failed`. Backup identity (application, volume key, Docker volume, storage key, and creation time) and restore identity (application, backup, idempotency key, and creation time) never change. Every restore must reference a successful backup owned by the same application. New successful backups must record the observed archive size; a pre-accounting legacy backup may retain an unknown size. Terminal result metadata is immutable, and backup/restore history cannot be deleted or truncated.

Backup storage entitlement checks use exact decimal arithmetic both when the API reserves capacity and when the Worker observes the completed archive size. The boundary is inclusive, invalid quota data fails closed, and integer addition cannot wrap around.

## Order

pending -> paid -> refunding -> refunded. A pending order may instead become closed. A failed refund returns refunding -> paid. Provider callbacks never assign a state directly; they invoke the same transactional transition service as manual operations. Administrator provider query operations never change the order state; provider-confirmed close changes pending -> closed and records an immutable provider operation plus audit event.

## Refund

processing -> succeeded | failed. Each payment order has at most one refund record. A successful full refund, its two append-only events, the negative wallet ledger entry, the order transitions, and the administrator audit entry share one database transaction. The payment order row is locked before the refund starts, so concurrent or repeated requests return the same terminal record and cannot debit the wallet twice. A precondition failure such as insufficient wallet balance leaves both the paid order and refund tables unchanged.

The database defers order/refund and refund/event alignment checks until transaction commit. `processing` requires exactly one initial event and an order in `refunding`; `succeeded` requires one processing event, one succeeded event, the matching negative wallet ledger entry, and an order in `refunded`; `failed` requires one processing event, one failed event, and the order restored to `paid`. The original order's user, provider, and amount must continue to match the refund snapshot. Refund identity, terminal state, and every refund event are immutable; updates, deletes, and table truncation are rejected. Migration 59 imports legacy refunded orders only when their exact negative wallet ledger entry exists; an incomplete legacy refund stops the migration with an explicit error instead of synthesizing financial history.

## Subscription

active -> grace_period -> expired. A subscription with an `ends_at` timestamp enters a three-day grace period when that timestamp is reached. Entitlements and routes remain available during the grace period. After `grace_ends_at`, the Worker suspends applications, closes routes, cancels unfinished deployment jobs, and stops runtime containers. Assigning a plan returns the subscription to active, clears the previous grace deadline, and queues the last successful immutable Release of each subscription-suspended application through the normal deployment state machine.

User purchases and renewals are explicit actions; the platform never auto-renews or performs an unattended wallet debit. A first purchase charges the current full cycle price. During an active paid period, moving to a higher-priced plan charges only the positive price difference, while moving to an equal- or lower-priced plan produces no refund. Renewal charges the target plan's current full cycle price and extends the service period. Each successful transition atomically writes the subscription, immutable purchase transaction, wallet ledger entry, bill item, and notification; an insufficient balance leaves all of them unchanged.

`plans.purchase_enabled` controls self-service entry into a plan. A disabled plan rejects new self-service purchases, but a user already holding that plan may renew it. Administrative assignment is independent of this switch. Only a super administrator may change the switch, and every change is audited.

When a plan version contains `creditGrantCents` greater than zero, the platform grants that target allowance per user and UTC calendar month. The base business reference is `subscription-credit/{user_id}/{YYYY-MM}`. Assignment and Worker reconciliation call the same transaction-locked database function: retries add nothing, an upgrade grants only the positive difference, and a downgrade never claws back issued or consumed credit. Grant batches expire at the next UTC month boundary; active and unexpired grace-period subscriptions remain eligible.

## Billing suspension

running -> suspended (`billing_insufficient`) closes the route and stops the runtime container after the database transaction commits. Once a changed wallet balance allows all priced pending usage windows to be charged, the Worker queues the last successful immutable Release through the deployment state machine; it never marks a missing container as running directly. An `unpriced` window is already sealed and therefore never blocks recovery.

## Usage billing disposition

An aggregate starts as `pending` only when its usage event already contains a frozen price-version snapshot. Successful debit changes it to `charged` and seals it. If no price existed when the event was created, aggregation writes `unpriced` and seals the window immediately; later price changes never move it back to `pending`. `waived_legacy` is a terminal compatibility state for historical migration or rollback. No terminal disposition may be charged or reopened.

## Invariants

- Currency values crossing a domain boundary use integer cents. Metering intermediates use PostgreSQL NUMERIC.
- Wallet ledger rows, application Release 快照和应用 Secret 版本只允许追加；应用 Secret 的键名与应用归属同样不可变，数据库拒绝篡改、删除和整表清空。
- A successful refund has exactly one order snapshot, one matching negative wallet entry, and one processing-to-succeeded event timeline.
- Audit logs are append-only at the database boundary: update, delete, and truncate operations are rejected.
- External effects are keyed by a unique business reference or idempotency key.
- A pending usage aggregate always references the immutable price version selected when the usage event was created.
- An unpriced or legacy-waived aggregate is terminal and can never create a wallet debit or bill item.
- A route points only at a release that passed its health check.
- User and application public slugs use a lowercase DNS-label-compatible format and cannot change after creation; an application's owner, product, and stable internal service name are also immutable.
- The Router derives path, release alias, and port from protected identity and the immutable Release instead of trusting writable route target columns.
- A published product version has completed an isolated successful product-version test.
- 产品、不可变版本与版本测试证据只能通过产品生命周期软下架，数据库拒绝删除或清空这些历史记录。
- Backup and restore rows follow database-enforced transitions; their identity and terminal history cannot be rewritten, deleted, or truncated.
- A product dependency targets a published product, never targets its own product, and the published product graph remains acyclic.
- A required dependency is resolved only within the same user network through its immutable `serviceSlug`; declared internal service names bypass the egress proxy.
- An application Secret may be rotated only when a published version of its product declares the key; only the target Release's declared keys can enter the container environment.
