package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/dockside-gg/game-panel/internal/engineclient"
	"github.com/dockside-gg/game-panel/internal/identity"
	"github.com/dockside-gg/game-panel/internal/secure"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const serverDatabaseImage = "postgres:18.4-alpine"

type ServerDatabase struct {
	ID        string    `json:"id"`
	ServerID  string    `json:"server_id"`
	Name      string    `json:"name"`
	Username  string    `json:"username"`
	Engine    string    `json:"engine"`
	Host      string    `json:"host"`
	Port      int       `json:"port"`
	Status    string    `json:"status"`
	LastError *string   `json:"last_error"`
	CreatedBy *string   `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type DatabaseProvision struct {
	Database      ServerDatabase
	Password      string
	AdminPassword string
}

type DatabaseJob struct {
	Database      ServerDatabase
	Password      string
	AdminPassword string
}

func (s *Store) PrepareDatabase(
	ctx context.Context,
	serverID, actorID, name string,
	box *secure.Box,
) (DatabaseProvision, error) {
	databaseID, err := identity.NewUUID()
	if err != nil {
		return DatabaseProvision{}, err
	}
	password, err := identity.Token(32)
	if err != nil {
		return DatabaseProvision{}, err
	}
	username := "ds_" + strings.ReplaceAll(databaseID, "-", "")[:20]
	encryptedPassword, err := box.Seal(
		password,
		[]byte("database:"+databaseID+":password"),
	)
	if err != nil {
		return DatabaseProvision{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return DatabaseProvision{}, err
	}
	defer tx.Rollback(ctx)
	var serverExists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM servers
			WHERE id = $1 AND deleted_at IS NULL
			  AND status NOT IN ('installing', 'deleting', 'failed')
		)
	`, serverID).Scan(&serverExists); err != nil {
		return DatabaseProvision{}, err
	}
	if !serverExists {
		return DatabaseProvision{}, ErrConflict
	}
	var encryptedAdmin string
	err = tx.QueryRow(ctx, `
		SELECT admin_password_encrypted
		FROM server_database_hosts
		WHERE server_id = $1
		FOR UPDATE
	`, serverID).Scan(&encryptedAdmin)
	if errors.Is(err, pgx.ErrNoRows) {
		adminPassword, err := identity.Token(48)
		if err != nil {
			return DatabaseProvision{}, err
		}
		encryptedAdmin, err = box.Seal(
			adminPassword,
			[]byte("database-host:"+serverID+":admin"),
		)
		if err != nil {
			return DatabaseProvision{}, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO server_database_hosts(
				server_id, image_reference, admin_password_encrypted
			) VALUES ($1, $2, $3)
		`, serverID, serverDatabaseImage, encryptedAdmin); err != nil {
			return DatabaseProvision{}, err
		}
	} else if err != nil {
		return DatabaseProvision{}, err
	}
	adminPassword, err := box.Open(
		encryptedAdmin,
		[]byte("database-host:"+serverID+":admin"),
	)
	if err != nil {
		return DatabaseProvision{}, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO server_databases(
			id, server_id, name, username, password_encrypted, created_by
		) VALUES ($1, $2, $3, $4, $5, $6)
	`, databaseID, serverID, name, username, encryptedPassword, actorID)
	if err != nil {
		var constraintErr *pgconn.PgError
		if errors.As(err, &constraintErr) && constraintErr.Code == "23505" {
			return DatabaseProvision{}, ErrConflict
		}
		return DatabaseProvision{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return DatabaseProvision{}, err
	}
	return DatabaseProvision{
		Database: ServerDatabase{
			ID: databaseID, ServerID: serverID, Name: name, Username: username,
			Engine: "postgresql", Host: "dockside-db", Port: 5432,
			Status: "provisioning", CreatedBy: &actorID, CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		},
		Password: password, AdminPassword: adminPassword,
	}, nil
}

func (s *Store) CompleteDatabaseProvision(
	ctx context.Context,
	provision DatabaseProvision,
	result engineclient.DatabaseResult,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		UPDATE server_databases
		SET status = 'ready', last_error = NULL, updated_at = now()
		WHERE id = $1 AND server_id = $2
	`, provision.Database.ID, provision.Database.ServerID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE server_database_hosts
		SET status = 'ready', container_id = $2, volume_name = $3,
		    last_error = NULL, updated_at = now()
		WHERE server_id = $1
	`, provision.Database.ServerID, result.ContainerID, result.VolumeName); err != nil {
		return err
	}
	return addConfigurationActivity(
		ctx, tx, provision.Database.ServerID, valueOr(provision.Database.CreatedBy, ""),
		"server.database.created", "Server database created",
		map[string]any{
			"database_id": provision.Database.ID,
			"name":        provision.Database.Name,
			"engine":      "postgresql",
		},
	)
}

