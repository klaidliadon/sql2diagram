package schema

import (
	"fmt"
	"slices"

	pgQuery "github.com/pganalyze/pg_query_go/v6"
)

type Schema struct {
	Tables []*Table
}

type ForeignReference struct {
	Table  string
	Column string
}

type Table struct {
	Name              string
	Columns           []*Column
	UniqueConstraints [][]string
}

type Column struct {
	Name                 string
	Type                 string
	Constraints          []string
	ForeignKeyReferences []*ForeignReference
	Length               int
}

func Parse(input string) (*Schema, error) {
	tree, err := pgQuery.Parse(input)
	if err != nil {
		return nil, fmt.Errorf("parse SQL statement: %w", err)
	}

	schema, err := astTreeToSchema(tree)
	if err != nil {
		return nil, fmt.Errorf("ast tree to schema: %w", err)
	}

	return schema, nil
}

func astTreeToSchema(tree *pgQuery.ParseResult) (*Schema, error) {
	schema := &Schema{
		Tables: make([]*Table, 0),
	}

	for _, stmt := range tree.Stmts {
		switch node := stmt.Stmt.Node.(type) {
		case *pgQuery.Node_CreateStmt:
			schema.Tables = append(schema.Tables, toTable(node))
		case *pgQuery.Node_AlterTableStmt:
			if err := alterTableStmt(schema, node.AlterTableStmt); err != nil {
				return nil, fmt.Errorf("handle alter table stmt: %w", err)
			}
		}
	}

	return schema, nil
}

func alterTableStmt(schema *Schema, stmt *pgQuery.AlterTableStmt) error {
	var sourceTable *Table

	for _, t := range schema.Tables {
		if t.Name == stmt.Relation.Relname {
			sourceTable = t
			break
		}
	}

	if sourceTable == nil {
		return fmt.Errorf("sourceTable could not be found in schema")
	}

	for _, cmd := range stmt.Cmds {
		node, ok := cmd.Node.(*pgQuery.Node_AlterTableCmd)
		if !ok {
			continue
		}

		if node.AlterTableCmd.Subtype != pgQuery.AlterTableType_AT_AddConstraint {
			continue
		}

		constraint, ok := node.AlterTableCmd.Def.Node.(*pgQuery.Node_Constraint)
		if !ok {
			continue
		}

		if constraint.Constraint.Contype == pgQuery.ConstrType_CONSTR_PRIMARY {
			for _, name := range constraintColumns(constraint.Constraint.Keys) {
				if col := sourceTable.findColumn(name); col != nil {
					col.Constraints = append(col.Constraints, "primary")
				}
			}
			continue
		}

		if constraint.Constraint.Contype == pgQuery.ConstrType_CONSTR_UNIQUE {
			sourceTable.addUniqueConstraint(constraintColumns(constraint.Constraint.Keys))
			continue
		}

		if constraint.Constraint.Contype != pgQuery.ConstrType_CONSTR_FOREIGN {
			continue
		}

		fk := &ForeignReference{Table: constraint.Constraint.Pktable.Relname}

		for _, pkattr := range constraint.Constraint.PkAttrs {
			if node, ok := pkattr.Node.(*pgQuery.Node_String_); ok {
				fk.Column = node.String_.Sval
			}
		}

		for _, fkattr := range constraint.Constraint.FkAttrs {
			node, ok := fkattr.Node.(*pgQuery.Node_String_)
			if !ok {
				continue
			}
			if col := sourceTable.findColumn(node.String_.Sval); col != nil {
				col.ForeignKeyReferences = append(col.ForeignKeyReferences, fk)
			}
		}
	}

	return nil
}

