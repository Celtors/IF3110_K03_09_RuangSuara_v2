package databasemanagement

import (
	"testing"

	fx "backend/access_control/effect"
)

func TestCreateDatabaseValidation(t *testing.T) {
	valid := CreateDatabaseRequest{Name: "test", Host: "db.example.test", Port: 5432, DatabaseName: "app", Username: "reader", Password: "secret"}
	old := getenv
	getenv = func(k string) string {
		if k == "TARGET_DB_ALLOWED_HOSTS" {
			return "db.example.test"
		}
		return ""
	}
	defer func() { getenv = old }()
	if err := valid.Validate(); err != nil {
		t.Fatalf("expected valid configuration, got %v", err)
	}
	valid.Port = 70000
	if err := valid.Validate(); err == nil {
		t.Fatal("expected invalid port to be rejected")
	}
}
func TestHostAllowlistAndLocalhost(t *testing.T) {
	old := getenv
	getenv = func(k string) string { return "" }
	defer func() { getenv = old }()
	if err := validateHost("localhost"); err == nil {
		t.Fatal("localhost must be rejected by default")
	}
	if err := validateHost("db.attacker.invalid"); err == nil {
		t.Fatal("unlisted hostname must be rejected")
	}
}
func TestPolicyRequestUsesSchemaEffects(t *testing.T) {
	for _, e := range []string{fx.ViewOnly, fx.ViewEdit, fx.Blocked} {
		if !fx.IsValid(e) {
			t.Fatalf("%q must be accepted", e)
		}
	}
	for _, e := range []string{"", "ALLOW", "DENY", "VIEW_ONLY", "VIEW_EDIT"} {
		if fx.IsValid(e) {
			t.Fatalf("legacy or empty effect %q must be rejected", e)
		}
	}
}

func TestIdentifierValidation(t *testing.T) {
	if !validateIdentifier(`table-name`) {
		t.Fatal("expected ordinary identifier to be valid")
	}
	if validateIdentifier("") {
		t.Fatal("empty identifier must be rejected")
	}
	if validateIdentifier(string(make([]byte, 64))) {
		t.Fatal("identifier longer than PostgreSQL's 63-byte limit must be rejected")
	}
	if validateIdentifier("bad\x00name") {
		t.Fatal("NUL byte must be rejected")
	}
}

func TestEffectivePermission(t *testing.T) {
	if !effectivePermission([]policyGrant{{Effect: fx.ViewOnly}, {Effect: fx.ViewEdit}}, true) {
		t.Fatal("ViewEdit in any group should grant edit")
	}
	if effectivePermission([]policyGrant{{Effect: fx.ViewEdit}, {Effect: fx.Blocked}}, false) {
		t.Fatal("Blocked in any group must override ViewEdit")
	}
	if effectivePermission([]policyGrant{{Effect: fx.ViewOnly}}, true) {
		t.Fatal("ViewOnly must not grant edit")
	}
	if !effectivePermission([]policyGrant{{Effect: fx.ViewOnly}}, false) {
		t.Fatal("ViewOnly should grant view")
	}
	if effectivePermission(nil, false) {
		t.Fatal("a user with no policy must have no access")
	}
}

func TestReadOnlyMethods(t *testing.T) {
	for _, m := range []string{"GET", "HEAD", "OPTIONS"} {
		if !isReadOnlyMethod(m) {
			t.Fatalf("%s must be treated as read-only", m)
		}
	}
	for _, m := range []string{"POST", "PATCH", "PUT", "DELETE"} {
		if isReadOnlyMethod(m) {
			t.Fatalf("%s changes state and must need ViewEdit", m)
		}
	}
}

func TestSupportedDBMSAndTLS(t *testing.T) {
	for _, engine := range []string{"postgres", "mariadb"} {
		if !validDBMS(engine) {
			t.Fatalf("expected %s", engine)
		}
	}
	for _, engine := range []string{"sqlite", "mysql", "unknown"} {
		if validDBMS(engine) {
			t.Fatalf("unexpected engine %s", engine)
		}
	}
	if normalizeDBMS("") != "postgres" {
		t.Fatal("legacy registrations must default to postgres")
	}
	if normalizeSSLModeForDBMS("mariadb", "") != "disable" {
		t.Fatal("incorrect MariaDB default")
	}
	if validateDBMSSSLMode("mariadb", "verify-full") != "" {
		t.Fatal("invalid MariaDB mode accepted")
	}
	if validateDBMSSSLMode("mariadb", "require") != "require" {
		t.Fatal("MariaDB TLS require must be supported")
	}
}
