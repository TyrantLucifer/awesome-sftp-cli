package migration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
)

const schemaContractMagic = "amsftp-schema-contract-v1\x00"

type schemaRow struct {
	typeName  string
	name      string
	tableName string
	sql       *string
}

type tableManifest struct {
	schema       string
	name         string
	typeName     string
	columns      int64
	withoutRowID int64
	strict       int64
	xinfo        []tableXInfo
	foreignKeys  []foreignKeyInfo
	indexes      []indexManifest
}

type tableXInfo struct {
	cid          int64
	name         string
	typeName     string
	notNull      int64
	defaultValue *string
	primaryKey   int64
	hidden       int64
}

type foreignKeyInfo struct {
	id       int64
	sequence int64
	table    string
	from     string
	to       *string
	onUpdate string
	onDelete string
	match    string
}

type indexManifest struct {
	sequence int64
	name     string
	unique   int64
	origin   string
	partial  int64
	xinfo    []indexXInfo
}

type indexXInfo struct {
	sequence  int64
	cid       int64
	name      *string
	desc      int64
	collation *string
	key       int64
}

// BuildSchemaContract encodes the complete main schema and its PRAGMA metadata
// using the immutable schema-contract-v1 binary format.
func BuildSchemaContract(ctx context.Context, connection *sql.Conn, head uint64) ([]byte, error) {
	if connection == nil {
		return nil, fmt.Errorf("build schema contract: nil connection")
	}
	if head == 0 || head > maxMigrationVersion {
		return nil, fmt.Errorf("build schema contract: invalid head %d", head)
	}
	if err := checkMigrationConnection(ctx, connection); err != nil {
		return nil, fmt.Errorf("build schema contract: %w", err)
	}
	application, err := queryPragmaInt(ctx, connection, "application_id")
	if err != nil {
		return nil, err
	}
	userVersion, err := queryPragmaInt(ctx, connection, "user_version")
	if err != nil {
		return nil, err
	}
	schema, err := loadSchemaRows(ctx, connection)
	if err != nil {
		return nil, err
	}
	tables, err := loadTableManifests(ctx, connection)
	if err != nil {
		return nil, err
	}

	var output bytes.Buffer
	encoder := contractEncoder{writer: &output}
	encoder.raw([]byte(schemaContractMagic))
	encoder.integer(application)
	encoder.integer(userVersion)
	// The head is already bounded by SQLite's signed-positive range.
	encoder.integer(int64(head)) //nolint:gosec
	encoder.tag(0x10)
	encoder.count(len(schema))
	for _, row := range schema {
		encoder.tag(0x11)
		encoder.text(row.typeName)
		encoder.text(row.name)
		encoder.text(row.tableName)
		encoder.nullableText(row.sql)
	}
	encoder.tag(0x20)
	encoder.count(len(tables))
	for _, table := range tables {
		encoder.tag(0x21)
		encoder.text(table.schema)
		encoder.text(table.name)
		encoder.text(table.typeName)
		encoder.integer(table.columns)
		encoder.integer(table.withoutRowID)
		encoder.integer(table.strict)
		encoder.tag(0x22)
		encoder.count(len(table.xinfo))
		for _, column := range table.xinfo {
			encoder.tag(0x23)
			encoder.integer(column.cid)
			encoder.text(column.name)
			encoder.text(column.typeName)
			encoder.integer(column.notNull)
			encoder.nullableText(column.defaultValue)
			encoder.integer(column.primaryKey)
			encoder.integer(column.hidden)
		}
		encoder.tag(0x24)
		encoder.count(len(table.foreignKeys))
		for _, foreignKey := range table.foreignKeys {
			encoder.tag(0x25)
			encoder.integer(foreignKey.id)
			encoder.integer(foreignKey.sequence)
			encoder.text(foreignKey.table)
			encoder.text(foreignKey.from)
			encoder.nullableText(foreignKey.to)
			encoder.text(foreignKey.onUpdate)
			encoder.text(foreignKey.onDelete)
			encoder.text(foreignKey.match)
		}
		encoder.tag(0x26)
		encoder.count(len(table.indexes))
		for _, index := range table.indexes {
			encoder.tag(0x27)
			encoder.integer(index.sequence)
			encoder.text(index.name)
			encoder.integer(index.unique)
			encoder.text(index.origin)
			encoder.integer(index.partial)
			encoder.tag(0x28)
			encoder.count(len(index.xinfo))
			for _, info := range index.xinfo {
				encoder.tag(0x29)
				encoder.integer(info.sequence)
				encoder.integer(info.cid)
				encoder.nullableText(info.name)
				encoder.integer(info.desc)
				encoder.nullableText(info.collation)
				encoder.integer(info.key)
			}
		}
	}
	if encoder.err != nil {
		return nil, fmt.Errorf("build schema contract: encode: %w", encoder.err)
	}
	return output.Bytes(), nil
}

