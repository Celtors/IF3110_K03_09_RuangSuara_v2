package databasemanagement

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type Provider struct {
	platform *gorm.DB
	timeout  time.Duration
	mu       sync.Mutex
	pools    map[poolKey]*pgxpool.Pool
	closed   bool
}

type poolKey struct {
	id, dsn string
}

var ErrDatabaseNotFound = errors.New("managed database not found")

func NewProvider(platform *gorm.DB) *Provider {
	return &Provider{platform: platform, timeout: 5 * time.Second, pools: make(map[poolKey]*pgxpool.Pool)}
}
func (p *Provider) targetDSN(ctx context.Context, id string) (string, error) {
	var target Database
	if err := p.platform.WithContext(ctx).Where("id = ?", id).First(&target).Error; err != nil {
		return "", err
	}
	if err := validateHost(target.Host); err != nil {
		return "", fmt.Errorf("target host rejected by security policy")
	}
	password, err := decryptPassword(target.PasswordCiphertext)
	if err != nil {
		return "", err
	}
	// lib/pq-style keyword DSN uses proper escaping via url.UserPassword and URL encoding.
	u := &url.URL{Scheme: "postgres", Host: net.JoinHostPort(target.Host, strconv.Itoa(target.Port)), Path: "/" + target.DatabaseName, User: url.UserPassword(target.Username, password)}
	q := u.Query()
	q.Set("sslmode", target.SSLMode)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (p *Provider) Target(ctx context.Context, id string) (*gorm.DB, error) {
	dsn, err := p.targetDSN(ctx, id)
	if err != nil {
		return nil, err
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		return nil, fmt.Errorf("target database connection failed")
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("target database connection failed")
	}
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(2 * time.Minute)
	sqlDB.SetConnMaxIdleTime(30 * time.Second)
	pingCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("target database connection failed or timed out")
	}
	return db, nil
}

// PostgreSQL supplies provider-owned pools to Migration. Credential lookup,
// decryption and host validation remain in the same provider as GORM consumers.
func (p *Provider) PostgreSQL(ctx context.Context, id string) (*pgxpool.Pool, error) {
	dsn, err := p.targetDSN(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrDatabaseNotFound
		}
		return nil, err
	}
	key := poolKey{id: id, dsn: dsn}
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
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("invalid target database configuration")
	}
	config.MaxConns = 4
	config.MaxConnLifetime = 2 * time.Minute
	config.MaxConnIdleTime = 30 * time.Second
	config.ConnConfig.ConnectTimeout = p.timeout
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, errors.New("target database connection failed")
	}
	pingCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	if err = pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, errors.New("target database connection failed or timed out")
	}
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

// Close runs after HTTP requests drain. Pools are keyed by registration and
// configuration, so A -> B -> A reuses A without closing an in-flight borrower.
func (p *Provider) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	pools := make([]*pgxpool.Pool, 0, len(p.pools))
	for _, cached := range p.pools {
		pools = append(pools, cached)
	}
	p.pools = nil
	p.mu.Unlock()
	for _, pool := range pools {
		pool.Close()
	}
}
func closeTarget(db *gorm.DB) {
	if db != nil {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
}
func normalizeSSLMode(mode string) string {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return "verify-full"
	}
	return mode
}
