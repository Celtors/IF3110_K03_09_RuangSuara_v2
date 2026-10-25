package databasemanagement

import "testing"

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
func TestPermissionConstants(t *testing.T) {
	if PermissionViewOnly == PermissionViewEdit {
		t.Fatal("permissions must be distinct")
	}
	if EffectAllow == EffectDeny {
		t.Fatal("effects must be distinct")
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
	if !effectivePermission([]policyGrant{{Effect: "ALLOW", Permission: PermissionViewOnly}, {Effect: "ALLOW", Permission: PermissionViewEdit}}, true) {
		t.Fatal("VIEW_EDIT allow should grant edit")
	}
	if effectivePermission([]policyGrant{{Effect: "ALLOW", Permission: PermissionViewEdit}, {Effect: "DENY", Permission: PermissionViewOnly}}, false) {
		t.Fatal("explicit deny must override allow")
	}
	if effectivePermission([]policyGrant{{Effect: "ALLOW", Permission: PermissionViewOnly}}, true) {
		t.Fatal("VIEW_ONLY must not grant edit")
	}
}