func queryPragmaInt(ctx context.Context, connection *sql.Conn, name string) (int64, error) {
	var value int64
	if err := connection.QueryRowContext(ctx, "PRAGMA "+name).Scan(&value); err != nil {
		return 0, fmt.Errorf("build schema contract: read PRAGMA %s: %w", name, err)
	}
	return value, nil
}

func loadSchemaRows(ctx context.Context, connection *sql.Conn) ([]schemaRow, error) {
	rows, err := connection.QueryContext(ctx, "SELECT type, name, tbl_name, sql FROM main.sqlite_schema")
	if err != nil {
		return nil, fmt.Errorf("build schema contract: query sqlite_schema: %w", err)
	}
	defer rows.Close()
	var result []schemaRow
	for rows.Next() {
		var row schemaRow
		var statement sql.NullString
		if err := rows.Scan(&row.typeName, &row.name, &row.tableName, &statement); err != nil {
			return nil, fmt.Errorf("build schema contract: scan sqlite_schema: %w", err)
		}
		row.sql = nullableString(statement)
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("build schema contract: iterate sqlite_schema: %w", err)
	}
	sort.Slice(result, func(left, right int) bool {
		return compareStrings(
			[]string{result[left].typeName, result[left].name, result[left].tableName},
			[]string{result[right].typeName, result[right].name, result[right].tableName},
		) < 0
	})
	return result, nil
}

func loadTableManifests(ctx context.Context, connection *sql.Conn) ([]tableManifest, error) {
	rows, err := connection.QueryContext(ctx, "PRAGMA main.table_list")
	if err != nil {
		return nil, fmt.Errorf("build schema contract: query table_list: %w", err)
	}
	var tables []tableManifest
	for rows.Next() {
		var table tableManifest
		if err := rows.Scan(&table.schema, &table.name, &table.typeName, &table.columns, &table.withoutRowID, &table.strict); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("build schema contract: scan table_list: %w", err)
		}
		if table.schema == "main" {
			tables = append(tables, table)
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("build schema contract: iterate table_list: %w", err)
	}
	sort.Slice(tables, func(left, right int) bool {
		return compareStrings([]string{tables[left].name, tables[left].typeName}, []string{tables[right].name, tables[right].typeName}) < 0
	})
	for index := range tables {
		table := &tables[index]
		table.xinfo, err = loadTableXInfo(ctx, connection, table.name)
		if err != nil {
			return nil, err
		}
		table.foreignKeys, err = loadForeignKeys(ctx, connection, table.name)
		if err != nil {
			return nil, err
		}
		table.indexes, err = loadIndexes(ctx, connection, table.name)
		if err != nil {
			return nil, err
		}
	}
	return tables, nil
}

func loadTableXInfo(ctx context.Context, connection *sql.Conn, table string) ([]tableXInfo, error) {
	rows, err := connection.QueryContext(ctx, "SELECT cid, name, type, \"notnull\", dflt_value, pk, hidden FROM pragma_table_xinfo(?, 'main')", table)
	if err != nil {
		return nil, fmt.Errorf("build schema contract: query table_xinfo for %q: %w", table, err)
	}
	defer rows.Close()
	var result []tableXInfo
	for rows.Next() {
		var info tableXInfo
		var defaultValue sql.NullString
		if err := rows.Scan(&info.cid, &info.name, &info.typeName, &info.notNull, &defaultValue, &info.primaryKey, &info.hidden); err != nil {
			return nil, fmt.Errorf("build schema contract: scan table_xinfo for %q: %w", table, err)
		}
		info.defaultValue = nullableString(defaultValue)
		result = append(result, info)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("build schema contract: iterate table_xinfo for %q: %w", table, err)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].cid < result[right].cid })
	return result, nil
}

