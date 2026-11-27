package migration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestHideRolledBackExecutions(t *testing.T) {
	applyID, forwardID := "E1", "E3"
	apply := ExecutionHistoryEntry{ExecutionID: applyID, MigrationID: "B", Operation: OperationApply, Status: "succeeded"}
	failedRollback := ExecutionHistoryEntry{
		ExecutionID: "failed", MigrationID: "B", Operation: OperationRollback, Status: "failed", ReversesExecutionID: &applyID,
	}
	rollback := ExecutionHistoryEntry{
		ExecutionID: "E2", MigrationID: "B", Operation: OperationRollback, Status: "succeeded", ReversesExecutionID: &applyID,
	}
	forward := ExecutionHistoryEntry{ExecutionID: forwardID, MigrationID: "B", Operation: OperationForward, Status: "succeeded"}
	secondRollback := ExecutionHistoryEntry{
		ExecutionID: "E4", MigrationID: "B", Operation: OperationRollback, Status: "succeeded", ReversesExecutionID: &forwardID,
	}
	unrelated := ExecutionHistoryEntry{ExecutionID: "other", MigrationID: "C", Operation: OperationApply, Status: "succeeded"}

	t.Run("failed rollback hides neither record", func(t *testing.T) {
		assertHistoryIDs(t, hideRolledBackExecutions([]ExecutionHistoryEntry{apply, failedRollback}), "E1", "failed")
	})
	t.Run("forward remains visible", func(t *testing.T) {
		history := []ExecutionHistoryEntry{apply, failedRollback, rollback, forward, unrelated}
		assertHistoryIDs(t, hideRolledBackExecutions(history), "failed", "E3", "other")
	})
	t.Run("repeated pairs preserve the input", func(t *testing.T) {
		history := []ExecutionHistoryEntry{apply, failedRollback, rollback, forward, secondRollback, unrelated}
		original := slices.Clone(history)
		assertHistoryIDs(t, hideRolledBackExecutions(history), "failed", "other")
		if !reflect.DeepEqual(history, original) {
			t.Fatalf("filter changed its input: %+v", history)
		}
	})
	t.Run("missing or unsuccessful target preserves the record", func(t *testing.T) {
		failedID := failedRollback.ExecutionID
		invalidLink := rollback
		invalidLink.ReversesExecutionID = &failedID
		assertHistoryIDs(t, hideRolledBackExecutions([]ExecutionHistoryEntry{failedRollback, invalidLink}), "failed", "E2")
		assertHistoryIDs(t, hideRolledBackExecutions([]ExecutionHistoryEntry{rollback}), "E2")
	})
}

const historyFixture = `
INSERT INTO dmp_migration.definitions (id,database_id,name,sql_script,author_user_id,state,version) VALUES
    ('hist-failed','slice3-history','Failed','SELECT 0','author','failed',1),
    ('hist-cycle','slice3-history','Same name','SELECT 1','author','rolled_back',2),
    ('hist-other','slice3-history','Same name','SELECT 2','author','applied',3),
    ('hist-running','slice3-history','Running','SELECT 3','author','draft',4),
    ('hist-outside','slice3-other','Other database','SELECT 4','author','unknown',NULL);

INSERT INTO dmp_migration.attempts
    (id,migration_id,database_id,operation,reverses_execution_id,status,version,sql_script_snapshot,executor_user_id,started_at,completed_at) VALUES
    ('h50-other','hist-other','slice3-history','apply',NULL,'succeeded',5,'SELECT 2','executor','2026-11-20 10:00Z','2026-11-20 10:01Z'),
    ('h00-failed','hist-failed','slice3-history','apply',NULL,'failed',NULL,'SELECT 0','executor','2026-11-20 10:00Z','2026-11-20 10:01Z'),
    ('h10-apply','hist-cycle','slice3-history','apply',NULL,'succeeded',1,'SELECT 1','executor','2026-11-20 10:00Z','2026-11-20 10:01Z'),
    ('h11-failed-rollback','hist-cycle','slice3-history','rollback','h10-apply','failed',NULL,'SELECT -1','executor','2026-11-20 10:00Z','2026-11-20 10:01Z'),
    ('h20-rollback','hist-cycle','slice3-history','rollback','h10-apply','succeeded',2,'SELECT -1','executor','2026-11-20 10:00Z','2026-11-20 10:01Z'),
    ('h30-forward','hist-cycle','slice3-history','forward',NULL,'succeeded',3,'SELECT 1','executor','2026-11-20 10:00Z','2026-11-20 10:01Z'),
    ('h40-rollback','hist-cycle','slice3-history','rollback','h30-forward','succeeded',4,'SELECT -1','executor','2026-11-20 10:00Z','2026-11-20 10:01Z'),
    ('h60-running','hist-running','slice3-history','apply',NULL,'running',NULL,'SELECT 3','executor','2026-11-20 10:01Z',NULL),
    ('outside','hist-outside','slice3-other','apply',NULL,'unknown',NULL,'SELECT 4','executor','2026-11-20 10:01Z',NULL);

UPDATE dmp_migration.attempts SET postgres_error_code='42P01',error_message='Table unavailable' WHERE id='h00-failed';`

