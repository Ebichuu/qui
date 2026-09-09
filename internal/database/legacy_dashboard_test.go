// Copyright (c) 2026, s0up and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package database

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"modernc.org/sqlite"
)

// Migration files through SQLite 090 / Postgres 092 are identical in the
// v1.28.0 upstream and bfe94b5e custom trees. The four testdata files are exact
// copies of the custom tail, not reconstructed from the upgrade implementation.
func legacyDashboardFixture(t *testing.T, dialect Dialect, variant string) (*DB, OpenOptions) {
	t.Helper()
	opts := OpenOptions{Engine: string(dialect)}
	var conn *sql.DB
	var err error
	if dialect == DialectPostgres {
		_, opts.PostgresDSN = openPostgresTestSchema(t)
		conn, err = sql.Open("pgx", opts.PostgresDSN)
	} else {
		opts.SQLitePath = filepath.Join(t.TempDir(), "legacy.db")
		conn, err = sql.Open("sqlite", opts.SQLitePath)
	}
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	db := &DB{writerConn: conn, dialect: dialect}
	if variant == "empty" {
		return db, opts
	}
	ctx := t.Context()
	id := "INTEGER PRIMARY KEY AUTOINCREMENT"
	if dialect == DialectPostgres {
		id = "BIGSERIAL PRIMARY KEY"
	}
	_, err = conn.ExecContext(ctx, "CREATE TABLE migrations (id "+id+", filename TEXT NOT NULL UNIQUE, applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)")
	require.NoError(t, err)
	files, cutoff := listMigrationFiles(t), "091"
	if dialect == DialectPostgres {
		files, cutoff = listPostgresMigrationFiles(t), "093"
	}
	var common []string
	for _, file := range files {
		if variant == "upstream" || file[:3] < cutoff {
			common = append(common, file)
		}
	}
	if dialect == DialectSQLite {
		require.NoError(t, db.applyAllMigrations(ctx, common))
	} else {
		for _, file := range common {
			body, err := postgresMigrationsFS.ReadFile("postgres_migrations/" + file)
			require.NoError(t, err)
			_, err = conn.ExecContext(ctx, string(body))
			require.NoError(t, err)
			_, err = conn.ExecContext(ctx, "INSERT INTO migrations (filename) VALUES ($1)", file)
			require.NoError(t, err)
		}
	}
	if variant != "upstream" {
		names := []string{"091_add_daily_transfer_stats.sql", "092_add_server_stats_sort.sql"}
		if dialect == DialectPostgres {
			names = []string{"093_add_daily_transfer_stats.sql", "094_add_server_stats_sort.sql"}
		}
		if variant == "daily_only" {
			names = names[:1]
		}
		for _, file := range names {
			body, err := os.ReadFile(filepath.Join("testdata", "legacy_dashboard", string(dialect), file))
			require.NoError(t, err)
			_, err = conn.ExecContext(ctx, string(body))
			require.NoError(t, err)
			_, err = conn.ExecContext(ctx, db.bindQuery("INSERT INTO migrations (filename) VALUES (?)"), file)
			require.NoError(t, err)
		}
	}
	for _, statement := range []string{
		`INSERT INTO string_pool (id, value) VALUES (10001, 'c01-instance'), (10002, 'http://127.0.0.1:1'), (10003, 'c01-user')`,
		`INSERT INTO instances (id, name_id, host_id, username_id, password_encrypted, is_active) VALUES (10001, 10001, 10002, 10003, 'synthetic', 0)`,
		`INSERT INTO automations (instance_id, name, tracker_pattern, conditions, enabled, sort_order, dry_run) VALUES (10001, 'c01-rule', '*', '{"delete":{"enabled":true,"conditionDurationSeconds":7200}}', 0, 7, 1)`,
		`INSERT INTO automation_activity (instance_id, hash, action, outcome, details) VALUES (10001, '0123456789012345678901234567890123456789', 'delete', 'skipped', '{"reason":"synthetic-history"}')`,
		`INSERT INTO dashboard_settings (user_id, section_order) VALUES (1, '["instances","server-stats"]')`,
	} {
		_, err := conn.ExecContext(ctx, statement)
		require.NoError(t, err)
	}
	if variant != "upstream" {
		_, err = conn.ExecContext(ctx, `INSERT INTO instance_daily_transfer_stats VALUES
			(10001, '2026-01-01', 5000000000, 9000000000, 6000000000, 10000000000, '2026-01-01T23:59:00Z'),
			(10001, '2026-01-02', 120, 340, 6000000120, 10000000340, '2026-01-02T00:01:00Z')`)
		require.NoError(t, err)
	}
	if variant == "custom" {
		_, err = conn.ExecContext(ctx, `UPDATE dashboard_settings SET server_stats_sort_column = 'uploadedToday', server_stats_sort_direction = 'desc'`)
		require.NoError(t, err)
	}
	return db, opts
}