func loadForeignKeys(ctx context.Context, connection *sql.Conn, table string) ([]foreignKeyInfo, error) {
	rows, err := connection.QueryContext(ctx, "SELECT id, seq, \"table\", \"from\", \"to\", on_update, on_delete, match FROM pragma_foreign_key_list(?, 'main')", table)
	if err != nil {
		return nil, fmt.Errorf("build schema contract: query foreign_key_list for %q: %w", table, err)
	}
	defer rows.Close()
	var result []foreignKeyInfo
	for rows.Next() {
		var info foreignKeyInfo
		var to sql.NullString
		if err := rows.Scan(&info.id, &info.sequence, &info.table, &info.from, &to, &info.onUpdate, &info.onDelete, &info.match); err != nil {
			return nil, fmt.Errorf("build schema contract: scan foreign_key_list for %q: %w", table, err)
		}
		info.to = nullableString(to)
		result = append(result, info)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("build schema contract: iterate foreign_key_list for %q: %w", table, err)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].id != result[right].id {
			return result[left].id < result[right].id
		}
		return result[left].sequence < result[right].sequence
	})
	return result, nil
}

func loadIndexes(ctx context.Context, connection *sql.Conn, table string) ([]indexManifest, error) {
	rows, err := connection.QueryContext(ctx, "SELECT seq, name, \"unique\", origin, partial FROM pragma_index_list(?, 'main')", table)
	if err != nil {
		return nil, fmt.Errorf("build schema contract: query index_list for %q: %w", table, err)
	}
	var result []indexManifest
	for rows.Next() {
		var index indexManifest
		if err := rows.Scan(&index.sequence, &index.name, &index.unique, &index.origin, &index.partial); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("build schema contract: scan index_list for %q: %w", table, err)
		}
		result = append(result, index)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("build schema contract: iterate index_list for %q: %w", table, err)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].name < result[right].name })
	for position := range result {
		result[position].xinfo, err = loadIndexXInfo(ctx, connection, result[position].name)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func loadIndexXInfo(ctx context.Context, connection *sql.Conn, index string) ([]indexXInfo, error) {
	rows, err := connection.QueryContext(ctx, "SELECT seqno, cid, name, desc, coll, key FROM pragma_index_xinfo(?, 'main')", index)
	if err != nil {
		return nil, fmt.Errorf("build schema contract: query index_xinfo for %q: %w", index, err)
	}
	defer rows.Close()
	var result []indexXInfo
	for rows.Next() {
		var info indexXInfo
		var name, collation sql.NullString
		if err := rows.Scan(&info.sequence, &info.cid, &name, &info.desc, &collation, &info.key); err != nil {
			return nil, fmt.Errorf("build schema contract: scan index_xinfo for %q: %w", index, err)
		}
		info.name = nullableString(name)
		info.collation = nullableString(collation)
		result = append(result, info)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("build schema contract: iterate index_xinfo for %q: %w", index, err)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].sequence < result[right].sequence })
	return result, nil
}

func nullableString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	copy := value.String
	return &copy
}

func compareStrings(left, right []string) int {
	for index := range left {
		if comparison := bytes.Compare([]byte(left[index]), []byte(right[index])); comparison != 0 {
			return comparison
		}
	}
	return 0
}

type contractEncoder struct {
	writer io.Writer
	err    error
}

func (encoder *contractEncoder) raw(value []byte) {
	if encoder.err != nil {
		return
	}
	_, encoder.err = encoder.writer.Write(value)
}

func (encoder *contractEncoder) tag(value byte) {
	encoder.raw([]byte{value})
}

func (encoder *contractEncoder) count(value int) {
	if value < 0 {
		encoder.err = fmt.Errorf("negative collection count %d", value)
		return
	}
	// Collection lengths originate from in-memory slices and fit uint64.
	encoder.unsigned(uint64(value)) //nolint:gosec
}

func (encoder *contractEncoder) unsigned(value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	encoder.raw(encoded[:])
}

func (encoder *contractEncoder) integer(value int64) {
	encoder.tag(0x02)
	// The frozen format encodes signed integers as their two's-complement bits.
	encoder.unsigned(uint64(value)) //nolint:gosec
}

func (encoder *contractEncoder) text(value string) {
	encoder.tag(0x01)
	encoder.count(len(value))
	encoder.raw([]byte(value))
}

func (encoder *contractEncoder) nullableText(value *string) {
	if value == nil {
		encoder.tag(0x00)
		return
	}
	encoder.text(*value)
}