type historyTestAccess struct{ fakeAccess }

func (historyTestAccess) Identity(c *gin.Context) (string, error) {
	return c.GetHeader("X-Test-User"), nil
}

func testExecutionHistoryAPI(t *testing.T, ctx context.Context, service *Service) {
	t.Helper()
	if _, err := service.Repository.Pool.Exec(ctx, historyFixture); err != nil {
		t.Fatal(err)
	}

	router := gin.New()
	RegisterRoutes(router, &Service{
		Repository: service.Repository,
		Access:     historyTestAccess{fakeAccess{perms: map[string]Permission{"view": View, "edit": Edit}}},
	})
	get := func(user, path string, status int) []byte {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/databases/"+path, nil)
		request.Header.Set("X-Test-User", user)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != status {
			t.Fatalf("GET %s for %q returned %d, want %d. Body %s", path, user, response.Code, status, response.Body)
		}
		return response.Body.Bytes()
	}
	readHistory := func(path string) []ExecutionHistoryEntry {
		t.Helper()
		var payload struct {
			Attempts []ExecutionHistoryEntry `json:"attempts"`
		}
		if err := json.Unmarshal(get("view", path, http.StatusOK), &payload); err != nil {
			t.Fatal(err)
		}
		return payload.Attempts
	}

	const path = "slice3-history/migration-history"
	full := readHistory(path)
	assertHistoryIDs(t, full, "h00-failed", "h10-apply", "h11-failed-rollback", "h20-rollback", "h30-forward", "h40-rollback", "h50-other", "h60-running")
	failed := full[0]
	if failed.Status != "failed" || failed.MigrationVersion == nil || *failed.MigrationVersion != 1 || failed.SuccessOrder != nil ||
		failed.ExecutorUserID != "executor" || failed.SQLSnapshot != "SELECT 0" || failed.CompletedAt == nil ||
		failed.PostgresErrorCode != "42P01" || failed.Error != "Table unavailable" {
		t.Fatalf("failed apply metadata: %+v", failed)
	}
	applied := full[1]
	if applied.Operation != OperationApply || applied.MigrationName != "Same name" || applied.MigrationVersion == nil ||
		*applied.MigrationVersion != 2 || applied.SuccessOrder == nil || *applied.SuccessOrder != 1 {
		t.Fatalf("apply metadata: %+v", applied)
	}
	if link := full[5].ReversesExecutionID; link == nil || *link != full[4].ExecutionID {
		t.Fatalf("second rollback lost its forward link: %+v", full[5])
	}
	if running := full[len(full)-1]; running.Status != "running" || running.CompletedAt != nil || running.SuccessOrder != nil {
		t.Fatalf("running attempt metadata: %+v", running)
	}

	hidden := readHistory(path + "?hide_rollback=true")
	assertHistoryIDs(t, hidden, "h00-failed", "h11-failed-rollback", "h50-other", "h60-running")
	if after := readHistory(path + "?hide_rollback=false"); !reflect.DeepEqual(full, after) {
		t.Fatalf("filtered read changed history: %+v", after)
	}
	outside := readHistory("slice3-other/migration-history?hide_rollback=true")
	assertHistoryIDs(t, outside, "outside")
	if outside[0].Status != "unknown" || outside[0].MigrationVersion != nil || outside[0].CompletedAt != nil {
		t.Fatalf("unknown attempt metadata: %+v", outside[0])
	}
	if body := get("view", "no-attempts/migration-history", http.StatusOK); string(body) != `{"attempts":[]}` {
		t.Fatalf("empty history response: %s", body)
	}

	legacy := readHistory("legacy/migration-history")
	assertHistoryIDs(t, legacy, "failure", "success", "unknown")
	if legacy[0].MigrationVersion != nil || legacy[0].SQLSnapshot != "SELECT 2" || legacy[0].Operation != OperationApply {
		t.Fatalf("legacy failed attempt metadata: %+v", legacy[0])
	}
	legacyResponse := get("view", "legacy/migrations/applied/attempts", http.StatusOK)
	if !strings.Contains(string(legacyResponse), `"version":1`) {
		t.Fatalf("existing per-migration response lost its version field: %s", legacyResponse)
	}
	body := get("edit", path, http.StatusOK)
	if strings.Contains(string(body), `"version":`) || !strings.Contains(string(body), `"success_order":1`) ||
		!strings.Contains(string(body), `"migration_version":2`) {
		t.Fatalf("history response has ambiguous version fields: %s", body)
	}

	get("none", path, http.StatusForbidden)
	get("", path, http.StatusUnauthorized)
	if body := get("view", path+"?hide_rollback=invalid", http.StatusBadRequest); !strings.Contains(string(body), `"code":"invalid_input"`) {
		t.Fatalf("invalid hide_rollback error: %s", body)
	}
}