func (s *Store) FailDatabaseProvision(
	ctx context.Context,
	provision DatabaseProvision,
	provisionErr error,
) error {
	detail := provisionErr.Error()
	if len(detail) > 2000 {
		detail = detail[:2000]
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE server_databases
		SET status = 'failed', last_error = $2, updated_at = now()
		WHERE id = $1
	`, provision.Database.ID, detail)
	return err
}

func (s *Store) ListDatabases(ctx context.Context, serverID string) ([]ServerDatabase, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, server_id, name, username, 'postgresql', 'dockside-db', 5432,
		       status, last_error, created_by, created_at, updated_at
		FROM server_databases
		WHERE server_id = $1
		ORDER BY created_at DESC
	`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ServerDatabase, 0)
	for rows.Next() {
		var item ServerDatabase
		if err := rows.Scan(
			&item.ID, &item.ServerID, &item.Name, &item.Username,
			&item.Engine, &item.Host, &item.Port, &item.Status, &item.LastError,
			&item.CreatedBy, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) DatabaseJob(
	ctx context.Context,
	serverID, databaseID string,
	box *secure.Box,
) (DatabaseJob, error) {
	var (
		result         DatabaseJob
		encryptedPass  string
		encryptedAdmin string
	)
	err := s.pool.QueryRow(ctx, `
		SELECT
			d.id, d.server_id, d.name, d.username, 'postgresql', 'dockside-db', 5432,
			d.status, d.last_error, d.created_by, d.created_at, d.updated_at,
			d.password_encrypted, h.admin_password_encrypted
		FROM server_databases AS d
		JOIN server_database_hosts AS h ON h.server_id = d.server_id
		WHERE d.id = $1 AND d.server_id = $2
	`, databaseID, serverID).Scan(
		&result.Database.ID, &result.Database.ServerID, &result.Database.Name,
		&result.Database.Username, &result.Database.Engine, &result.Database.Host,
		&result.Database.Port, &result.Database.Status, &result.Database.LastError,
		&result.Database.CreatedBy, &result.Database.CreatedAt, &result.Database.UpdatedAt,
		&encryptedPass, &encryptedAdmin,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, err
	}
	result.Password, err = box.Open(
		encryptedPass,
		[]byte("database:"+databaseID+":password"),
	)
	if err != nil {
		return result, err
	}
	result.AdminPassword, err = box.Open(
		encryptedAdmin,
		[]byte("database-host:"+serverID+":admin"),
	)
	return result, err
}

func (s *Store) DatabaseRemovalPlan(
	ctx context.Context,
	serverID, databaseID string,
) (bool, error) {
	var (
		exists    bool
		remaining int
	)
	err := s.pool.QueryRow(ctx, `
		SELECT
			EXISTS(
				SELECT 1 FROM server_databases
				WHERE id = $2 AND server_id = $1
			),
			(
				SELECT count(*) FROM server_databases
				WHERE server_id = $1 AND id <> $2
			)
	`, serverID, databaseID).Scan(&exists, &remaining)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, ErrNotFound
	}
	return remaining == 0, nil
}

func (s *Store) CompleteDatabaseDeletion(
	ctx context.Context,
	serverID, databaseID, actorID string,
	removeHost bool,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		DELETE FROM server_databases WHERE id = $1 AND server_id = $2
	`, databaseID, serverID); err != nil {
		return err
	}
	if removeHost {
		if _, err := tx.Exec(ctx, `
			DELETE FROM server_database_hosts WHERE server_id = $1
		`, serverID); err != nil {
			return err
		}
	}
	return addConfigurationActivity(
		ctx, tx, serverID, actorID,
		"server.database.deleted", "Server database deleted",
		map[string]any{"database_id": databaseID, "host_removed": removeHost},
	)
}

func (s *Store) RotateDatabasePassword(
	ctx context.Context,
	serverID, databaseID, actorID, password string,
	box *secure.Box,
) error {
	encrypted, err := box.Seal(
		password,
		[]byte("database:"+databaseID+":password"),
	)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		UPDATE server_databases
		SET password_encrypted = $3, updated_at = now()
		WHERE id = $1 AND server_id = $2 AND status = 'ready'
	`, databaseID, serverID, encrypted)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return addConfigurationActivity(
		ctx, tx, serverID, actorID,
		"server.database.password_rotated", "Database password rotated",
		map[string]any{"database_id": databaseID},
	)
}

func (j DatabaseJob) EngineRequest() engineclient.DatabaseRequest {
	return engineclient.DatabaseRequest{
		Name:          j.Database.Name,
		Username:      j.Database.Username,
		Password:      j.Password,
		AdminPassword: j.AdminPassword,
	}
}

func (p DatabaseProvision) EngineRequest() engineclient.DatabaseRequest {
	return engineclient.DatabaseRequest{
		Name:          p.Database.Name,
		Username:      p.Database.Username,
		Password:      p.Password,
		AdminPassword: p.AdminPassword,
	}
}
