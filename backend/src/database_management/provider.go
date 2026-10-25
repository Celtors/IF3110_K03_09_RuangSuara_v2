package databasemanagement

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type Provider struct {
	platform *gorm.DB
	timeout  time.Duration
}

func NewProvider(platform *gorm.DB) *Provider {
	return &Provider{platform: platform, timeout: 5 * time.Second}
}
func (p *Provider) Target(ctx context.Context, id string) (*gorm.DB, error) {
	var target Database
	if err := p.platform.WithContext(ctx).Where("id = ?", id).First(&target).Error; err != nil {
		return nil, err
	}
	if err := validateHost(target.Host); err != nil {
		return nil, fmt.Errorf("target host rejected by security policy")
	}
	password, err := decryptPassword(target.PasswordCiphertext)
	if err != nil {
		return nil, err
	}
	// lib/pq-style keyword DSN uses proper escaping via url.UserPassword and URL encoding.
	u := &url.URL{Scheme: "postgres", Host: net.JoinHostPort(target.Host, strconv.Itoa(target.Port)), Path: "/" + target.DatabaseName, User: url.UserPassword(target.Username, password)}
	q := u.Query()
	q.Set("sslmode", target.SSLMode)
	u.RawQuery = q.Encode()
	db, err := gorm.Open(postgres.Open(u.String()), &gorm.Config{DisableAutomaticPing: true})
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
