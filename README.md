# RuangSuara

**Database Management Platform — IF3110 Pengembangan Aplikasi Web (2026), K03-09**

RuangSuara is a web platform for managing database access, exploring schemas and data, and changing databases safely through reviewed, approved, and asynchronously executed migrations. The platform's own metadata (users, groups, policies, migration definitions, change requests, jobs, history) lives in **PostgreSQL**, and the managed target databases are **PostgreSQL** as well.

- **Milestone 1:** authentication, access control, database management, linear migrations.
- **Milestone 2 (this version):** SPA frontend, rollback/forward with immutable history, multi-group approval workflow, queue + worker with batch mode, structured logging and tracing, and the observability, k6 and CodeQL bonuses.

## Features

### Access Control
- Username/password authentication with backend-managed sessions (`HttpOnly` `ssid` cookie, CSRF token on mutating requests).
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
- **Definitions:** each migration has **SQL up** and **SQL down**, or is marked **irreversible** with a reason. Definitions of migrations that have already been executed are immutable; fixes require a new migration.
- **Linear order:** migrations apply in order; **rollback** is only allowed on the last active migration (and only if reversible), and **forward** re-applies a rolled-back migration without running the next one automatically.
- **Rollback and forward are new executions** and use the original SQL from the executed revision.
- **Validation tools:** schema inspection, schema diff, query plan, and a SQL console on the target database.
- **Migration history:** every committed execution is stored with migration ID, execution ID, revision, user, time, status, and a copy of the SQL that actually ran. History is never overwritten, and a successful rollback references the execution it undid.
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
  - Resume and compensate follow the same permission, validation, and approval rules.

### Activity Logging and Tracing
- Structured JSON logs through Go's `log/slog` to stdout/stderr (not stored in the platform database).
- Logged attributes include time, level, event, actor, database, status, and correlation ID. SQL events add the query, parameters, duration, row count, error/SQLSTATE, and migration/job/execution IDs with attempt/batch where relevant. Start, result, and commit/rollback are logged separately, covering read-only queries, batches, retries, and failures.
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

- **Go 1.26.1** (or the toolchain declared in `backend/src/go.mod`).
- **Node.js and npm** compatible with Vite 8.
- **Docker / Docker Compose** for the databases and observability stack.
- **k6** (only for stress testing).

## Local development setup

### 1. Start the databases

Create a local `.env` if your Compose setup needs one (`.env` is git-ignored; never commit secrets).

```bash
docker compose -f docker-compose.yaml up -d
docker compose -f docker-compose.yaml ps
```

| Service | Host port | Database | User / password (development only) |
| --- | --- | --- | --- |
| `access-control-db` (platform) | `9000` | `access-control-db` | `myuser` / `mypassword` |
| `target-academic-db` | `9001` | `ruangsuara_academic` | `academic_user` / `academic_password` |
| `target-inventory-db` | `9002` | `ruangsuara_inventory` | `inventory_user` / `inventory_password` |
| `target-sales-db` | `9003` | `ruangsuara_sales` | `sales_user` / `sales_password` |

SQL files under `database_schema/` are loaded as initialization scripts when a **fresh** volume is first created. Existing volumes are not reinitialized; remove the volume (`docker compose down -v`) to start over.

### 2. Configure and start the backend

```bash
cd backend/src
export PLATFORM_DATABASE_DSN="host=localhost port=9000 user=myuser password=mypassword dbname=access-control-db sslmode=disable"
export DB_CREDENTIALS_KEY="$(openssl rand -base64 32)"
export ALLOW_PRIVATE_TARGETS=true
go mod download
go run .
```

PowerShell equivalent:

```powershell
$env:PLATFORM_DATABASE_DSN = "host=localhost port=9000 user=myuser password=mypassword dbname=access-control-db sslmode=disable"
$env:DB_CREDENTIALS_KEY = [Convert]::ToBase64String([Security.Cryptography.RandomNumberGenerator]::GetBytes(32))
$env:ALLOW_PRIVATE_TARGETS = "true"
```

| Variable | Purpose |
| --- | --- |
| `PLATFORM_DATABASE_DSN` / `PLATFORM_DATABASE_URL` | Platform PostgreSQL connection. The backend creates the migration/queue storage on startup. |
| `DB_CREDENTIALS_KEY` | Base64-encoded 32-byte key for encrypting managed-database passwords. **Persist it**: a new key makes stored credentials unreadable. |
| `ALLOW_PRIVATE_TARGETS` | `true` allows localhost/private targets (development only). |
| `HTTP_ADDR` | Listener address (default `:8080`). |
| `FRONTEND_DIST` | Frontend build directory (default `../../frontend/dist` from `backend/src`). |
| `MIGRATION_FORBID_SELF_APPROVAL` | `true` stops requesters approving their own change requests. |
| `METRICS_ADDR` | Enables the Prometheus `/metrics` endpoint on this address (e.g. `:9464`). |
| `OTEL_EXPORTER_OTLP_ENDPOINT` (or `..._TRACES_ENDPOINT` / `..._LOGS_ENDPOINT`) | Sends traces/logs to an OTLP/HTTP collector. Unset means console exporter. |

The queue worker starts with the backend process.

### 3. Build the frontend

```bash
cd frontend
npm ci
npm run build
```

The backend serves `frontend/dist`. For frontend development, run `npm run dev`; Vite proxies `/api`, `/session-manager`, and `/admin-control-panel` to `http://localhost:8080` (override with `VITE_BACKEND_URL`).

