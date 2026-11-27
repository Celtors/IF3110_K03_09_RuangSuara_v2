package databasemanagement

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"sync"
	"time"

	"backend/internal/targetdb"
	"backend/internal/telemetry"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"gorm.io/gorm"
)

type Provider struct {
	platform *gorm.DB
	mu       sync.Mutex
	pools    map[poolKey]*pgxpool.Pool
	closed   bool
}

type poolKey struct {
	id, dsn, fingerprint string
}

var ErrDatabaseNotFound = errors.New("managed database not found")
var errTargetUnavailable = errors.New("could not verify the PostgreSQL target; check its connection settings, credentials, TLS and identity inspection permission")

func NewProvider(platform *gorm.DB) *Provider {
	return &Provider{platform: platform, pools: make(map[poolKey]*pgxpool.Pool)}
}

func postgresDSN(d Database, password string) string {
	u := &url.URL{Scheme: "postgres", Host: net.JoinHostPort(d.Host, strconv.Itoa(d.Port)), Path: "/" + d.DatabaseName, User: url.UserPassword(d.Username, password)}
	q := u.Query()
	q.Set("sslmode", d.SSLMode)
	u.RawQuery = q.Encode()
	return u.String()
}

func platformFingerprint(ctx context.Context, db *gorm.DB) (string, error) {
	var fingerprint string
	err := db.WithContext(ctx).Raw(targetdb.FingerprintSQL).Row().Scan(&fingerprint)
	return fingerprint, err
}

func (p *Provider) probe(ctx context.Context, platform *gorm.DB, d Database, password string) (string, string, error) {
	if validateHost(d.Host) != nil || validateSSLMode(d.SSLMode) == "" {
		return "", "", errTargetUnavailable
	}
	own, err := platformFingerprint(ctx, platform)
	if err != nil {
		return "", "", errTargetUnavailable
	}
	config, err := pgx.ParseConfig(postgresDSN(d, password))
	if err != nil {
		return "", "", errTargetUnavailable
	}
	config.ConnectTimeout = 5 * time.Second
	config.Tracer = telemetry.PostgresTracer{Scope: "target"}
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return "", "", errTargetUnavailable
	}
	defer conn.Close(context.Background())
	actual, err := targetdb.Fingerprint(ctx, conn)
	if err != nil {
		return "", "", errTargetUnavailable
	}
	expected := ""
	if d.PhysicalFingerprint != nil {
		expected = *d.PhysicalFingerprint
	}
	if err = targetdb.CheckIdentity(actual, own, expected); err != nil {
		return "", "", err
	}
	return actual, targetdb.RecoveryIdentity(conn.Config(), actual), nil
}

// A legacy registration with execution evidence must match the last identifiable
// attempt before binding. Existing attempt identities keep their original format.
func checkLegacyIdentity(platform *gorm.DB, id uuid.UUID, recoveryID string) error {
	var recorded string
	err := platform.Raw(`SELECT COALESCE((
		SELECT target_identity FROM dmp_migration.attempts
		WHERE database_id = ? AND target_identity NOT IN ('', 'pending')
		ORDER BY started_at DESC, id DESC LIMIT 1
	), '')`, id.String()).Scan(&recorded).Error
	if err == nil && recorded != "" && recorded != recoveryID {
		return targetdb.ErrChanged
	}
	return err
}

func (p *Provider) PostgreSQL(ctx context.Context, id string) (*pgxpool.Pool, error) {
	var d Database
	platform := p.platform.WithContext(ctx)
	if err := platform.First(&d, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrDatabaseNotFound
		}
		return nil, err
	}
	if validateHost(d.Host) != nil || validateSSLMode(d.SSLMode) == "" {
		return nil, errTargetUnavailable
	}
	password, err := decryptPassword(d.PasswordCiphertext)
	if err != nil {
		return nil, errTargetUnavailable
	}
	connectCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	if d.PhysicalFingerprint == nil {
		fingerprint, recoveryID, err := p.probe(connectCtx, platform, d, password)
		if err != nil {
			return nil, err
		}
		if err = checkLegacyIdentity(platform, d.ID, recoveryID); err != nil {
			return nil, err
		}
		result := platform.Model(&Database{}).
			Where("id = ? AND updated_at = ? AND (physical_fingerprint IS NULL OR physical_fingerprint = ?)", d.ID, d.UpdatedAt, fingerprint).
			UpdateColumn("physical_fingerprint", fingerprint)
		if result.Error != nil {
			var pgErr *pgconn.PgError
			if errors.As(result.Error, &pgErr) && pgErr.ConstraintName == "databases_physical_fingerprint_key" {
				return nil, errDuplicateTarget
			}
			return nil, result.Error
		}
		if result.RowsAffected != 1 {
			return nil, errRegistrationChanged
		}
		d.PhysicalFingerprint = &fingerprint
	}

	key := poolKey{id: id, dsn: postgresDSN(d, password), fingerprint: *d.PhysicalFingerprint}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, errors.New("connection provider is closed")
	}
	if cached, ok := p.pools[key]; ok {
		p.mu.Unlock()
		return cached, nil
	}
	p.mu.Unlock()

	own, err := platformFingerprint(connectCtx, platform)
	if err != nil {
		return nil, errTargetUnavailable
	}
	config, err := pgxpool.ParseConfig(key.dsn)
	if err != nil {
		return nil, errTargetUnavailable
	}
	config.MaxConns = 4
	config.MaxConnLifetime = 2 * time.Minute
	config.MaxConnIdleTime = 30 * time.Second
	config.ConnConfig.ConnectTimeout = 5 * time.Second
	config.ConnConfig.Tracer = telemetry.PostgresTracer{Scope: "target"}
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		actual, err := targetdb.Fingerprint(ctx, conn)
		if err != nil {
			return errTargetUnavailable
		}
		return targetdb.CheckIdentity(actual, own, key.fingerprint)
	}
	pool, err := pgxpool.NewWithConfig(connectCtx, config)
	if err != nil {
		return nil, errTargetUnavailable
	}
	if err = pool.Ping(connectCtx); err != nil {
		slog.LogAttrs(ctx, slog.LevelWarn, "database.ping", append(telemetry.ErrorAttrs(err), slog.String("status", "failed"))...)
		pool.Close()
		if errors.Is(err, targetdb.ErrPlatform) || errors.Is(err, targetdb.ErrChanged) {
			return nil, err
		}
		return nil, errTargetUnavailable
	}
	slog.InfoContext(ctx, "database.ping", "status", "succeeded")
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		pool.Close()
		return nil, errors.New("connection provider is closed")
	}
	if cached, ok := p.pools[key]; ok {
		pool.Close()
		return cached, nil
	}
	p.pools[key] = pool
	return pool, nil
}

func (p *Provider) MigrationPoolStats() []*pgxpool.Stat {
	p.mu.Lock()
	defer p.mu.Unlock()
	stats := make([]*pgxpool.Stat, 0, len(p.pools))
	for _, pool := range p.pools {
		stats = append(stats, pool.Stat())
	}
	return stats
}

// Close runs after HTTP requests and workers drain.
func (p *Provider) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	pools := p.pools
	p.pools = nil
	p.mu.Unlock()
	for _, pool := range pools {
		pool.Close()
	}
}
