package targetdb

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

var ErrPlatform = errors.New("the platform database cannot be used as a managed target")
var ErrChanged = errors.New("the connection resolves to a different physical database")

const FingerprintSQL = `SELECT system_identifier::text || '/' ||
 (SELECT oid::text FROM pg_catalog.pg_database WHERE datname=pg_catalog.current_database())
 FROM pg_catalog.pg_control_system()`

// Registration identity is independent of host aliases and credentials.
func Fingerprint(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) (string, error) {
	var fingerprint string
	err := q.QueryRow(ctx, FingerprintSQL).Scan(&fingerprint)
	return fingerprint, err
}

// RecoveryIdentity preserves the format of identity evidence in existing attempts.
func RecoveryIdentity(config *pgx.ConnConfig, fingerprint string) string {
	cluster, database, _ := strings.Cut(fingerprint, "/")
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%s\x00%s\x00%s",
		config.Host, config.Port, config.Database, cluster, database))))
}

func CheckIdentity(actual, platform, expected string) error {
	switch {
	case actual == platform:
		return ErrPlatform
	case expected != "" && actual != expected:
		return ErrChanged
	default:
		return nil
	}
}

const LedgerSchema = "dmp_batch"
