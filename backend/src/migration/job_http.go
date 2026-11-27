package migration

import (
	"github.com/gin-gonic/gin"
	"net/http"
)

func (r *Review) jobStatus(c *gin.Context) {
	ctx := c.Request.Context()
	user := c.GetString("migration_user")
	db := canonicalDatabaseID(c.Param("id"))
	id := c.Param("jobId")
	q, err := r.Get(ctx, user, db, id)
	if err != nil {
		respond(c, nil, err)
		return
	}
	state := string(q.Status)
	if q.Status == RequestSucceeded {
		state = "completed"
	}
	if q.Status == RequestUnknown {
		state = "unresolved"
	}
	result := gin.H{"job_id": id, "database_id": db, "migration_id": q.MigrationID, "operation": q.Operation, "status": state, "approval_status": q.Status, "committed_batches": 0, "committed_rows": nil, "checkpoint": nil, "error_code": q.ResultCode, "error": q.ResultMessage, "created_at": q.CreatedAt, "updated_at": q.UpdatedAt, "started_at": q.StartedAt, "completed_at": q.CompletedAt, "available_actions": []string{}, "compensation_status": nil, "mode": "normal"}
	if q.CorrectsBatchID != nil {
		result["corrects_batch_id"] = q.CorrectsBatchID
	}
	p, e := loadBatch(ctx, r.repo().Pool, id)
	if e == nil {
		actions := []string{}
		state = string(q.Status)
		switch q.Status {
		case RequestSucceeded:
			state = "completed"
		case RequestFailed:
			if p.State == "reverted" {
				state = "reverted"
			}
		case RequestRunning:
			state = p.State
			switch p.State {
			case "completed", "reverted", "failed":
				// The request snapshot may predate the atomic finalization commit,
				// or an older worker may have only partly finalized the platform.
				state = "running"
			case "ready":
				state = "queued"
			case "paused":
				state = "failed"
				if p.Batches > 0 {
					state = "partial"
				}
				if user == q.RequestedBy && !p.CompensationUnsafe {
					if r.Service.authorize(ctx, user, db, Edit) == nil {
						actions = append(actions, "resume")
						if p.Batches > 0 && p.DownHash != "" {
							actions = append(actions, "compensate")
						}
					}
				}
			case "compensating":
				state = "partial"
			}
		}
		if p.CorrectionStarted && q.Status == RequestFailed {
			state = "partial"
			if p.ResolvedBy != nil {
				state = "corrected"
			}
		}
		if p.State == "paused" && p.ResolvedBy == nil && (p.DownHash == "" || p.CompensationUnsafe) {
			var eligible bool
			var open *string
			e = r.repo().Pool.QueryRow(ctx, `SELECT EXISTS (`+eligibleCorrection+` AND p.id=$1),
				(SELECT id FROM dmp_migration.change_requests WHERE corrects_batch_id=$1
				AND status IN ('pending_review','approved','queued','running','unknown'))`, id).Scan(&eligible, &open)
			if e != nil {
				respond(c, nil, e)
				return
			}
			result["corrective_available"] = eligible && open == nil
			result["open_correction_id"] = open
			if open != nil {
				actions = []string{}
			}
		}
		result["resolved_by"] = p.ResolvedBy
		result["compensation_unsafe"] = p.CompensationUnsafe
		result["mode"] = "batch"
		result["status"] = state
		result["committed_batches"] = p.Batches
		result["committed_rows"] = p.Rows
		result["checkpoint"] = p.Cursor
		result["batch_size"] = p.Size
		result["batch_attribute"] = p.Attribute
		result["error"] = p.Error
		result["intent"] = p.Intent
		result["scope_rows"] = p.ScopeCount
		result["paused"] = p.State == "paused" && state != "corrected"
		result["updated_at"] = p.Updated
		result["last_verified_checkpoint"] = gin.H{"batch_number": p.Batches, "cursor": p.Cursor}
		if p.Error != "" {
			result["error_code"] = "batch_failed"
			if p.State == "unresolved" {
				result["error_code"] = "indeterminate"
			}
		}
		result["compensation_sql_hash"] = p.DownHash
		result["available_actions"] = actions
		result["reverted_batches"] = p.Reverted
		revertedRows := int64(0)
		if p.Reverted > 0 {
			revertedRows = p.Rows - (p.Batches-p.Reverted)*int64(p.Size)
		}
		result["reverted_rows"] = revertedRows
		result["remaining_committed_rows"] = p.Rows - revertedRows
		if p.Action == "compensate" {
			cs := "running"
			if state == "reverted" {
				cs = "reverted"
			} else if p.State == "paused" {
				cs = "failed"
				if p.Reverted > 0 {
					cs = "partially_reverted"
				}
			} else if p.State == "unresolved" {
				cs = "unresolved"
			}
			result["compensation_status"] = cs
		}
	} else if !isNoRows(e) {
		respond(c, nil, e)
		return
	} else if q.Status == RequestSucceeded && q.AttemptID != nil {
		var rows *int64
		if e = r.repo().Pool.QueryRow(ctx, `SELECT prepared_rows FROM dmp_migration.attempts WHERE id=$1`, *q.AttemptID).Scan(&rows); e != nil {
			respond(c, nil, e)
			return
		}
		result["committed_rows"] = rows
	}
	c.JSON(http.StatusOK, result)
}

func RegisterJobRoutes(router gin.IRouter, r *Review) {
	g := router.Group("/api/databases/:id/jobs")
	g.Use(reviewSecurityHeaders(), requireSession(r.Service))
	g.GET("/:jobId", r.jobStatus)
	for _, action := range []string{"resume", "compensate"} {
		a := action
		g.POST("/:jobId/"+a, func(c *gin.Context) {
			err := r.batchAction(c.Request.Context(), c.GetString("migration_user"), c.Param("id"), c.Param("jobId"), a)
			if err != nil {
				respond(c, nil, err)
				return
			}
			c.JSON(http.StatusAccepted, gin.H{"job_id": c.Param("jobId"), "status": "queued"})
		})
	}
}
