# RuangSuara

**IF3110 Pengembangan Aplikasi Web — Milestone 1**

RuangSuara is a web-based database management platform built with Go, React, and PostgreSQL.

## Features

- **Access Control:** User management, groups, authentication, sessions, and database permissions.
- **Database Management:** PostgreSQL and MariaDB connection management, schema inspection, and data viewing APIs.
- **Migration:** SQL migration drafts, transactional execution, rollback, and version history.
- **Bonus:** PostgreSQL schema diff, SQL generation, and query execution plans using `EXPLAIN`.

Migration currently supports PostgreSQL targets only.

## Prerequisites

- Docker Desktop
- Go 1.26.1
- Node.js and npm

## Quick Start

After completing the first-time setup below, run the application using three terminals.

**1. PostgreSQL — repository root**

```powershell
docker compose up -d
```

**2. Backend — terminal 1**

```powershell
cd backend/src
go run .
```

**3. Frontend — terminal 2**

```powershell
cd frontend
npm run dev
```

Open **http://localhost:5173/login.html**.

Development account:

| Username | Password |
|---|---|
| admin | password123 |

The backend runs on port `8080`, the frontend on port `5173`, and PostgreSQL on port `9000`.

## First-Time Setup

### 1. Initialize PostgreSQL

The Docker Compose configuration creates the `access-control-db` database. On a fresh Docker volume, the SQL files in `database_schema/access-control/` initialize the platform schema and development accounts.

Check that Docker is running:

```powershell
docker compose up -d
docker compose ps
```

Apply the additional DBMS column used by Database Management:

```powershell
Get-Content -Raw .\database_schema\migrations\002_add_dbms.sql |
  docker exec -i access-control-db psql -X -v ON_ERROR_STOP=1 -U myuser -d access-control-db -f -
```

Apply this schema update once per database. Existing Docker volumes are not automatically reinitialized.

**Note:** If the repository's `docker-compose.yaml` still contains an invalid top-level `include` entry with only `env_file`, remove that entry before starting Docker. Keep the existing `services` and `volumes` definitions.

### 2. Configure the backend

In PowerShell, set the platform database connection:

```powershell
$env:PLATFORM_DATABASE_DSN = 'host=localhost port=9000 user=myuser password=mypassword dbname=access-control-db sslmode=disable'
$env:ALLOW_PRIVATE_TARGETS = 'true'
$env:TARGET_DB_ALLOWED_HOSTS = 'localhost,127.0.0.1'
$env:CGO_ENABLED = '0'
```

Database Management also requires `DB_CREDENTIALS_KEY` to encrypt saved database passwords.

Generate a local key once:

```powershell
$keyFile = Join-Path $HOME '.ruangsuara-key.txt'

if (!(Test-Path $keyFile)) {
  $bytes = New-Object byte[] 32
  $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
  $rng.GetBytes($bytes)
  $rng.Dispose()
  [IO.File]::WriteAllText($keyFile, [Convert]::ToBase64String($bytes))
}

$env:DB_CREDENTIALS_KEY = (Get-Content $keyFile -Raw).Trim()
```

Reuse the same key and configuration whenever starting the backend in a new terminal.

Run `go run .` from `backend/src`.

### 3. Install frontend dependencies

From `frontend`:

```powershell
npm ci
npm run dev
```

Use `localhost` consistently when accessing the application because the login cookie is configured for that host.

## Using the Application

### Database Connections

Open **Administration → Database connections** to register PostgreSQL or MariaDB databases.

For a disposable PostgreSQL demo database:

```powershell
docker exec access-control-db createdb -U myuser ruangsuara_demo
```

Register it using:

| Field | Value |
|---|---|
| DBMS | PostgreSQL |
| Host | localhost |
| Port | 9000 |
| Database | ruangsuara_demo |
| Username | myuser |
| Password | mypassword |
| SSL mode | disable |

Test the connection and grant the appropriate group `ViewEdit` permission through **Database access**.

The platform database stores application information, while registered target databases contain the data being managed. Avoid executing demonstration migrations on the platform database.

### Migration

Open **Migrations** and select a registered PostgreSQL database.

Example successful migration:

```sql
CREATE TABLE demo_verify (
    id INTEGER PRIMARY KEY,
    title TEXT
);
```

Save and execute the draft. The successful migration should appear in the version history.

Example failed migration:

```sql
ALTER TABLE demo_verify ADD COLUMN note TEXT;
ALTER TABLE demo_verify ADD COLUMN id INTEGER;
```

The second statement fails because `id` already exists. The transaction should roll back both changes.

### Bonus Features

**Schema Diff:** Compare the current schema with a desired schema and generate SQL for supported changes.

**Query Execution Plan:** Use `EXPLAIN` to inspect PostgreSQL's estimated query plan without executing the query.

## Testing

Backend:

```powershell
cd backend/src
$env:CGO_ENABLED = '0'
go test ./...
```

Frontend:

```powershell
cd frontend
npm ci
npm run build
npm run lint
```

Some integration tests require additional disposable test database configuration.

## Troubleshooting

| Problem | Check |
|---|---|
| Backend fails to start | PostgreSQL connection and platform schema |
| Login fails | Seeded users and `PLATFORM_DATABASE_DSN` |
| Database connection fails | Host, port, credentials, SSL mode |
| Cannot save database credentials | `DB_CREDENTIALS_KEY` |
| Access denied | Selected database and group permissions |
| Frontend cannot connect | Backend running on port 8080 |

Do not delete Docker volumes containing important data. Development credentials and permissive local connection settings must not be used in production.