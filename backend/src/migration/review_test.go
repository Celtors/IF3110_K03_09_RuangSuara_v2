package migration

import (
	"errors"
	"strings"
	"testing"
)

func codeOf(err error) string {
	if err == nil {
		return ""
	}
	code, _ := faultInfo(err)
	return code
}

func TestSQLHashIsStableAndSensitive(t *testing.T) {
	a := sqlHash("CREATE TABLE public.t (id integer)")
	if len(a) != 64 {
		t.Fatalf("hash length = %d, want 64 hex characters", len(a))
	}
	if a != sqlHash("CREATE TABLE public.t (id integer)") {
		t.Fatal("hash is not deterministic")
	}
	if a == sqlHash("CREATE TABLE public.t (id integer) ") {
		t.Fatal("a one-character change must change the hash")
	}
}

func TestCheckApprovers(t *testing.T) {
	ok := RequiredApprover{GroupID: "g1", GroupName: "data-engineers", LeaderID: "u1", LeaderUsername: "alice"}
	cases := []struct {
		name      string
		approvers []RequiredApprover
		wantCode  string
		wantInMsg string
	}{
		{"nobody has access", nil, "conflict", "nobody can approve"},
		// A missing or inactive leader does not block a submission: the approval stays pending
		// until a leader is assigned and decides it.
		{"group without a leader", []RequiredApprover{ok, {GroupID: "g2", GroupName: "analysts"}}, "", ""},
		{"only group has no leader", []RequiredApprover{{GroupID: "g2", GroupName: "analysts"}}, "", ""},
		{"single team with a leader", []RequiredApprover{ok}, "", ""},
		{"several teams, all led", []RequiredApprover{ok, {GroupID: "g2", GroupName: "analysts", LeaderID: "u2", LeaderUsername: "bob"}}, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkApprovers(c.approvers)
			if codeOf(err) != c.wantCode {
				t.Fatalf("code = %q, want %q (err=%v)", codeOf(err), c.wantCode, err)
			}
			if c.wantInMsg != "" && !strings.Contains(err.Error(), c.wantInMsg) {
				t.Fatalf("message %q should mention %q", err.Error(), c.wantInMsg)
			}
		})
	}
}

func TestRequestSQLStateRules(t *testing.T) {
	withDown := Migration{State: "draft", Draft: Draft{Name: "add table", SQLUp: "CREATE TABLE public.t (id integer)", SQLDown: "DROP TABLE public.t"}}
	cases := []struct {
		name     string
		m        Migration
		op       Operation
		wantCode string
	}{
		{"apply a draft with SQL down", withDown, OperationApply, ""},
		{"apply a draft without SQL down or irreversible mark", Migration{State: "draft", Draft: Draft{Name: "x", SQLUp: "CREATE TABLE public.t (id integer)"}}, OperationApply, "invalid_input"},
		{"apply an irreversible draft with a reason", Migration{State: "draft", Draft: Draft{Name: "x", SQLUp: "CREATE TABLE public.t (id integer)", Irreversible: true, IrreversibleReason: "data loss"}}, OperationApply, ""},
		{"apply an already applied migration", Migration{State: "applied", Draft: withDown.Draft}, OperationApply, "already_applied"},
		{"apply a rolled back migration", Migration{State: "rolled_back", Draft: withDown.Draft}, OperationApply, "conflict"},
		{"rollback a draft", withDown, OperationRollback, "conflict"},
		{"rollback an applied migration", Migration{State: "applied", Draft: withDown.Draft}, OperationRollback, ""},
		{"rollback an irreversible migration", Migration{State: "applied", Draft: Draft{Name: "x", SQLUp: "CREATE TABLE public.t (id integer)", Irreversible: true, IrreversibleReason: "data loss"}}, OperationRollback, "conflict"},
		{"forward a rolled back migration", Migration{State: "rolled_back", Draft: withDown.Draft}, OperationForward, ""},
		{"forward an applied migration", Migration{State: "applied", Draft: withDown.Draft}, OperationForward, "conflict"},
		{"unknown operation", withDown, Operation("drop-everything"), "invalid_input"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sql, err := requestSQL(c.m, c.op)
			if codeOf(err) != c.wantCode {
				t.Fatalf("code = %q, want %q (err=%v)", codeOf(err), c.wantCode, err)
			}
			if c.wantCode == "" && strings.TrimSpace(sql) == "" {
				t.Fatal("a valid request must return the SQL it will run")
			}
		})
	}
}

func TestRawSQLPicksTheOperationsScript(t *testing.T) {
	m := Migration{Draft: Draft{SQLUp: "up", SQLDown: "down"}}
	if rawSQL(m, OperationApply) != "up" || rawSQL(m, OperationForward) != "up" {
		t.Fatal("apply and forward run SQL up")
	}
	if rawSQL(m, OperationRollback) != "down" {
		t.Fatal("rollback runs SQL down")
	}
}

func TestFaultInfoHidesUnexpectedErrors(t *testing.T) {
	code, message := faultInfo(fault("conflict", "visible"))
	if code != "conflict" || message != "visible" {
		t.Fatalf("got %q %q", code, message)
	}
	code, message = faultInfo(errors.New("pq: password authentication failed for user x"))
	if code != "internal_error" || strings.Contains(message, "password") {
		t.Fatalf("unexpected errors must not leak details, got %q %q", code, message)
	}
}

func TestRequestStatusesMatchTheSchema(t *testing.T) {
	// Keep in step with the CHECK constraint on change_requests.status in review.sql.
	want := []RequestStatus{"pending_review", "approved", "queued", "running", "succeeded", "failed", "unknown", "rejected", "cancelled"}
	if len(allRequestStatuses) != len(want) {
		t.Fatalf("%d statuses known, schema has %d", len(allRequestStatuses), len(want))
	}
	for _, s := range want {
		if !allRequestStatuses[s] {
			t.Errorf("status %q is missing", s)
		}
	}
	for _, s := range []string{"", "done", "PENDING_REVIEW"} {
		if allRequestStatuses[RequestStatus(s)] {
			t.Errorf("status %q must not be accepted", s)
		}
	}
}

func TestValidOperation(t *testing.T) {
	for _, op := range []Operation{OperationApply, OperationRollback, OperationForward} {
		if !validOperation(op) {
			t.Errorf("%q should be valid", op)
		}
	}
	if validOperation("") || validOperation("Apply") {
		t.Fatal("operations are exact, lower-case values")
	}
}

func TestNotifyNeverBlocks(t *testing.T) {
	r := NewReview(&Service{})
	for i := 0; i < 5; i++ {
		r.notify() // the wake channel holds one signal; extra signals are dropped, not queued
	}
	select {
	case <-r.wake:
	default:
		t.Fatal("expected a pending wake signal")
	}
	select {
	case <-r.wake:
		t.Fatal("signals must coalesce into one")
	default:
	}
}