func legacySnapshot(t *testing.T, q legacySchemaQuerier, query string) [][]string {
	t.Helper()
	rows, err := q.QueryContext(t.Context(), query)
	require.NoError(t, err)
	defer rows.Close()
	columns, err := rows.Columns()
	require.NoError(t, err)
	var snapshot [][]string
	for rows.Next() {
		values := make([]string, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		require.NoError(t, rows.Scan(dest...))
		snapshot = append(snapshot, values)
	}
	require.NoError(t, rows.Err())
	return snapshot
}

func TestLegacyDashboardUpgrade(t *testing.T) {
	for _, dialect := range []Dialect{DialectSQLite, DialectPostgres} {
		t.Run(string(dialect), func(t *testing.T) {
			for _, variant := range []string{"empty", "upstream", "daily_only", "custom"} {
				t.Run(variant, func(t *testing.T) {
					legacy, opts := legacyDashboardFixture(t, dialect, variant)
					queries := []string{}
					if variant != "empty" {
						queries = append(queries,
							`SELECT name, conditions, CAST(enabled AS TEXT), CAST(sort_order AS TEXT), CAST(dry_run AS TEXT) FROM automations ORDER BY id`,
							`SELECT hash, action, outcome, details, CAST(created_at AS TEXT) FROM automation_activity ORDER BY id`,
							`SELECT section_order FROM dashboard_settings ORDER BY id`,
						)
					}
					if variant == "daily_only" || variant == "custom" {
						queries = append(queries, `SELECT CAST(instance_id AS TEXT), day, CAST(downloaded AS TEXT), CAST(uploaded AS TEXT), CAST(last_alltime_dl AS TEXT), CAST(last_alltime_ul AS TEXT), last_sample_at FROM instance_daily_transfer_stats ORDER BY instance_id, day`)
					}
					if variant == "custom" {
						queries = append(queries, `SELECT server_stats_sort_column, server_stats_sort_direction FROM dashboard_settings`)
					}
					before := make([][][]string, len(queries))
					for i, query := range queries {
						before[i] = legacySnapshot(t, legacy.writerConn, query)
					}
					var history [][]string
					if variant != "empty" {
						history = legacySnapshot(t, legacy.writerConn, `SELECT filename, CAST(applied_at AS TEXT) FROM migrations ORDER BY filename`)
					}
					for range 2 {
						upgraded, err := Open(opts)
						require.NoError(t, err)
						for i, query := range queries {
							require.Equal(t, before[i], legacySnapshot(t, upgraded.Conn(), query))
						}
						for _, row := range history {
							var timestamp string
							require.NoError(t, upgraded.QueryRowContext(t.Context(), `SELECT CAST(applied_at AS TEXT) FROM migrations WHERE filename = ?`, row[0]).Scan(&timestamp))
							require.Equal(t, row[1], timestamp)
						}
						// The colliding upstream migration is required, not skipped.
						var pools int
						require.NoError(t, upgraded.QueryRowContext(t.Context(), `SELECT count(*) FROM cross_seed_partial_pools`).Scan(&pools))
						require.NoError(t, upgraded.Close())
					}
				})
			}
		})
	}
}

func TestLegacyDashboardRejectsMismatch(t *testing.T) {
	for _, dialect := range []Dialect{DialectSQLite, DialectPostgres} {
		t.Run(string(dialect), func(t *testing.T) {
			cases := map[string]string{
				"missing_table":      `DROP TABLE instance_daily_transfer_stats`,
				"unrecorded_table":   `DELETE FROM migrations WHERE filename LIKE '%_add_daily_transfer_stats.sql'`,
				"unrecorded_sorting": `DELETE FROM migrations WHERE filename LIKE '%_add_server_stats_sort.sql'`,
				"missing_column":     `ALTER TABLE dashboard_settings DROP COLUMN server_stats_sort_direction`,
				"missing_day_index":  `DROP INDEX idx_daily_transfer_stats_day`,
				"wrong_sort_default": `ALTER TABLE dashboard_settings DROP COLUMN server_stats_sort_direction; ALTER TABLE dashboard_settings ADD COLUMN server_stats_sort_direction TEXT NOT NULL DEFAULT 'desc'`,
				"wrong_counter_type": `ALTER TABLE instance_daily_transfer_stats DROP COLUMN uploaded; ALTER TABLE instance_daily_transfer_stats ADD COLUMN uploaded TEXT NOT NULL DEFAULT '0'`,
				"wrong_day_index":    `DROP INDEX idx_daily_transfer_stats_day; CREATE INDEX idx_daily_transfer_stats_day ON instance_daily_transfer_stats(uploaded)`,
			}
			dailyFile := "091_add_daily_transfer_stats.sql"
			if dialect == DialectPostgres {
				dailyFile = "093_add_daily_transfer_stats.sql"
			}
			body, err := os.ReadFile(filepath.Join("testdata", "legacy_dashboard", string(dialect), dailyFile))
			require.NoError(t, err)
			for name, replacement := range map[string][2]string{
				"wrong_primary_key": {"PRIMARY KEY (instance_id, day)", "PRIMARY KEY (instance_id)"},
				"wrong_foreign_key": {"ON DELETE CASCADE", "ON DELETE RESTRICT"},
				"nullable_sample":   {"last_sample_at TEXT NOT NULL", "last_sample_at TEXT"},
			} {
				cases[name] = "DROP TABLE instance_daily_transfer_stats; " + strings.ReplaceAll(string(body), replacement[0], replacement[1])
			}
			for name, mutation := range cases {
				t.Run(name, func(t *testing.T) {
					legacy, opts := legacyDashboardFixture(t, dialect, "custom")
					_, err := legacy.writerConn.ExecContext(t.Context(), mutation)
					require.NoError(t, err)
					history := legacySnapshot(t, legacy.writerConn, `SELECT filename, CAST(applied_at AS TEXT) FROM migrations ORDER BY filename`)
					for range 2 {
						_, err = Open(opts)
						require.ErrorContains(t, err, "legacy dashboard schema does not match migration history")
						require.Equal(t, history, legacySnapshot(t, legacy.writerConn, `SELECT filename, CAST(applied_at AS TEXT) FROM migrations ORDER BY filename`))
					}
				})
			}
		})
	}
}

func TestLegacyDashboardMigrationRollback(t *testing.T) {
	for _, dialect := range []Dialect{DialectSQLite, DialectPostgres} {
		t.Run(string(dialect), func(t *testing.T) {
			legacy, opts := legacyDashboardFixture(t, dialect, "custom")
			ctx := t.Context()
			trigger := `CREATE TRIGGER c01_fail BEFORE INSERT ON migrations WHEN NEW.filename = '093_drop_unused_timestamp_indexes.sql' BEGIN SELECT RAISE(ABORT, 'c01 simulated failure'); END`
			remove := `DROP TRIGGER c01_fail`
			if dialect == DialectPostgres {
				trigger = `CREATE FUNCTION c01_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.filename = '094_drop_unused_timestamp_indexes.sql' THEN RAISE EXCEPTION 'c01 simulated failure'; END IF; RETURN NEW; END $$;
					CREATE TRIGGER c01_fail BEFORE INSERT ON migrations FOR EACH ROW EXECUTE FUNCTION c01_fail()`
				remove = `DROP TRIGGER c01_fail ON migrations; DROP FUNCTION c01_fail()`
			}
			_, err := legacy.writerConn.ExecContext(ctx, trigger)
			require.NoError(t, err)
			history := legacySnapshot(t, legacy.writerConn, `SELECT filename, CAST(applied_at AS TEXT) FROM migrations ORDER BY filename`)
			_, err = Open(opts)
			require.ErrorContains(t, err, "c01 simulated failure")
			require.Equal(t, history, legacySnapshot(t, legacy.writerConn, `SELECT filename, CAST(applied_at AS TEXT) FROM migrations ORDER BY filename`))
			columns, err := legacy.legacyTableColumns(ctx, legacy.writerConn, "cross_seed_partial_pools")
			require.NoError(t, err)
			require.Empty(t, columns, "earlier DDL in the failed batch must roll back")
			_, err = legacy.writerConn.ExecContext(ctx, remove)
			require.NoError(t, err)
			upgraded, err := Open(opts)
			require.NoError(t, err)
			require.NoError(t, upgraded.Close())
		})
	}
}

func TestLegacyDashboardSQLiteCrash(t *testing.T) {
	legacy, opts := legacyDashboardFixture(t, DialectSQLite, "custom")
	_, err := legacy.writerConn.ExecContext(t.Context(), `CREATE TRIGGER c01_pause AFTER INSERT ON migrations WHEN NEW.filename = '091_add_cross_seed_partial_pools.sql' BEGIN SELECT c01_pause(); END`)
	require.NoError(t, err)
	history := legacySnapshot(t, legacy.writerConn, `SELECT filename, CAST(applied_at AS TEXT) FROM migrations ORDER BY filename`)
	ready := filepath.Join(t.TempDir(), "ready")
	executable, err := os.Executable()
	require.NoError(t, err)
	command := exec.CommandContext(t.Context(), executable, "-test.run=^TestLegacyDashboardCrashHelper$")
	command.Env = append(os.Environ(), "C01_CRASH_DB="+opts.SQLitePath, "C01_CRASH_READY="+ready)
	command.Stdout, command.Stderr = io.Discard, io.Discard
	require.NoError(t, command.Start())
	t.Cleanup(func() { _ = command.Process.Kill() })
	require.Eventually(t, func() bool { _, err := os.Stat(ready); return err == nil }, 15*time.Second, 10*time.Millisecond)
	require.NoError(t, command.Process.Kill())
	require.Error(t, command.Wait())
	require.Equal(t, history, legacySnapshot(t, legacy.writerConn, `SELECT filename, CAST(applied_at AS TEXT) FROM migrations ORDER BY filename`))
	columns, err := legacy.legacyTableColumns(t.Context(), legacy.writerConn, "cross_seed_partial_pools")
	require.NoError(t, err)
	require.Empty(t, columns)
	_, err = legacy.writerConn.ExecContext(t.Context(), `DROP TRIGGER c01_pause`)
	require.NoError(t, err)
	upgraded, err := Open(opts)
	require.NoError(t, err)
	require.NoError(t, upgraded.Close())
}

func TestLegacyDashboardPostgresDisconnect(t *testing.T) {
	legacy, opts := legacyDashboardFixture(t, DialectPostgres, "custom")
	ctx := t.Context()
	_, err := legacy.writerConn.ExecContext(ctx, `
		CREATE FUNCTION c01_disconnect() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.filename = '093_add_cross_seed_partial_pools.sql' THEN
				PERFORM pg_terminate_backend(pg_backend_pid());
			END IF;
			RETURN NEW;
		END $$;
		CREATE TRIGGER c01_disconnect AFTER INSERT ON migrations FOR EACH ROW EXECUTE FUNCTION c01_disconnect()`)
	require.NoError(t, err)
	history := legacySnapshot(t, legacy.writerConn, `SELECT filename, CAST(applied_at AS TEXT) FROM migrations ORDER BY filename`)
	_, err = Open(opts)
	require.ErrorContains(t, err, "terminating connection")
	require.Equal(t, history, legacySnapshot(t, legacy.writerConn, `SELECT filename, CAST(applied_at AS TEXT) FROM migrations ORDER BY filename`))
	columns, err := legacy.legacyTableColumns(ctx, legacy.writerConn, "cross_seed_partial_pools")
	require.NoError(t, err)
	require.Empty(t, columns)
	_, err = legacy.writerConn.ExecContext(ctx, `DROP TRIGGER c01_disconnect ON migrations; DROP FUNCTION c01_disconnect()`)
	require.NoError(t, err)
	upgraded, err := Open(opts)
	require.NoError(t, err)
	require.NoError(t, upgraded.Close())
}

func TestLegacyDashboardCrashHelper(t *testing.T) {
	dbPath := os.Getenv("C01_CRASH_DB")
	if dbPath == "" {
		t.Skip("subprocess helper")
	}
	err := sqlite.RegisterScalarFunction("c01_pause", 0, func(_ *sqlite.FunctionContext, _ []driver.Value) (driver.Value, error) {
		if err := os.WriteFile(os.Getenv("C01_CRASH_READY"), []byte("ready"), 0o600); err != nil {
			return nil, err
		}
		time.Sleep(30 * time.Second)
		return nil, errors.New("parent did not terminate the migration process")
	})
	require.NoError(t, err)
	_, err = New(dbPath)
	if err != nil && !strings.Contains(err.Error(), "parent did not terminate") {
		t.Fatal(err)
	}
}
