// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type legacySchemaQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type legacyColumn struct {
	name         string
	dataType     string
	notNull      bool
	defaultValue string
}

// validateLegacyDashboard runs before any migration history rewrites or DDL.
// Legacy custom migrations share numeric prefixes with unrelated upstream
// migrations. Keep their full filenames and data intact; upstream migrations
// must still run. A filename alone is not sufficient evidence of the schema.
func (db *DB) validateLegacyDashboard(ctx context.Context, q legacySchemaQuerier) error {
	dailyFile, sortFile := "091_add_daily_transfer_stats.sql", "092_add_server_stats_sort.sql"
	integerType := "integer"
	if db.dialect == DialectPostgres {
		dailyFile, sortFile = "093_add_daily_transfer_stats.sql", "094_add_server_stats_sort.sql"
		integerType = "bigint"
	}

	var dailyRecorded, sortRecorded bool
	if err := q.QueryRowContext(ctx, db.bindQuery(`SELECT
		EXISTS(SELECT 1 FROM migrations WHERE filename = ?),
		EXISTS(SELECT 1 FROM migrations WHERE filename = ?)`), dailyFile, sortFile).
		Scan(&dailyRecorded, &sortRecorded); err != nil {
		return fmt.Errorf("inspect legacy dashboard migration history: %w", err)
	}
	daily, err := db.legacyTableColumns(ctx, q, "instance_daily_transfer_stats")
	if err != nil {
		return err
	}
	dashboard, err := db.legacyTableColumns(ctx, q, "dashboard_settings")
	if err != nil {
		return err
	}
	var sorting []legacyColumn
	for _, column := range dashboard {
		if strings.HasPrefix(column.name, "server_stats_sort_") {
			sorting = append(sorting, column)
		}
	}

	mismatch := func(detail string) error {
		return fmt.Errorf("legacy dashboard schema does not match migration history: %s; restore a verified backup or inspect the schema before retrying", detail)
	}
	if dailyRecorded != (len(daily) > 0) || sortRecorded != (len(sorting) > 0) {
		return mismatch("daily transfer or server sort objects disagree with recorded filenames")
	}
	if sortRecorded && !dailyRecorded {
		return mismatch("server sorting exists without the preceding daily transfer migration")
	}
	if dailyRecorded {
		expected := []legacyColumn{
			{"instance_id", integerType, true, ""},
			{"day", "text", true, ""},
			{"downloaded", integerType, true, "0"},
			{"uploaded", integerType, true, "0"},
			{"last_alltime_dl", integerType, true, "0"},
			{"last_alltime_ul", integerType, true, "0"},
			{"last_sample_at", "text", true, ""},
		}
		if !legacyColumnsMatch(daily, expected) {
			return mismatch("unexpected daily transfer columns, types, nullability or defaults")
		}
		valid, err := db.legacyDailyConstraints(ctx, q)
		if err != nil {
			return err
		}
		if !valid {
			return mismatch("unexpected daily transfer primary key, foreign key or day index")
		}
	}
	if sortRecorded && !legacyColumnsMatch(sorting, []legacyColumn{
		{"server_stats_sort_column", "text", true, "'instance'"},
		{"server_stats_sort_direction", "text", true, "'asc'"},
	}) {
		return mismatch("unexpected server sort columns, types, nullability or defaults")
	}
	return nil
}

func legacyColumnsMatch(actual, expected []legacyColumn) bool {
	if len(actual) != len(expected) {
		return false
	}
	for i := range expected {
		if actual[i] != expected[i] {
			return false
		}
	}
	return true
}