| Screen | URL |
| --- | --- |
| Login | http://localhost:8080/login |
| Home | http://localhost:8080/home |
| Users / Groups / Databases / Policies | http://localhost:8080/admin/users, `/admin/groups`, `/admin/databases`, `/admin/policies` |
| Migration workspace | http://localhost:8080/migrations |
| History / Editor / Details / Schema diff / Query plan / Change request | `/migrations/history`, `/editor`, `/details`, `/schema-diff`, `/query-plan`, `/change-request` |

### 4. Development login

`database_schema/access-control/access-control-db.sql` seeds development accounts (`admin`, `alice`, `bob`, `carol`, and an inactive `dave`), groups (`platform-admins`, `data-engineers`, `analysts`, `auditors`, `interns`), policies, and the sample databases `academic_db`, `sales_db`, and `inventory_db` (ports `9001`–`9003`). The documented sample password is `password123`. **These seeds are for local use only.** Remove them and change credentials before using non-local infrastructure. The seeded `users_db` entry has placeholder connection details and is not usable.

## Using the migration workflow

1. Log in and open **Migrations**, then choose an accessible database.
2. Inspect the schema and data, and try queries in the SQL console.
3. Create a migration with SQL up and SQL down (or mark it irreversible with a reason); validate with schema diff and query plan.
4. Submit a change request (apply, rollback, or forward), choosing ordinary or per-batch mode (batch attribute and size).
5. Group leaders review it from the **approval inbox**. Once every required group has approved, it is queued.
6. Watch job progress in the SPA (queued, running, partial, succeeded, failed). Resume or compensate batch jobs if needed.
7. Review **History**, with or without **Hide rollback**.

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
| `POST` | `/api/databases/:id/change-requests` | Submit `{migration_id, operation}` |
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
| `MIGRATION_TEST_DSN` | Migration definition persistence test (must be a database without migration storage) |
| `ACTIVITY_TEST_PLATFORM_DSN`, `ACTIVITY_TEST_TARGET_DSN` | Activity logging through the worker |
| `SQL_CONSOLE_TEST_PLATFORM_DSN`, `SQL_CONSOLE_TEST_TARGET_DSN` | SQL console tests |
| `MIGRATION_RUN_PERFORMANCE=1` | Large-dataset batch performance acceptance test |

Frontend, from `frontend`:

```bash
npm run lint
npm test        # Vitest
npm run build
```

Tests cover successful flows, failures, and access denials for the relevant features.

### Large-dataset test data

The batch performance test (`MIGRATION_RUN_PERFORMANCE=1`) uses a table large enough that one unbatched `UPDATE` over every row takes more than one second, then runs the same dataset through per-batch mode. Record the row count and durations from your own environment here:

| Environment | Rows | Unbatched `UPDATE` | Per-batch (size) |
| --- | --- | --- | --- |
| _fill in_ | _fill in_ | _fill in_ | _fill in_ |

## Observability

Start the stack and run the backend with telemetry enabled:

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
| Grafana | http://127.0.0.1:3001 (anonymous viewer; datasources and dashboard provisioned from `observability/grafana`) |

Prometheus metrics (prefixed `ruangsuara_`) cover queue and migration state (job counts by status, retries, queue and lock wait time, execution duration, batches and rows processed, worker slots) and database connection pools (active/idle connections and wait time). Labels are low-cardinality; user IDs, job IDs, and SQL are never used as labels. Logs carry `trace_id`/`span_id`, so a log line in Loki can be matched to its trace in Jaeger and to the metrics in Grafana.

## Stress testing with k6

`performance/stress.js` signs in as disposable accounts, then ramps API read load while submitting approved migration jobs to the same and to different databases, and polls each job until it finishes. Job-submission responses are not counted as successful execution; only finished jobs are.

```bash
PASSWORD=password123 PROFILE=demo BASE_URL=http://127.0.0.1:8080 k6 run performance/stress.js
```

| Variable | Meaning |
| --- | --- |
| `PROFILE` | `demo` (default), `stress` (longer, heavier waves), or `verify` (quick check) |
| `BASE_URL` | Backend URL (default `http://127.0.0.1:8080`) |
| `PASSWORD` or `<ROLE>_PASSWORD` | Password for the requester, reader, and reviewer accounts |
| `REQUESTER_USER`, `READER_USER`, `REVIEWER_USER` | Account names (default `alice`, `carol`, `bob`) |
| `DATABASES` | Comma-separated target database names (default `sales_db,academic_db,inventory_db`) |

Thresholds: workload `http_req_failed` rate < 1%, API `p(95)` < 1000 ms, job failure rate < 1%, job completion `p(95)` < 35 s, and every submitted job must finish.

### Results

_Add the p50/p95 API response time, throughput, error rate, queue wait, and job duration/throughput for each load level, with a short analysis, after running the `stress` profile._

## Security / SAST

CodeQL scans Go (`backend/src`) and JavaScript/JSX with the `security-extended` query suite. The workflow (`.github/workflows/codeql.yml`) runs on pushes and pull requests to `main` and `feat/**`, weekly, and on demand (**Actions > CodeQL SAST > Run workflow**). View alerts under **Security > Code scanning**; SARIF artifacts are attached to each workflow run. Private repositories need GitHub Code Security access.

Document each finding (rule, severity, location, impact, and the fix or the reason it was not fixed), and reference the fix commit and re-scan result for fixed findings. A clean scan is not proof the application is secure.