func toTable(stmt *pgQuery.Node_CreateStmt) *Table {
	table := &Table{Name: stmt.CreateStmt.Relation.Relname}
	var pendingUniques [][]string

	for _, columnNode := range stmt.CreateStmt.TableElts {
		if colDef, ok := columnNode.Node.(*pgQuery.Node_ColumnDef); ok {
			table.Columns = append(table.Columns, generateColumnProperties(colDef.ColumnDef))
			continue
		}

		constraint, ok := columnNode.Node.(*pgQuery.Node_Constraint)
		if !ok || constraint.Constraint.Contype != pgQuery.ConstrType_CONSTR_UNIQUE {
			continue
		}
		pendingUniques = append(pendingUniques, constraintColumns(constraint.Constraint.Keys))
	}

	for _, cols := range pendingUniques {
		table.addUniqueConstraint(cols)
	}

	return table
}

func generateColumnProperties(columnDefinition *pgQuery.ColumnDef) *Column {
	column := &Column{
		Name: columnDefinition.Colname,
	}

	for _, node := range columnDefinition.TypeName.Names {
		stringNode, ok := node.Node.(*pgQuery.Node_String_)
		if !ok {
			fmt.Printf("unknown name node %v\n", stringNode)
			continue
		}

		column.Type = stringNode.String_.Sval

		for _, mod := range columnDefinition.TypeName.Typmods {
			aConst, ok := mod.Node.(*pgQuery.Node_AConst)
			if !ok {
				continue
			}

			integer, ok := aConst.AConst.Val.(*pgQuery.A_Const_Ival)
			if !ok {
				continue
			}

			column.Length = int(integer.Ival.GetIval())
		}

		for _, constraint := range columnDefinition.Constraints {
			nodeConstraint, ok := constraint.Node.(*pgQuery.Node_Constraint)
			if !ok {
				continue
			}

			switch nodeConstraint.Constraint.Contype {
			case pgQuery.ConstrType_CONSTR_PRIMARY:
				column.Constraints = append(column.Constraints, "primary")
			case pgQuery.ConstrType_CONSTR_UNIQUE:
				if !slices.Contains(column.Constraints, "unique") {
					column.Constraints = append(column.Constraints, "unique")
				}
			case pgQuery.ConstrType_CONSTR_FOREIGN:
				foreignReference := &ForeignReference{
					Table: nodeConstraint.Constraint.Pktable.Relname,
				}

				var found bool
				for _, pkattr := range nodeConstraint.Constraint.PkAttrs {
					node, ok := pkattr.Node.(*pgQuery.Node_String_)
					if !ok {
						continue
					}

					for _, fkr := range column.ForeignKeyReferences {
						if fkr.Table == nodeConstraint.Constraint.Pktable.Relname && fkr.Column == node.String_.Sval {
							found = true
						}
					}

					foreignReference.Column = node.String_.Sval
				}

				if !found {
					column.ForeignKeyReferences = append(column.ForeignKeyReferences, foreignReference)
				}
			case pgQuery.ConstrType_CONSTR_NOTNULL:
				if !slices.Contains(column.Constraints, "not null") {
					column.Constraints = append(column.Constraints, "not null")
				}
			}
		}
	}

	return column
}

func (t *Table) findColumn(name string) *Column {
	for _, col := range t.Columns {
		if col.Name == name {
			return col
		}
	}
	return nil
}

func (t *Table) addUniqueConstraint(columns []string) {
	if len(columns) == 1 {
		if col := t.findColumn(columns[0]); col != nil {
			if !slices.Contains(col.Constraints, "unique") {
				col.Constraints = append(col.Constraints, "unique")
			}
		}
	} else if len(columns) > 1 && !slices.ContainsFunc(t.UniqueConstraints, func(e []string) bool { return slices.Equal(e, columns) }) {
		t.UniqueConstraints = append(t.UniqueConstraints, columns)
	}
}

func constraintColumns(keys []*pgQuery.Node) []string {
	columns := make([]string, 0, len(keys))
	for _, key := range keys {
		node, ok := key.Node.(*pgQuery.Node_String_)
		if !ok {
			continue
		}

		columns = append(columns, node.String_.Sval)
	}

	return columns
}

