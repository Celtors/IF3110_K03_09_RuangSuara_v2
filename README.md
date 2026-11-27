# RuangSuara

**Database Management Platform — IF3110 Pengembangan Aplikasi Web (2026), K03-09**

RuangSuara is a web platform for managing database access, exploring schemas and data, and changing databases safely through reviewed, approved, and asynchronously executed migrations. The platform's own metadata (users, groups, policies, migration definitions, change requests, jobs, history) lives in **PostgreSQL**, and the managed target databases are **PostgreSQL** as well.

- **Milestone 1:** authentication, access control, database management, linear migrations.
- **Milestone 2 (this version):** SPA frontend, rollback/forward with immutable history, multi-group approval workflow, queue + worker with batch mode, structured logging and tracing, and the observability, k6 and CodeQL bonuses.

## Features

### Access Control
- Username/password authentication with backend-managed sessions (`HttpOnly` `ssid` cookie, CSRF token on mutating requests).
- Sessions expire after 30 minutes of inactivity or 8 hours from login. Login revokes older sessions for the account. Active sessions rotate every 15 minutes and on the next authenticated request after a privilege change. The previous token works for 30 seconds after periodic rotation. Privilege changes invalidate the previous token immediately. Logout and account deactivation revoke sessions server-side.
- The session cookie is `Secure`, `HttpOnly`, and `SameSite=Lax`. Use `http://localhost` for local browser development and HTTPS for deployment. Authenticated write requests require a same-origin `Origin` and the `X-CSRF-Token` obtained from `GET /session-manager/me`.
- User and group administration, plus group-based database permissions.
- Permission levels: **View Only**, **View + Edit**, and **Blocked** (internal policy effect).
- **Group leaders:** every group has an assignable leader who approves change requests for the databases the group can access.

### Database Management
- Register, list, edit, remove, and test managed PostgreSQL connections.
- Browse schemas, tables, columns, constraints, indexes, and rows.
- Stored target-database credentials are encrypted with a configured 32-byte key.
- Managed database IDs are UUIDs issued by the platform, not database names.

### Single-Page Application (SPA)
- The whole frontend (login, home, administration, migrations) is one React SPA served from a single `index.html`; the client router picks the screen from the URL, so refreshes and pasted links work.
- Loading and error states are shown while data is fetched, and job progress updates without a page reload.
- Responsive on desktop and mobile; wide tables and the SQL editor scroll inside their own container instead of widening the page.
- Legacy `.html` URLs (e.g. `/login.html`) still resolve and are rewritten to clean URLs.

### Migrations
- **Definitions:** each migration has **SQL up** and **SQL down**, or is marked **irreversible** with a reason. After an execution attempt, the definition is immutable. A fix requires a new migration.
- **Linear order:** migrations apply in order; **rollback** is only allowed on the last active migration (and only if reversible), and **forward** re-applies a rolled-back migration without running the next one automatically.
- **Rollback and forward are new executions** and use the original SQL from the executed revision.
- **Validation tools:** schema inspection, schema diff, query plan, and a read-only SQL console for both View Only and View + Edit users. Persistent changes go through approved migration jobs.
- **Migration history:** execution attempts, including failures, record the migration ID, execution ID, revision, user, time, status, and SQL snapshot. History is never overwritten, and a successful rollback references the execution it undid.
- **Hide rollback:** a checkbox hides cancelled execution pairs (the rolled-back execution and the rollback that undid it), matched by execution ID rather than migration name. Failed attempts stay visible, and hiding never deletes history or audit data.

Example (history view):

```text
Full history              Hide rollback checked
A         [E1]            A  [E1]
B         [E2]            B  [E4]
B rollback [E3]  (E2)     C  [E5]
B forward [E4]
C         [E5]
```

### Review and Approval Workflow
Every change, including rollback and forward, goes through a change request:

```text
Draft → Pending Review → Approved → Queued → Running → Succeeded / Failed
```

