package databasemanagement

import (
	"net/url"
	"testing"
)

func TestDBMSAndTLSMatrix(t *testing.T) {
	cases := []struct{ engine, mode, want string }{
		{"", "", "verify-full"}, {"postgres", "", "verify-full"},
		{"POSTGRES", "require", "require"},
		{"unknown", "", ""},
	}
	for _, c := range cases {
		t.Run(c.engine+"/"+c.mode, func(t *testing.T) {
			if !validDBMS(c.engine) && c.want != "" {
				t.Fatal("expected supported engine")
			}
			got := ""
			if validDBMS(c.engine) {
				got = validateSSLMode(c.mode)
			}
			if got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
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
		{"postgres", 5432, "disable", true},
		{"postgres", 0, "", false},
	}
	for _, c := range cases {
		req := CreateDatabaseRequest{Name: "fixture", Host: "localhost", Port: c.port, DatabaseName: "example", Username: "user", Password: "pass", SSLMode: c.mode}
		if got := req.Validate() == nil; got != c.ok {
			t.Errorf("%+v validation success = %t want %t", c, got, c.ok)
		}
	}
}
