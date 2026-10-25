package databasemanagement

import (
	"net/url"
	"strings"
	"testing"
)

func TestDBMSAndTLSMatrix(t *testing.T) {
	cases := []struct{ engine, mode, want string }{
		{"", "", "verify-full"}, {"postgres", "", "verify-full"},
		{"POSTGRES", "require", "require"}, {"MariaDB", "", "disable"},
		{"mariadb", "require", "require"}, {"mariadb", "verify-full", ""},
		{"unknown", "", ""},
	}
	for _, c := range cases {
		t.Run(c.engine+"/"+c.mode, func(t *testing.T) {
			if !validDBMS(c.engine) && c.want != "" {
				t.Fatal("expected supported engine")
			}
			got := ""
			if validDBMS(c.engine) {
				got = validateDBMSSSLMode(normalizeDBMS(c.engine), normalizeSSLModeForDBMS(c.engine, c.mode))
			}
			if got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestMariaDSNEncodingAndTLS(t *testing.T) {
	d := Database{DBMS: DBMSMariaDB, Host: "localhost", Port: 3306, DatabaseName: "sample", Username: "u", SSLMode: "disable"}
	dsn, err := mariaDSN(d, "p@ss:word")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dsn, "tcp(localhost:3306)") || !strings.Contains(dsn, "parseTime=true") {
		t.Fatalf("unexpected driver DSN: %s", strings.ReplaceAll(dsn, "p@ss:word", "[REDACTED]"))
	}
	d.SSLMode = "require"
	secure, err := mariaDSN(d, "secret")
	if err != nil || !strings.Contains(secure, "tls=true") {
		t.Fatalf("TLS required: %v", err)
	}
	d.SSLMode = "verify-ca"
	if _, err := mariaDSN(d, "secret"); err == nil {
		t.Fatal("unsupported TLS mode must fail closed")
	}
}

func TestPostgresDSNEscapesCredentials(t *testing.T) {
	d := Database{Host: "db.example.test", Port: 5432, DatabaseName: "hello", Username: "test user", SSLMode: "require"}
	raw := postgresDSN(d, "secret@/?")
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	pw, ok := u.User.Password()
	if !ok || pw != "secret@/?" || u.User.Username() != "test user" || u.Query().Get("sslmode") != "require" {
		t.Fatal("PostgreSQL DSN did not round-trip escaped credentials")
	}
}

func TestCreateDatabaseValidationForBothEngines(t *testing.T) {
	t.Setenv("ALLOW_PRIVATE_TARGETS", "true")
	cases := []struct {
		engine string
		port   int
		mode   string
		ok     bool
	}{
		{"postgres", 5432, "disable", true}, {"mariadb", 3306, "disable", true},
		{"mariadb", 3306, "verify-full", false}, {"sqlite", 1234, "", false},
		{"postgres", 0, "", false},
	}
	for _, c := range cases {
		req := CreateDatabaseRequest{DBMS: c.engine, Name: "fixture", Host: "localhost", Port: c.port, DatabaseName: "example", Username: "user", Password: "pass", SSLMode: c.mode}
		if got := req.Validate() == nil; got != c.ok {
			t.Errorf("%+v validation success = %t want %t", c, got, c.ok)
		}
	}
}