- A change request requires approval from the **leader of every group that has View or View + Edit access** to the target database (a single group only needs its own leader).
- A request stays pending while any required group lacks an eligible leader.
- Reviewers can approve, reject (a reason is required), or cancel. Requesters can cancel.
- Approvers have an **approval inbox** for requests waiting on them, and requests show their approvals and audit events.
- Set `MIGRATION_FORBID_SELF_APPROVAL=true` to stop requesters from approving their own requests.
- Direct execution (`POST .../migrations/:migrationId/execute`) is rejected; use a change request.

### Asynchronous Execution (Queue and Worker)
- Approved requests become jobs in a queue, and a Go worker runs them. The API returns immediately and the SPA tracks job status.
- **One migration per database at a time**; different databases can run in parallel (worker concurrency defaults to 2). Locking spans workers and keeps protecting a migration between batch commits.
- **Restart safety:** jobs and checkpoints are persisted. Orphaned jobs are reclaimed, retries are bounded with a delay, and duplicate messages do not cause a double commit.
- **Uncertain outcomes:** when a commit result is unknown, the platform checks execution evidence before retrying and blocks later migrations on that database until the result is resolved (`unknown` status, plus `POST /api/databases/:id/migrations/reconcile`).
- **Ordinary mode:** the worker runs the SQL as one transaction, still through the queue.
- **Per-batch mode:** for large data migrations. The worker processes rows in configurable batches, each batch in its own transaction, with the checkpoint (execution, batch, cursor) saved in the same transaction. If one batch fails, earlier batches stay committed. The UI shows committed batches/rows and a `partial` state until the job finishes.
  - Batch SQL is deliberately narrow: `UPDATE schema.table SET col = col ± N WHERE pk = ANY($1::bigint[])` with a batch attribute (the unique, unchanging key column) and a batch size from 1 to 10000.
  - **Resume** continues from the checkpoint without repeating committed batches (`POST /api/databases/:id/jobs/:jobId/resume`).
  - **Compensate** undoes committed batches in reverse order using the approved, immutable SQL down (`POST /api/databases/:id/jobs/:jobId/compensate`). Unsafe compensation is refused, and a partly reverted job is shown as `partially reverted` so it can be continued.
  - Resume and compensate revalidate the original requester's permissions and approval. If compensation is unsafe or unavailable and the partial checkpoint is verified, a user with Edit access can submit a new corrective migration. Its successful execution releases the reservation and preserves the original checkpoint and history. An uncertain commit still requires reconciliation.

### Activity Logging and Tracing
- Structured JSON logs through Go's `log/slog` to stdout/stderr (not stored in the platform database).
- Logged attributes include time, level, event, actor, database, status, and correlation ID. SQL events add redacted query structure, parameter positions and types where available, duration, row count, error/SQLSTATE, and migration/job/execution IDs with attempt/batch where relevant. Start, result, and commit/rollback are logged separately, covering read-only queries, batches, retries, and failures.
- OpenTelemetry tracing links API → queue → worker → lock → batch/query. The trace context travels through the queue, and `trace_id`/`span_id` appear in the related logs.
- Credentials, session identifiers, and other sensitive data are redacted.
- Without an OTLP endpoint, spans are written to the console exporter (stdout).

