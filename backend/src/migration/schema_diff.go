package migration

import (
	"fmt"
	"sort"
	"strings"
)

func CompareSchemas(current, desired Schema) (Diff, error) {
	out := Diff{Current: current, Differences: []Difference{}, Warnings: append([]string{}, current.Unsupported...)}
	if err := validateSchema(desired); err != nil {
		return out, err
	}
	if current.Name != desired.Name {
		return out, fault("invalid_input", "Current and desired schemas must have the same name")
	}
	qualified := func(t string) string { return quote(desired.Name) + "." + quote(t) }
	// Foreign keys depend on keys/indexes in OTHER tables. 
	// Remove them first and recreate them last, independently of the order of tables in either snapshot.
	const (
		dropForeignKeys = iota
		dropConstraintsAndIndexes
		dropTablesAndColumns
		createAndAlterTables
		addConstraints
		addIndexes
		addForeignKeys
		stageCount
	)
	stages := make([][]Difference, stageCount)
	add := func(stage int, kind, obj, sql string, destructive bool) {
		stages[stage] = append(stages[stage], Difference{kind, obj, destructive, sql + ";"})
		if destructive {
			out.Warnings = append(out.Warnings, "Potential data loss or validation failure: "+obj)
		}
	}
	old := map[string]Table{}
	next := map[string]Table{}
	for _, t := range current.Tables {
		old[t.Name] = t
	}
	for _, t := range desired.Tables {
		next[t.Name] = t
	}
	constraints := func(xs []Constraint) map[string]string {
		m := map[string]string{}
		for _, x := range xs {
			m[x.Name] = x.Definition
		}
		return m
	}
	indexes := func(xs []Index) map[string]Index {
		m := map[string]Index{}
		for _, x := range xs {
			m[x.Name] = x
		}
		return m
	}
	indexEqual := func(a, b Index) bool {
		return a.Unique == b.Unique && strings.Join(a.Columns, "\x00") == strings.Join(b.Columns, "\x00")
	}
	colDef := func(c Column) string {
		v := quote(c.Name) + " " + c.Type
		if !c.Nullable {
			v += " NOT NULL"
		}
		if c.Default != nil {
			v += " DEFAULT " + *c.Default
		}
		return v
	}
	for _, t := range current.Tables {
		nt, exists := next[t.Name]
		oc, nc := constraints(t.Constraints), constraints(nt.Constraints)
		oi, ni := indexes(t.Indexes), indexes(nt.Indexes)
		for _, co := range t.Constraints {
			if !exists || nc[co.Name] != oc[co.Name] {
				constraint, err := parseConstraint(co)
				if err != nil {
					return out, err
				}
				stage := dropConstraintsAndIndexes
				if constraint["contype"] == "CONSTR_FOREIGN" {
					stage = dropForeignKeys
				}
				add(stage, "drop_constraint", t.Name+"."+co.Name, "ALTER TABLE "+qualified(t.Name)+" DROP CONSTRAINT "+quote(co.Name), true)
			}
		}
		for _, ix := range t.Indexes {
			v, ok := ni[ix.Name]
			if !ix.Unsupported && (!exists || !ok || !indexEqual(oi[ix.Name], v)) {
				add(dropConstraintsAndIndexes, "drop_index", ix.Name, "DROP INDEX "+quote(current.Name)+"."+quote(ix.Name), false)
			}
		}
		if !exists {
			add(dropTablesAndColumns, "drop_table", t.Name, "DROP TABLE "+qualified(t.Name), true)
			continue
		}
		cols := map[string]Column{}
		for _, c := range nt.Columns {
			cols[c.Name] = c
		}
		removed, added := 0, 0
		oldcols := map[string]Column{}
		for _, c := range t.Columns {
			oldcols[c.Name] = c
		}
		for _, c := range t.Columns {
			n, ok := cols[c.Name]
			obj := t.Name + "." + c.Name
			if c.Unsupported {
				out.Warnings = append(out.Warnings, "No automatic changes to identity/generated column "+obj)
				continue
			}
			if !ok {
				removed++
				add(dropTablesAndColumns, "drop_column", obj, "ALTER TABLE "+qualified(t.Name)+" DROP COLUMN "+quote(c.Name), true)
				continue
			}
			prefix := "ALTER TABLE " + qualified(t.Name) + " ALTER COLUMN " + quote(c.Name)
			if c.Type != n.Type {
				add(createAndAlterTables, "alter_type", obj, prefix+" TYPE "+n.Type, true)
				out.Warnings = append(out.Warnings, "Type casts may require a manually reviewed USING expression: "+obj)
			}
			if c.Nullable != n.Nullable {
				suffix := " DROP NOT NULL"
				if !n.Nullable {
					suffix = " SET NOT NULL"
				}
				add(createAndAlterTables, "alter_nullability", obj, prefix+suffix, !n.Nullable)
			}
			if defaultValue(c.Default) != defaultValue(n.Default) {
				suffix := " DROP DEFAULT"
				if n.Default != nil {
					suffix = " SET DEFAULT " + *n.Default
				}
				add(createAndAlterTables, "alter_default", obj, prefix+suffix, false)
			}
		}
		for _, c := range nt.Columns {
			if _, ok := oldcols[c.Name]; !ok {
				added++
				add(createAndAlterTables, "add_column", t.Name+"."+c.Name, "ALTER TABLE "+qualified(t.Name)+" ADD COLUMN "+colDef(c), !c.Nullable && c.Default == nil)
			}
		}
		if removed > 0 && added > 0 {
			out.Warnings = append(out.Warnings, "Possible column rename in "+t.Name+": generated DROP/ADD does not preserve old values. Replace with RENAME COLUMN if intended.")
		}
	}
	for _, t := range desired.Tables {
		ot, exists := old[t.Name]
		if !exists {
			defs := []string{}
			for _, c := range t.Columns {
				defs = append(defs, colDef(c))
			}
			add(createAndAlterTables, "add_table", t.Name, "CREATE TABLE "+qualified(t.Name)+" ("+strings.Join(defs, ", ")+")", false)
		}
		oc := constraints(ot.Constraints)
		oi := indexes(ot.Indexes)
		for _, co := range t.Constraints {
			if oc[co.Name] != co.Definition {
				constraint, err := parseConstraint(co)
				if err != nil {
					return out, err
				}
				stage := addConstraints
				if constraint["contype"] == "CONSTR_FOREIGN" {
					stage = addForeignKeys
				}
				add(stage, "add_constraint", t.Name+"."+co.Name, "ALTER TABLE "+qualified(t.Name)+" ADD CONSTRAINT "+quote(co.Name)+" "+co.Definition, false)
			}
		}
		for _, ix := range t.Indexes {
			v, ok := oi[ix.Name]
			if ix.Unsupported || v.Unsupported {
				continue
			}
			if !ok || !indexEqual(v, ix) {
				cols := []string{}
				for _, c := range ix.Columns {
					cols = append(cols, quote(c))
				}
				u := ""
				if ix.Unique {
					u = "UNIQUE "
				}
				add(addIndexes, "add_index", ix.Name, "CREATE "+u+"INDEX "+quote(ix.Name)+" ON "+qualified(t.Name)+" ("+strings.Join(cols, ", ")+")", false)
			}
		}
	}
	for _, stage := range stages {
		out.Differences = append(out.Differences, stage...)
	}
	sql := []string{}
	for _, d := range out.Differences {
		sql = append(sql, d.SQL)
	}
	out.SQL = strings.Join(sql, "\n")
	if out.SQL != "" {
		if _, err := parseSQL(out.SQL, false); err != nil {
			return out, fmt.Errorf("generated SQL validation: %w", err)
		}
	}
	sort.Strings(out.Warnings)
	return out, nil
}
func defaultValue(p *string) string {
	if p == nil {
		return "<no-default>"
	}
	return *p
}
