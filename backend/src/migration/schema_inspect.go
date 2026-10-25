package migration

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func inspectSchema(ctx context.Context, pool *pgxpool.Pool, name string) (Schema, error) {
	out := Schema{Name: name, Tables: []Table{}, Unsupported: []string{}}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, fault("database_unavailable", "Target connection unavailable")
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, `SET LOCAL statement_timeout='5s'; SET LOCAL lock_timeout='2s'`); err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, `SELECT c.relname,c.relkind::text FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relkind IN ('r','p','v','m','f') ORDER BY c.relname`, name)
	if err != nil {
		return out, err
	}
	names := []string{}
	for rows.Next() {
		var n, kind string
		if err = rows.Scan(&n, &kind); err != nil {
			rows.Close()
			return out, err
		}
		if kind != "r" {
			out.Unsupported = append(out.Unsupported, "Unsupported relation left untouched: "+n)
		} else {
			names = append(names, n)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	for _, n := range names {
		t := Table{Name: n, Columns: []Column{}, Constraints: []Constraint{}, Indexes: []Index{}}
		rows, err = tx.Query(ctx, `SELECT a.attname,pg_catalog.format_type(a.atttypid,a.atttypmod),NOT a.attnotnull,pg_catalog.pg_get_expr(d.adbin,d.adrelid),a.attidentity<>'' OR a.attgenerated<>'' FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_class c ON c.oid=a.attrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace LEFT JOIN pg_catalog.pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum WHERE n.nspname=$1 AND c.relname=$2 AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum`, name, n)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var col Column
			if err = rows.Scan(&col.Name, &col.Type, &col.Nullable, &col.Default, &col.Unsupported); err != nil {
				rows.Close()
				return out, err
			}
			t.Columns = append(t.Columns, col)
			if col.Unsupported {
				out.Unsupported = append(out.Unsupported, "Identity/generated column requires manual review: "+n+"."+col.Name)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
		rows, err = tx.Query(ctx, `SELECT con.conname,pg_catalog.pg_get_constraintdef(con.oid,true) FROM pg_catalog.pg_constraint con JOIN pg_catalog.pg_class c ON c.oid=con.conrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=$2 AND con.contype IN ('p','u','f','c','x') ORDER BY con.conname`, name, n)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var co Constraint
			if err = rows.Scan(&co.Name, &co.Definition); err != nil {
				rows.Close()
				return out, err
			}
			t.Constraints = append(t.Constraints, co)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
		rows, err = tx.Query(ctx, `SELECT ci.relname,ix.indisunique,
 ARRAY(SELECT a.attname::text FROM unnest(ix.indkey) WITH ORDINALITY k(attnum,ord) JOIN pg_catalog.pg_attribute a ON a.attrelid=c.oid AND a.attnum=k.attnum ORDER BY k.ord),
 ix.indexprs IS NOT NULL OR ix.indpred IS NOT NULL OR ix.indnkeyatts<>ix.indnatts OR am.amname<>'btree'
 OR EXISTS(SELECT 1 FROM unnest(ix.indoption) opt WHERE opt<>0)
 OR EXISTS(SELECT 1 FROM unnest(ix.indclass) cls JOIN pg_catalog.pg_opclass opc ON opc.oid=cls WHERE NOT opc.opcdefault)
 OR EXISTS(SELECT 1 FROM unnest(ix.indkey,ix.indcollation) k(attnum,coll) JOIN pg_catalog.pg_attribute a ON a.attrelid=c.oid AND a.attnum=k.attnum WHERE k.coll<>a.attcollation)
 FROM pg_catalog.pg_index ix JOIN pg_catalog.pg_class c ON c.oid=ix.indrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace JOIN pg_catalog.pg_class ci ON ci.oid=ix.indexrelid JOIN pg_catalog.pg_am am ON am.oid=ci.relam WHERE n.nspname=$1 AND c.relname=$2 AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_constraint co WHERE co.conindid=ix.indexrelid) ORDER BY ci.relname`, name, n)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var i Index
			if err = rows.Scan(&i.Name, &i.Unique, &i.Columns, &i.Unsupported); err != nil {
				rows.Close()
				return out, err
			}
			t.Indexes = append(t.Indexes, i)
			if i.Unsupported {
				out.Unsupported = append(out.Unsupported, "Complex index left untouched: "+i.Name)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
		out.Tables = append(out.Tables, t)
	}
	return out, nil
}