### Bonus Features
- **Observability:** OpenTelemetry Collector → Loki (logs) and Jaeger (traces), Prometheus metrics, and a provisioned Grafana dashboard. See [Observability](#observability).
- **Stress testing:** a k6 script that runs API reads and concurrent job submissions. See [Stress testing with k6](#stress-testing-with-k6).
- **SAST:** CodeQL on Go and JavaScript. See [Security / SAST](#security--sast).

## Tech stack

| Layer | Technology |
| --- | --- |
| Backend and worker | Go **1.26.1**, Gin, GORM, pgx |
| Frontend | React 19, React Router, Vite 8 (SPA) |
| Platform database | PostgreSQL 16 |
| Managed target databases | PostgreSQL |
| Observability | OpenTelemetry, Prometheus, Grafana, Loki, Jaeger |
| Load testing | k6 |
| SAST | CodeQL (GitHub Actions) |
| Local infrastructure | Docker Compose |
| Tests | Go `testing`, Vitest + Testing Library |

## Repository structure

```text
RuangSuara/
├── backend/src/
│   ├── main.go                  # HTTP server, queue worker, metrics endpoint
│   ├── access_control/          # Authentication, sessions, groups, policies, leaders
│   ├── database_management/     # Connections and schema/data viewer
│   ├── migration/               # Definitions, review/approval, queue worker, batch mode, history
│   └── internal/
│       ├── app/                 # Routes and SPA shell registration
│       └── telemetry/           # Logging, tracing, Prometheus metrics
├── frontend/
│   └── src/                     # React SPA (admin/, migration/, pages/, auth/, api/)
├── database_schema/
│   ├── access-control/          # Platform schema and development seeds
│   ├── migration-review/        # Change-request / job storage schema
│   ├── migrations/              # Platform upgrade scripts
│   └── target-databases/        # Sample target databases (univ-db, inventory-db, sales-db)
├── observability/               # Collector, Loki, Jaeger, Prometheus, Grafana provisioning + dashboard
├── performance/stress.js        # k6 stress test
├── .github/                     # CodeQL workflow and config
├── docker-compose.yaml          # Platform DB + sample target databases
└── docker-compose.observability.yaml
```

## Prerequisites

- **Go 1.26.1** (or the toolchain declared in `backend/src/go.mod`) and a working C compiler on `PATH` for the PostgreSQL parser's cgo build.
- **Node.js 24 LTS** with npm (tested with Node.js 24.11.1 and npm 11.6.2).
- **Docker / Docker Compose** for the databases and observability stack.
- **k6** (only for stress testing).

## Local development setup

### 1. Start the databases

```bash
docker compose -f docker-compose.yaml up -d
docker compose -f docker-compose.yaml ps
docker compose -f docker-compose.yaml exec -T access-control-db pg_isready -h 127.0.0.1 -U myuser -d access-control-db
```

| Service | Host port | Database | User / password (development only) |
| --- | --- | --- | --- |
| `access-control-db` (platform) | `9000` | `access-control-db` | `myuser` / `mypassword` |
| `target-academic-db` | `9001` | `ruangsuara_academic` | `academic_user` / `academic_password` |
| `target-inventory-db` | `9002` | `ruangsuara_inventory` | `inventory_user` / `inventory_password` |
| `target-sales-db` | `9003` | `ruangsuara_sales` | `sales_user` / `sales_password` |

Compose loads the SQL in the mounted `database_schema/access-control/` and target directories on first initialization. Wait until PostgreSQL is ready before starting the backend. On startup the backend creates or upgrades migration, review, and batch storage from embedded SQL and adds the current session and database registration fields. Preserve existing volumes and the encryption key. `database_schema/migrations/001_database_management.sql` is a manual upgrade for legacy schemas with missing connection metadata and old policy columns. Use the current Compose initialization and backend startup for a fresh checkout.

### 2. Build the frontend

Run these commands from the repository root.

```bash
cd frontend
npm ci
npm run build
cd ..
```

### 3. Configure and start the backend

Run these commands from the repository root in Bash. Generate the key once and reuse it for subsequent runs.

```bash
cd backend/src
export PLATFORM_DATABASE_DSN="host=localhost port=9000 user=myuser password=mypassword dbname=access-control-db sslmode=disable"
export DB_CREDENTIALS_KEY="$(openssl rand -base64 32)"
export ALLOW_PRIVATE_TARGETS=true
export HTTP_ADDR=127.0.0.1:8080
export CGO_ENABLED=1
go mod download
go run .
```

These commands start the same backend in PowerShell.

```powershell
Set-Location backend/src
$env:PLATFORM_DATABASE_DSN = "host=localhost port=9000 user=myuser password=mypassword dbname=access-control-db sslmode=disable"
$keyBytes = New-Object byte[] 32
$rng = [Security.Cryptography.RandomNumberGenerator]::Create()
$rng.GetBytes($keyBytes)
$rng.Dispose()
$env:DB_CREDENTIALS_KEY = [Convert]::ToBase64String($keyBytes)
$env:ALLOW_PRIVATE_TARGETS = "true"
$env:HTTP_ADDR = "127.0.0.1:8080"
$env:CGO_ENABLED = "1"
go mod download
go run .
```

| Variable | Purpose |
| --- | --- |
| `PLATFORM_DATABASE_DSN` / `PLATFORM_DATABASE_URL` | Platform PostgreSQL connection. The backend creates the migration/queue storage on startup. |
| `DB_CREDENTIALS_KEY` | Base64-encoded 32-byte key for encrypting managed-database passwords. **Persist it**: a new key makes stored credentials unreadable. |
| `ALLOW_PRIVATE_TARGETS` | `true` allows localhost/private targets (development only). |
| `TARGET_DB_ALLOWED_HOSTS` | Comma-separated allowlist required for target IP addresses and most hostnames. With `ALLOW_PRIVATE_TARGETS=true`, `localhost` and `.internal` hostnames can be used without an allowlist entry. |
| `HTTP_ADDR` | Listener address (default `:8080`). |
| `FRONTEND_DIST` | Frontend build directory (default `../../frontend/dist` from `backend/src`). |
| `MIGRATION_FORBID_SELF_APPROVAL` | `true` stops requesters approving their own change requests. |
| `METRICS_ADDR` | Enables the Prometheus `/metrics` endpoint on this address (e.g. `:9464`). |
| `OTEL_EXPORTER_OTLP_ENDPOINT` (or `..._TRACES_ENDPOINT` / `..._LOGS_ENDPOINT`) | Sends traces/logs to an OTLP/HTTP collector. Unset means console exporter. |

The backend starts the worker and serves `frontend/dist`. For frontend development, run `npm run dev` from `frontend/`; Vite proxies `/api`, `/session-manager`, and `/admin-control-panel` to `http://localhost:8080` (override with `VITE_BACKEND_URL`).

| Screen | URL |
| --- | --- |
| Login | http://localhost:8080/login |
| Home | http://localhost:8080/home |
| Users / Groups / Databases / Policies | http://localhost:8080/admin/users, `/admin/groups`, `/admin/databases`, `/admin/policies` |
| Migration workspace | http://localhost:8080/migrations |
| Database viewer | `/migrations?tab=viewer` |
| History / Editor / Details / Schema diff / Query plan / Change request | `/migrations/history`, `/migrations/editor`, `/migrations/details`, `/migrations/schema-diff`, `/migrations/query-plan`, `/migrations/change-request` |

### 4. Development login

Open `http://localhost:8080/login` and sign in as `admin` with `password123`. The same development password works for `alice`, `bob`, and `carol`. `dave` is inactive. These accounts and the Compose credentials are for local use only.

1. In **Admin → Databases**, edit `academic_db`, `inventory_db`, and `sales_db`. Keep their seeded endpoints and enter the corresponding target passwords from the table above. Choose **Save connection**, then **Test**. Saving encrypts the password with the configured key. The seeded `users_db` entry is a placeholder.
2. For a basic review demo, add `bob` to `data-engineers` and assign him as that group's leader in **Admin → Groups**. Sign in as `alice` to draft and submit a migration on `sales_db`, then as `bob` to approve it. Final approval queues the job automatically.

Managed database credentials must be able to read `pg_catalog.pg_control_system()` for physical identity checks. PostgreSQL roles created by the local Compose targets have this access. Give other deployments the function access and the schema/data privileges their approved work requires.

## Using the migration workflow

1. Log in and open **Migrations**, then choose an accessible database.
2. Inspect the schema and data, and try queries in the SQL console.
3. Create a migration with SQL up and SQL down (or mark it irreversible with a reason); validate with schema diff and query plan.
4. Submit a change request (apply, rollback, or forward), choosing ordinary or per-batch mode (batch attribute and size).
5. Group leaders review it from the **approval inbox**. Once every required group has approved, it is queued.
6. Watch job progress in the SPA (queued, running, partial, succeeded, failed). Resume or compensate batch jobs if needed.
7. Review **History**, with or without **Hide rollback**.

### Correcting an unsafe partial batch

1. Open the original job. When the server verifies a partial checkpoint and finds that compensation is unsafe or unavailable, a user with Edit access can choose **Create corrective migration**.
2. Save a new definition with the corrective SQL and submit the linked request. Submit it as an ordinary apply. The applicable group leaders approve it through the normal workflow.
3. The worker verifies the same physical target and checkpoint before running the correction. A successful correction marks the original job `corrected`, links the corrective request, retains the original counts and history, and releases the database reservation. If the correction fails, the reservation stays in place and a new correction can be reviewed once the outcome is verified.

## Selected API endpoints

Most endpoints require an authenticated session; mutating requests also need the CSRF token.

| Method | Path | Purpose |
| --- | --- | --- |
| `POST` | `/session-manager/login`, `/session-manager/logout` | Session |
| `GET` | `/session-manager/me` | Current user |
| `GET`/`POST`/`PATCH`/`DELETE` | `/admin-control-panel/users`, `/groups`, `/policy` | Administration |
| `GET`/`POST` | `/admin-control-panel/groups/leader` | Read / set a group leader |
| `GET`/`POST`/`PATCH`/`DELETE` | `/api/admin/databases[/:id]` | Manage connections |
| `POST` | `/api/admin/databases/:id/test` | Test connection |
| `GET` | `/api/accessible-databases` | Databases the caller can access |
| `GET` | `/api/databases/:id/schemas[/:schema/tables[/:table/(columns\|constraints\|indexes\|rows)]]` | Schema/data viewer |
| `GET` | `/api/databases/:id/migrations`, `/migrations/:migrationId`, `/migrations/:migrationId/attempts` | Definitions and attempts |
| `POST`/`PATCH` | `/api/databases/:id/migrations[/:migrationId]` | Create / edit a definition |
| `GET` | `/api/databases/:id/migration-history?hide_rollback=true\|false` | Execution history |
| `POST` | `/api/databases/:id/migrations/reconcile` | Resolve an uncertain outcome |
| `GET` | `/api/databases/:id/schema` | Inspect schema |
| `POST` | `/api/databases/:id/schema-diff`, `/query-plan`, `/query` | Diff, plan, SQL console |
| `POST` | `/api/databases/:id/change-requests` | Submit `{migration_id, operation}`. For a linked corrective apply include `corrects_batch_id`. |
| `GET` | `/api/databases/:id/change-requests[/:requestId]` | List / details with approvals and audit events |
| `POST` | `/api/databases/:id/change-requests/:requestId/approve`, `/reject`, `/cancel` | Review actions |
| `GET` | `/api/change-requests/pending-approval` | Requests waiting for the caller |
| `GET` | `/api/databases/:id/jobs/:jobId` | Job status and batch progress |
| `POST` | `/api/databases/:id/jobs/:jobId/resume`, `/compensate` | Batch resume / compensation |

## Automated testing

Backend, from `backend/src`:

```bash
go test ./... -count=1
```

Unit tests run without setup. Integration tests are skipped unless you point them at **disposable** PostgreSQL databases:

| Variable | Used by |
| --- | --- |
| `SESSION_TEST_DSN` | Session and admin integration test |
| `MIGRATION_TEST_DSN` | Fresh platform database without migration storage for the definition persistence test, paired with `SQL_CONSOLE_TEST_TARGET_DSN` |
| `ACTIVITY_TEST_PLATFORM_DSN`, `ACTIVITY_TEST_TARGET_DSN` | Activity logging through the worker |
| `SQL_CONSOLE_TEST_PLATFORM_DSN`, `SQL_CONSOLE_TEST_TARGET_DSN` | Separate disposable platform and target databases for SQL console, queue, and batch tests |
| `MIGRATION_RUN_PERFORMANCE=1` | Enables the large-dataset test, which uses both `SQL_CONSOLE_TEST_*_DSN` variables |

Frontend, from `frontend`:

```bash
npm run lint
npm test        # Vitest
npm run build
```

Tests cover successful flows, failures, and access denials for the relevant features.

### Large-dataset test data

Run this from `backend/src` with both `SQL_CONSOLE_TEST_*_DSN` variables set to disposable databases.

```bash
MIGRATION_RUN_PERFORMANCE=1 go test ./migration -run '^TestBatchPerformanceAcceptance$' -count=1 -v
```

| Recorded Slice 7A environment | Rows | Direct `UPDATE` | Queued ordinary apply | Queued batch apply |
| --- | ---: | ---: | ---: | ---: |
| Windows, local PostgreSQL 18.6, Go 1.26.2 | 1,000,000 | 6.70 s | 10.62 s | 40.41 s |

The queued durations include queueing and polling. The batch job committed 100 batches of 10,000 rows and reached cursor 1,000,000. The fixture verified equivalent final rows for ordinary and batch applies. The acceptance run exposed a terminal status ordering issue, which the later integration regression checks covered.

## Observability

On Docker Desktop, start the stack from the repository root and set the telemetry variables in the backend terminal before running `go run .`.

```bash
docker compose -f docker-compose.observability.yaml up -d

# in the backend terminal
export OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
export METRICS_ADDR=:9464
go run .
```

| Component | URL / role |
| --- | --- |
| OpenTelemetry Collector | `127.0.0.1:4318` (OTLP/HTTP) → Loki (logs), Jaeger (traces) |
| Jaeger | http://127.0.0.1:16686 |
| Prometheus | http://127.0.0.1:9090 (scrapes the backend at `host.docker.internal:9464`) |
| Grafana | http://127.0.0.1:3001 (anonymous viewer, with data sources and dashboard provisioned from `observability/grafana`) |

Prometheus metrics (prefixed `ruangsuara_`) cover queue and migration state (job counts by status, retries, queue and lock wait time, execution duration, batches and rows processed, worker slots) and database connection pools (active/idle connections and wait time). Labels are low-cardinality; user IDs, job IDs, and SQL are never used as labels. Logs carry `trace_id`/`span_id`, so a log line in Loki can be matched to its trace in Jaeger and to the metrics in Grafana.

Submit and approve a disposable migration. In Grafana Explore, select Loki and find `queue.submitted` or `queue.claim` by job ID. Open its trace ID in Jaeger, then check job counts, queue wait, and worker slots on the provisioned Grafana dashboard for the same time window. Prometheus must be able to reach the host's port 9464 at `host.docker.internal`.

This excerpt pairs a JSON log with its console-exported span from the local stress run.

```json
{"event":"queue.claim","status":"running","job_id":"b506533d84411566b7d8f5286be668b0","trace_id":"7b1a9ec8e6dc0ed7e9be62bc18908804","span_id":"4a5007577ef697ff"}
{"Name":"queue.claim","SpanContext":{"TraceID":"7b1a9ec8e6dc0ed7e9be62bc18908804","SpanID":"4a5007577ef697ff"}}
```

That trace also included `queue.publish`, `worker.process`, and `migration.execute`. The job waited 26.29 s before claim and its recorded attempt took 0.581 s. A local `/metrics` scrape exposed `ruangsuara_worker_slots_max 2` and pool gauges. The console correlation and direct metrics scrape were verified locally. The Compose configuration validated, and the Collector-to-Grafana path still needs a live Docker run.

## Stress testing with k6

`performance/stress.js` signs in as disposable accounts, then ramps API read load while submitting approved migration jobs to the same and to different databases, and polls each job until it finishes. Job-submission responses are not counted as successful execution; only finished jobs are.

Use the three local targets set up above. Give `data-engineers` **View + Edit** on all three. Add `bob` to `data-engineers` and `auditors` and make him leader of `data-engineers`, `analysts`, and `auditors`. The seeded membership already places Bob in `analysts`. With the seeded policies, `alice` can submit on all three, `carol` can read `academic_db`, and `bob` can approve every required group. Jobs use temporary scratch tables and leave migration definitions and history in the disposable platform database.

```bash
PASSWORD=password123 PROFILE=demo BASE_URL=http://127.0.0.1:8080 k6 run performance/stress.js
```

Use `PROFILE=verify` for a quick check or `PROFILE=stress` to reproduce the five-minute run below.

| Variable | Meaning |
| --- | --- |
| `PROFILE` | `demo` (default), `stress` (longer, heavier waves), or `verify` (quick check) |
| `BASE_URL` | Backend URL (default `http://127.0.0.1:8080`) |
| `PASSWORD` or `<ROLE>_PASSWORD` | Password for the requester, reader, and reviewer accounts |
| `REQUESTER_USER`, `READER_USER`, `REVIEWER_USER` | Account names (default `alice`, `carol`, `bob`) |
| `DATABASES` | Comma-separated target database names (default `sales_db,academic_db,inventory_db`) |

Thresholds: workload `http_req_failed` rate < 1%, API `p(95)` < 1000 ms, job failure rate < 1%, job completion `p(95)` < 35 s, and every submitted job must finish.

### Results

Recorded `stress` profile on Windows with PostgreSQL 18.6, Go 1.26.2, k6 2.4.0, two worker slots, and up to three VUs. The backend was built from `1f2d08f` and the run used the pre-submission workload. Attempt percentiles are from durable job records, grouped by completion phase.

| Phase | API p50 / p95 | Requests/s | HTTP error rate | Queue p95 | Attempt p50 / p95 | Completed jobs / jobs per second |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Baseline (60 s) | 3.2 / 46.2 ms | 5.22 | 0% | 5.33 s | 0.756 / 1.080 s | 12 / 0.20 |
| Ramp (60 s) | 3.2 / 40.8 ms | 12.37 | 0% | 11.21 s | 0.669 / 0.892 s | 22 / 0.37 |
| Peak (120 s) | 2.7 / 35.0 ms | 52.95 | 0% | 23.53 s | 0.588 / 0.637 s | 56 / 0.47 |
| Recovery (60 s) | 2.8 / 36.4 ms | 9.53 | 0% | 6.35 s | 0.577 / 0.622 s | 12 / 0.20 |

All thresholds passed. The run recorded 7,981 workload requests, 102 of 102 successful jobs, API p95 of 36.03 ms, and approval-to-completion p95 of 22.11 s. Queue wait rose as job submissions increased and fell after the peak. Same-target queue p95 was 23.73 s versus 12.24 s for the two-target cohort. The attempt records showed no overlapping executions on one target and 25 overlapping pairs across targets. The workload did not measure saturation. A later correction pass ran `verify` and finished 7 of 7 jobs with 0% workload HTTP failures, 130.03 ms API p95, and 6.74 s job completion p95.

## Security / SAST

CodeQL scans Go (`backend/src`) and JavaScript/JSX with the `security-extended` query suite. The workflow (`.github/workflows/codeql.yml`) runs on pushes and pull requests to `main` and `feat/**`, weekly, and on demand (**Actions > CodeQL SAST > Run workflow**). View alerts under **Security > Code scanning**; SARIF artifacts are attached to each workflow run. Private repositories need GitHub Code Security access.

The team report contains the initial scan, archived evidence, and triage. It records the remediation and rescan disposition as pending. [CodeQL report](docs/sast-codeql.md)