func assertHistoryIDs(t *testing.T, history []ExecutionHistoryEntry, want ...string) {
	t.Helper()
	got := make([]string, 0, len(history))
	for _, entry := range history {
		got = append(got, entry.ExecutionID)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("history IDs: got %v, want %v", got, want)
	}
}

func testDefinitionStateConstraintCleanup(t *testing.T, ctx context.Context, repo *Repository) {
	t.Helper()
	const records = `
 SELECT jsonb_build_object(
     'definitions', (SELECT jsonb_agg(to_jsonb(d) ORDER BY d.id) FROM dmp_migration.definitions d),
     'attempts', (SELECT jsonb_agg(to_jsonb(a) ORDER BY a.id) FROM dmp_migration.attempts a)
 )::text`

	tests := []struct {
		name string
		sql  string
	}{
		{
			name: "execution state check",
			sql: `
 ALTER TABLE dmp_migration.definitions ADD CONSTRAINT definitions_execution_state_check
 CHECK (state IN ('draft','running','failed','applied','rolled_back','unknown'))`,
		},
		{
			name: "legacy state check with approval labels",
			sql: `
 ALTER TABLE dmp_migration.definitions ADD CONSTRAINT definitions_state_check
 CHECK (state IN ('draft','pending_review','approved','running','failed','applied','rolled_back','unknown'));
 INSERT INTO dmp_migration.definitions (id,database_id,name,sql_script,author_user_id,state) VALUES
     ('pending_review','slice3-conflict','Pending','SELECT 1','author','pending_review'),
     ('approved','slice3-conflict','Approved','SELECT 2','author','approved')`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := repo.Pool.Exec(ctx, test.sql); err != nil {
				t.Fatal(err)
			}

			var before string
			if err := repo.Pool.QueryRow(ctx, records).Scan(&before); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := repo.Initialize(ctx); err != nil {
					t.Fatalf("initialize existing storage: %v", err)
				}
			}

			var obsoleteCheckExists bool
			err := repo.Pool.QueryRow(ctx, `
 SELECT EXISTS (
     SELECT 1 FROM pg_constraint
     WHERE conrelid='dmp_migration.definitions'::regclass
       AND conname IN ('definitions_execution_state_check','definitions_state_check')
 )`).Scan(&obsoleteCheckExists)
			if err != nil || obsoleteCheckExists {
				t.Fatalf("obsolete state constraint still exists: %t, error: %v", obsoleteCheckExists, err)
			}

			var after string
			if err := repo.Pool.QueryRow(ctx, records).Scan(&after); err != nil {
				t.Fatal(err)
			}
			if after != before {
				t.Fatal("cleanup changed existing definitions or attempts")
			}
		})
	}
}
