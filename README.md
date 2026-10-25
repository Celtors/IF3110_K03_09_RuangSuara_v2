# RuangSuara

**Database Management Platform — IF3110 Pengembangan Aplikasi Web (2026), K03-09**

RuangSuara is a web application for managing database access, registering database connections, exploring schemas and data, and tracking SQL migrations. The platform's internal metadata and access-control storage run on **PostgreSQL**. As managed target databases, **PostgreSQL and MariaDB** are supported by Database Management; **migration execution currently targets PostgreSQL only**.

> **Project status:** The source includes unit tests, but a full local build and integration test have not been verified in the provided environment. Use this README for setup and validate functionality before deployment.

## Features

### Access Control
- Username/password authentication and backend-managed sessions.
- User and group administration, including group-based database permissions.
- Database permission levels: **View Only**, **View + Edit**, and **Blocked** (internal policy effect).
- Protected platform administration and database APIs.

### Database Management
- Register, list, edit, remove, and test managed database connections.
- Target engines: **PostgreSQL** (`postgres`) and **MariaDB** (`mariadb`).
- Browse schemas, tables, columns, constraints, indexes, and table rows.
- Encrypt stored target-database credentials using a configured 32-byte key.
- Managed database IDs are UUIDs issued by the platform, not database names.

### Migration
- Draft, review, and execute migrations against supported PostgreSQL target connections.
- Browse migration history, details, and execution attempts.
- PostgreSQL schema inspection, schema diff, and query-plan-related interfaces.
- Authorization checks for viewing and editing migrations.

> **Limitation:** MariaDB support currently covers Database Management and the database viewer, **not** the Migration execution workflow. Some migration pages and advanced analysis features may need further end-to-end verification.

## Tech stack

| Layer | Technology |
| --- | --- |
| Backend | Go **1.26.1**, Gin, GORM, pgx |
| Frontend | React 19, Vite 8, JavaScript, HTML/CSS |
| Platform database | PostgreSQL 16 |
| Managed target databases | PostgreSQL, MariaDB |
| Local infrastructure | Docker Compose |
| Tests | Go `testing` package |

## Repository structure

```text
RuangSuara/
├── backend/src/
│   ├── main.go                  # Shared Go HTTP server
│   ├── access_control/          # Authentication, sessions, groups, policies
│   ├── database_management/     # DBMS connections and schema/data viewer
│   ├── migration/               # Migration service and APIs
│   └── internal/app/            # Route and static-page registration
├── frontend/
│   ├── admin/                   # Administration pages
│   ├── migrations/              # Migration pages
│   └── package.json
├── database_schema/
│   ├── access-control/          # Platform schema and development seeds
│   └── migrations/              # Platform database upgrade scripts
├── docker-compose.yaml         # PostgreSQL platform database
├── docker-compose.test.yaml    # PostgreSQL + MariaDB for local testing
├── run-tests.ps1               # Module unit-test runner (PowerShell)
└── TESTING.md                  # Automated-testing scope and limitations
```

## Prerequisites

- **Go 1.26.1** or the compatible toolchain declared by `backend/src/go.mod`.
- **Node.js and npm** capable of running the Vite version in `frontend/package.json`.
- **Docker Desktop / Docker Compose** (recommended for local PostgreSQL and MariaDB).
- **PowerShell** for the provided test runner. Commands below assume PowerShell on Windows.

## Local development setup

### 1. Start the platform database

From the project root, create a local `.env` file if needed by the included Compose configuration. The committed Compose files reference `./.env`; do **not** commit secrets to version control.

```powershell
docker compose -f docker-compose.yaml up -d
docker compose -f docker-compose.yaml ps
```

The default development Compose file exposes the **platform PostgreSQL** instance on `localhost:9000`, with database `access-control-db`, user `myuser`, and password `mypassword`. These are **development-only** values.

The SQL under `database_schema/access-control/` is mounted as a PostgreSQL initialization script and runs when a **fresh** volume is first created. Existing volumes are not automatically reinitialized.

### 2. Apply platform database upgrades

For an existing installation, **back up the database** and review scripts before applying them. On a fresh database initialized with the bundled base schema, the `001` migration may overlap with existing definitions; inspect it before applying rather than running all upgrades blindly.

To add the DBMS discriminator to the platform's `databases` table (after its base schema exists):

```powershell
Get-Content .\database_schema\migrations\002_add_dbms.sql |
  docker compose -f docker-compose.yaml exec -T access-control-db `
    psql -v ON_ERROR_STOP=1 -U myuser -d access-control-db
```

Existing records default to `postgres`. Confirm the column:

```powershell
docker compose -f docker-compose.yaml exec access-control-db `
  psql -U myuser -d access-control-db -c "select id, name, dbms from databases;"
```

### 3. Configure the backend

From the project root, open a PowerShell terminal and enter the backend directory:

```powershell
cd backend/src
```

Set these environment variables in the **same terminal**:

```powershell
$env:PLATFORM_DATABASE_DSN = "host=localhost port=9000 user=myuser password=mypassword dbname=access-control-db sslmode=disable"
$env:DB_CREDENTIALS_KEY = [Convert]::ToBase64String(
  [Security.Cryptography.RandomNumberGenerator]::GetBytes(32)
)
$env:ALLOW_PRIVATE_TARGETS = "true"
```

- `PLATFORM_DATABASE_DSN` (or `PLATFORM_DATABASE_URL`) is the internal PostgreSQL connection string.
- `DB_CREDENTIALS_KEY` is a **base64-encoded 32-byte key** for encrypting managed database passwords. **Persist this key securely**; generating a different key on each restart makes previously stored credentials unreadable.
- `ALLOW_PRIVATE_TARGETS=true` allows localhost/private target addresses in local development. **Do not enable unrestricted private-target access in production.**
- `HTTP_ADDR` optionally sets the listener (default `:8080`).
- `FRONTEND_DIST` optionally overrides the frontend asset directory (default `../../frontend/dist` when invoked from `backend/src`).