func (db *DB) legacyTableColumns(ctx context.Context, q legacySchemaQuerier, table string) ([]legacyColumn, error) {
	query := `SELECT name, lower(type), "notnull", coalesce(dflt_value, '') FROM pragma_table_info(?) ORDER BY cid`
	if db.dialect == DialectPostgres {
		query = `SELECT column_name, data_type, is_nullable = 'NO', coalesce(column_default, '')
			FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = $1
			ORDER BY ordinal_position`
	}
	rows, err := q.QueryContext(ctx, query, table)
	if err != nil {
		return nil, fmt.Errorf("inspect legacy table %s: %w", table, err)
	}
	defer rows.Close()
	var columns []legacyColumn
	for rows.Next() {
		var column legacyColumn
		if err := rows.Scan(&column.name, &column.dataType, &column.notNull, &column.defaultValue); err != nil {
			return nil, fmt.Errorf("inspect legacy column in %s: %w", table, err)
		}
		// PostgreSQL decorates literal defaults with their type; SQLite may
		// parenthesize them. Neither changes the legacy default's meaning.
		column.defaultValue = strings.TrimSuffix(column.defaultValue, "::text")
		column.defaultValue = strings.TrimSuffix(column.defaultValue, "::bigint")
		column.defaultValue = strings.Trim(column.defaultValue, "()")
		columns = append(columns, column)
	}
	return columns, rows.Err()
}

func (db *DB) legacyDailyConstraints(ctx context.Context, q legacySchemaQuerier) (bool, error) {
	query := `SELECT
		(SELECT count(*) = 2 AND sum(CASE WHEN (name = 'instance_id' AND pk = 1) OR (name = 'day' AND pk = 2) THEN 1 ELSE 0 END) = 2
		 FROM pragma_table_info('instance_daily_transfer_stats') WHERE pk > 0),
		(SELECT count(*) = 1 AND sum(CASE WHEN "table" = 'instances' AND "from" = 'instance_id' AND "to" = 'id' AND on_delete = 'CASCADE' AND on_update = 'NO ACTION' THEN 1 ELSE 0 END) = 1
		 FROM pragma_foreign_key_list('instance_daily_transfer_stats')),
		EXISTS(SELECT 1 FROM pragma_index_list('instance_daily_transfer_stats')
		 WHERE name = 'idx_daily_transfer_stats_day' AND "unique" = 0 AND partial = 0
		 AND (SELECT count(*) FROM pragma_index_info('idx_daily_transfer_stats_day')) = 1
		 AND (SELECT name FROM pragma_index_info('idx_daily_transfer_stats_day') WHERE seqno = 0) = 'day')`
	if db.dialect == DialectPostgres {
		query = `SELECT
			EXISTS(SELECT 1 FROM pg_constraint c JOIN pg_class t ON t.oid = c.conrelid
			 JOIN pg_namespace n ON n.oid = t.relnamespace
			 WHERE n.nspname = current_schema() AND t.relname = 'instance_daily_transfer_stats' AND c.contype = 'p'
			 AND (SELECT array_agg(a.attname::text ORDER BY k.ord) FROM unnest(c.conkey) WITH ORDINALITY k(num, ord)
			 JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.num) = ARRAY['instance_id', 'day']),
			(SELECT count(*) = 1 AND coalesce(bool_and(c.confdeltype = 'c' AND c.confupdtype = 'a'
			 AND c.confrelid = to_regclass('instances') AND c.convalidated
			 AND c.conkey = ARRAY[(SELECT attnum FROM pg_attribute WHERE attrelid = t.oid AND attname = 'instance_id')]
			 AND c.confkey = ARRAY[(SELECT attnum FROM pg_attribute WHERE attrelid = c.confrelid AND attname = 'id')]), false)
			 FROM pg_constraint c JOIN pg_class t ON t.oid = c.conrelid JOIN pg_namespace n ON n.oid = t.relnamespace
			 WHERE n.nspname = current_schema() AND t.relname = 'instance_daily_transfer_stats' AND c.contype = 'f'),
			EXISTS(SELECT 1 FROM pg_index i JOIN pg_class t ON t.oid = i.indrelid
			 JOIN pg_class idx ON idx.oid = i.indexrelid JOIN pg_namespace n ON n.oid = t.relnamespace
			 JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = i.indkey[0]
			 WHERE n.nspname = current_schema() AND t.relname = 'instance_daily_transfer_stats'
			 AND idx.relname = 'idx_daily_transfer_stats_day' AND a.attname = 'day'
			 AND i.indnatts = 1 AND NOT i.indisunique AND i.indpred IS NULL AND i.indisvalid)`
	}
	var primary, foreign, index bool
	if err := q.QueryRowContext(ctx, query).Scan(&primary, &foreign, &index); err != nil {
		return false, fmt.Errorf("inspect legacy daily transfer constraints: %w", err)
	}
	return primary && foreign && index, nil
}
