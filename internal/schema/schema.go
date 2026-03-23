package schema

import (
	"fmt"

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
			for _, key := range constraint.Constraint.Keys {
				node, ok := key.Node.(*pgQuery.Node_String_)
				if !ok {
					continue
				}

				for _, col := range sourceTable.Columns {
					if col.Name == node.String_.Sval {
						col.Constraints = append(col.Constraints, "primary")
						break
					}
				}
			}
			continue
		}

		if constraint.Constraint.Contype == pgQuery.ConstrType_CONSTR_UNIQUE {
			uniqueColumns := constraintColumns(constraint.Constraint.Keys)

			if len(uniqueColumns) == 1 {
				for _, col := range sourceTable.Columns {
					if col.Name == uniqueColumns[0] {
						col.Constraints = appendConstraint(col.Constraints, "unique")
						break
					}
				}
			} else if len(uniqueColumns) > 1 && !containsUniqueConstraint(sourceTable.UniqueConstraints, uniqueColumns) {
				sourceTable.UniqueConstraints = append(sourceTable.UniqueConstraints, uniqueColumns)
			}

			continue
		}

		if constraint.Constraint.Contype != pgQuery.ConstrType_CONSTR_FOREIGN {
			continue
		}

		foreignReference := &ForeignReference{
			Table: constraint.Constraint.Pktable.Relname,
		}

		for _, pkattr := range constraint.Constraint.PkAttrs {
			node, ok := pkattr.Node.(*pgQuery.Node_String_)
			if !ok {
				continue
			}
			foreignReference.Column = node.String_.Sval
		}

		for _, fkattr := range constraint.Constraint.FkAttrs {
			node, ok := fkattr.Node.(*pgQuery.Node_String_)
			if !ok {
				continue
			}

			var column *Column
			for _, col := range sourceTable.Columns {
				if col.Name == node.String_.Sval {
					column = col
					break
				}
			}

			if column != nil {
				column.ForeignKeyReferences = append(column.ForeignKeyReferences, foreignReference)
			}
		}
	}

	return nil
}

func toTable(stmt *pgQuery.Node_CreateStmt) *Table {
	table := &Table{
		Name: stmt.CreateStmt.Relation.Relname,
	}
	pendingSingleColumnUniques := make([]string, 0)

	for _, columnNode := range stmt.CreateStmt.TableElts {
		columnNodeDefinition, ok := columnNode.Node.(*pgQuery.Node_ColumnDef)
		if !ok {
			tableConstraintNode, ok := columnNode.Node.(*pgQuery.Node_Constraint)
			if !ok {
				continue
			}

			if tableConstraintNode.Constraint.Contype != pgQuery.ConstrType_CONSTR_UNIQUE {
				continue
			}

			uniqueColumns := constraintColumns(tableConstraintNode.Constraint.Keys)
			if len(uniqueColumns) == 1 {
				pendingSingleColumnUniques = append(pendingSingleColumnUniques, uniqueColumns[0])
			} else if len(uniqueColumns) > 1 && !containsUniqueConstraint(table.UniqueConstraints, uniqueColumns) {
				table.UniqueConstraints = append(table.UniqueConstraints, uniqueColumns)
			}

			continue
		}

		table.Columns = append(table.Columns, generateColumnProperties(columnNodeDefinition.ColumnDef))
	}

	for _, columnName := range pendingSingleColumnUniques {
		for _, col := range table.Columns {
			if col.Name == columnName {
				col.Constraints = appendConstraint(col.Constraints, "unique")
				break
			}
		}
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
				column.Constraints = appendConstraint(column.Constraints, "unique")
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
				var alreadyExists bool
				for _, c := range column.Constraints {
					if c == "not null" {
						alreadyExists = true
					}
				}

				if !alreadyExists {
					column.Constraints = append(column.Constraints, "not null")
				}
			}
		}
	}

	return column
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

func appendConstraint(constraints []string, value string) []string {
	if contains(constraints, value) {
		return constraints
	}

	return append(constraints, value)
}

func containsUniqueConstraint(uniqueConstraints [][]string, constraint []string) bool {
	for _, existing := range uniqueConstraints {
		if len(existing) != len(constraint) {
			continue
		}

		matches := true
		for i := range existing {
			if existing[i] != constraint[i] {
				matches = false
				break
			}
		}

		if matches {
			return true
		}
	}

	return false
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