Install dependencies and start the backend:

```powershell
go mod download
go run .
```

### 4. Build the frontend

Open **another** terminal at the project root:

```powershell
cd frontend
npm ci
npm run build
```

If the Go backend was already running, refresh the browser after the build completes. The backend serves the compiled frontend from `frontend/dist` by default.

**Application URLs:**

| Page | URL |
| --- | --- |
| Login | http://localhost:8080/login.html |
| Home | http://localhost:8080/home.html |
| Database connections | http://localhost:8080/admin/databases.html |
| Users | http://localhost:8080/admin/users.html |
| Groups | http://localhost:8080/admin/groups.html |
| Policies | http://localhost:8080/admin/policies.html |
| Migrations | http://localhost:8080/migrations/ |

### 5. Development login

The bundled `database_schema/access-control/access-control-db.sql` contains **development seed accounts**, including `admin`, with a documented sample password of `password123`. These seeds are **not suitable for production**. Disable/remove test accounts and change credentials before using nonlocal infrastructure.

The seeded `databases` records also use **placeholder** connection details and nonfunctional password ciphertext. Register or update a real connection through Database Management before using Test Connection or the viewer.

## Test PostgreSQL and MariaDB together

The included `docker-compose.test.yaml` provides disposable local database engines:

```powershell
# From project root; avoid running this alongside docker-compose.yaml
# if both try to bind localhost:9000.
docker compose -f docker-compose.test.yaml up -d
docker compose -f docker-compose.test.yaml ps
```

Managed target settings for the test services:

| Setting | PostgreSQL | MariaDB |
| --- | --- | --- |
| Host (backend running on host) | `localhost` | `localhost` |
| Port | `9000` | `3306` |
| Database | `access-control-db` | `test_mariadb` |
| Username | `myuser` | `testuser` |
| Password | `mypassword` | `testpassword` |
| DBMS selector | `PostgreSQL` | `MariaDB` |

Create sample MariaDB data:

```powershell
docker compose -f docker-compose.test.yaml exec mariadb `
  mariadb -u testuser -ptestpassword test_mariadb
```

```sql
create table mahasiswa (
  nim varchar(10) primary key,
  nama varchar(100) not null,
  angkatan int
);
create index idx_angkatan on mahasiswa(angkatan);
insert into mahasiswa values
  ('13524001', 'Andi', 2024),
  ('13524002', 'Budi', 2024),
  ('13523003', 'Citra', 2023);
```

Then open `/admin/databases.html`, register a connection for each engine, use **Test Connection**, and inspect the schema/data viewer with an account granted access through a group policy.

**Docker networking note:** `localhost` refers to the *backend machine/process*. If you run the backend in a separate container, use reachable Docker service names or suitable host networking instead.

## Finding a managed database ID

A managed database ID is the UUID in the platform's `databases` table. To inspect it:

```powershell
docker compose -f docker-compose.yaml exec access-control-db `
  psql -U myuser -d access-control-db `
  -c "select id, name, dbms, database_name from databases;"
```

Use the returned `id` in the Migration page or APIs. **Do not** enter the database name, port, or username in the Managed Database ID input. Migration execution currently applies to PostgreSQL targets only.

## Selected API endpoints

Most of these endpoints require an authenticated session and appropriate backend authorization.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/admin/databases` | List managed connections (admin) |
| `POST` | `/api/admin/databases` | Add managed connection (admin) |
| `PATCH` | `/api/admin/databases/:id` | Update connection (admin) |
| `DELETE` | `/api/admin/databases/:id` | Delete connection (admin) |
| `POST` | `/api/admin/databases/:id/test` | Test connection (admin) |
| `GET` | `/api/databases/:id/schemas` | List schemas |
| `GET` | `/api/databases/:id/schemas/:schema/tables` | List tables |
| `GET` | `/api/databases/:id/schemas/:schema/tables/:table/columns` | Inspect columns |
| `GET` | `/api/databases/:id/schemas/:schema/tables/:table/constraints` | Inspect constraints |
| `GET` | `/api/databases/:id/schemas/:schema/tables/:table/indexes` | Inspect indexes |
| `GET` | `/api/databases/:id/schemas/:schema/tables/:table/rows` | Read rows |
| `GET` | `/api/databases/:id/migrations` | Migration history |
| `POST` | `/api/databases/:id/migrations` | Create migration draft |
| `POST` | `/api/databases/:id/migrations/:migrationId/execute` | Execute migration (PostgreSQL) |

Use the target database's schema name (`public` is common in PostgreSQL; the database name serves as the schema namespace in MariaDB).

## Automated testing

From `backend/src` run:

```powershell
go test ./access_control/... -count=1 -v
go test ./database_management/... -count=1 -v
go test ./migration/... -count=1 -v
```

To generate coverage:

```powershell
go test ./access_control/... ./database_management/... ./migration/... -coverprofile=coverage.out
go tool cover -func=coverage.out
```

Frontend lint/build checks:

```powershell
cd frontend
npm run lint
npm run build
```

The added backend tests are **unit tests** covering access decisions, DBMS configuration/validation, and migration SQL/schema validation. **They do not yet prove live connection behavior, HTTP session flows, migration transactions, rollback, or migration-history persistence.** See [TESTING.md](TESTING.md) for details. Local automated-test execution was not confirmed in the preparation environment due to the missing required Go toolchain.