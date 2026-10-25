//go:build unit

package repository

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/service"
	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func TestChannelMonitorAdminOnlyPersistenceAndLegacyDefault(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE channel_monitors (
		id INTEGER PRIMARY KEY AUTOINCREMENT, created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL,
		name TEXT NOT NULL, provider TEXT NOT NULL, check_mode TEXT NOT NULL DEFAULT 'probe', account_id INTEGER,
		api_mode TEXT NOT NULL DEFAULT 'chat_completions', endpoint TEXT NOT NULL, api_key_encrypted TEXT NOT NULL,
		primary_model TEXT NOT NULL, extra_models TEXT NOT NULL DEFAULT '[]', group_name TEXT DEFAULT '',
		enabled BOOLEAN NOT NULL DEFAULT true, interval_seconds INTEGER NOT NULL, jitter_seconds INTEGER NOT NULL DEFAULT 0,
		last_checked_at TIMESTAMP, created_by INTEGER NOT NULL, template_id INTEGER,
		extra_headers TEXT NOT NULL DEFAULT '{}', body_override_mode TEXT NOT NULL DEFAULT 'off', body_override TEXT
	);
	INSERT INTO channel_monitors
		(created_at,updated_at,name,provider,endpoint,api_key_encrypted,primary_model,interval_seconds,created_by)
		VALUES (CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'legacy','openai','https://example.com','encrypted','model',60,1)`)
	require.NoError(t, err)
	migration, err := dbmigrations.FS.ReadFile("247_channel_monitor_admin_only.sql")
	require.NoError(t, err)
	_, err = db.Exec(strings.ReplaceAll(string(migration), "ADD COLUMN IF NOT EXISTS", "ADD COLUMN"))
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.SQLite, db)))
	repo := NewChannelMonitorRepository(client, db)
	ctx := context.Background()
	legacy, err := repo.GetByID(ctx, 1)
	require.NoError(t, err)
	require.False(t, legacy.AdminOnly)
	m := &service.ChannelMonitor{Name: "private", Provider: service.MonitorProviderOpenAI, Endpoint: "https://example.com",
		APIKey: "encrypted-secret", PrimaryModel: "model", Enabled: true, AdminOnly: true, IntervalSeconds: 60}
	require.NoError(t, repo.Create(ctx, m))
	loaded, err := repo.GetByID(ctx, m.ID)
	require.NoError(t, err)
	require.True(t, loaded.AdminOnly)
	// Scheduling continues to run private monitors; only user-facing reads hide them.
	enabled, err := repo.ListEnabled(ctx)
	require.NoError(t, err)
	require.Len(t, enabled, 2)
	loaded.AdminOnly = false
	require.NoError(t, repo.Update(ctx, loaded))
	loaded, err = repo.GetByID(ctx, m.ID)
	require.NoError(t, err)
	require.False(t, loaded.AdminOnly)
}
