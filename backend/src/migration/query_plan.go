package migration

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Plan struct {
	Raw   json.RawMessage `json:"plan"`
	Nodes []PlanNode      `json:"nodes"`
}
type PlanNode struct {
	Depth    int     `json:"depth"`
	Type     string  `json:"node_type"`
	Relation string  `json:"relation,omitempty"`
	Index    string  `json:"index,omitempty"`
	Join     string  `json:"join_type,omitempty"`
	Cost     float64 `json:"total_cost"`
	Rows     float64 `json:"estimated_rows"`
}

func explainPostgres(ctx context.Context, pool *pgxpool.Pool, sql string) (Plan, error) {
	var out Plan
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, fault("database_unavailable", "Target connection unavailable")
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, `SET LOCAL statement_timeout='5s'; SET LOCAL lock_timeout='2s'; SET LOCAL search_path=pg_catalog,public`); err != nil {
		return out, err
	}
	// Use EXPLAIN without ANALYZE to preview the plan without executing the query.
	err = tx.QueryRow(ctx, "EXPLAIN (FORMAT JSON) "+sql, pgx.QueryExecModeExec).Scan(&out.Raw)
	if err != nil {
		return out, fault("invalid_sql", "PostgreSQL could not plan this statement")
	}
	var roots []struct {
		Root map[string]any `json:"Plan"`
	}
	if err = json.Unmarshal(out.Raw, &roots); err != nil {
		return out, err
	}
	out.Nodes = []PlanNode{}
	var walk func(map[string]any, int)
	walk = func(n map[string]any, depth int) {
		str := func(k string) string { v, _ := n[k].(string); return v }
		num := func(k string) float64 { v, _ := n[k].(float64); return v }
		out.Nodes = append(out.Nodes, PlanNode{
			Depth: depth, Type: str("Node Type"), Relation: str("Relation Name"),
			Index: str("Index Name"), Join: str("Join Type"),
			Cost: num("Total Cost"), Rows: num("Plan Rows"),
		})
		children, _ := n["Plans"].([]any)
		for _, child := range children {
			if m, ok := child.(map[string]any); ok {
				walk(m, depth+1)
			}
		}
	}
	for _, r := range roots {
		walk(r.Root, 0)
	}
	return out, nil
}
