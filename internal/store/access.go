package store

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/dockside-gg/game-panel/internal/identity"
	"github.com/jackc/pgx/v5"
)

var builtInRolePermissions = map[string][]string{
	"owner": {"*"},
	"administrator": {
		"server.view",
		"server.power.start", "server.power.stop", "server.power.restart", "server.power.kill",
		"server.console.read", "server.console.write",
		"server.files.read", "server.files.write", "server.files.delete",
		"server.backups.manage", "server.backups.restore",
		"server.schedules.manage", "server.databases.manage",
		"server.network.manage", "server.startup.manage",
		"server.resources.manage", "server.webhooks.manage", "server.delete",
	},
	"operator": {
		"server.view",
		"server.power.start", "server.power.stop", "server.power.restart", "server.power.kill",
		"server.console.read", "server.console.write",
		"server.files.read", "server.files.write", "server.files.delete",
		"server.backups.manage", "server.backups.restore",
		"server.schedules.manage",
	},
	"viewer": {
		"server.view", "server.console.read", "server.files.read",
	},
}

type ServerAccessBinding struct {
	ServerID   string `json:"server_id"`
	ServerName string `json:"server_name"`
	Role       string `json:"role"`
}

func (s *Store) ensureBuiltInRolePermissions(ctx context.Context, installationID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for roleName, requested := range builtInRolePermissions {
		permissions := requested
		if len(requested) == 1 && requested[0] == "*" {
			rows, err := tx.Query(ctx, "SELECT name FROM permissions ORDER BY name")
			if err != nil {
				return err
			}
			permissions = nil
			for rows.Next() {
				var permission string
				if err := rows.Scan(&permission); err != nil {
					rows.Close()
					return err
				}
				permissions = append(permissions, permission)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return err
			}
			rows.Close()
		}
		for _, permission := range permissions {
			if _, err := tx.Exec(ctx, `
				INSERT INTO role_permissions(role_id, permission_name)
				SELECT role.id, $3
				FROM roles AS role
				WHERE role.installation_id = $1 AND role.name = $2
				ON CONFLICT DO NOTHING
			`, installationID, roleName, permission); err != nil {
				return fmt.Errorf("grant %s to %s: %w", permission, roleName, err)
			}
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) UserHasServerPermission(
	ctx context.Context,
	userID, serverID, permission string,
) (bool, error) {
	var allowed bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM role_bindings AS binding
			JOIN role_permissions AS granted ON granted.role_id = binding.role_id
			WHERE binding.user_id = $1
			  AND binding.server_id = $2
			  AND granted.permission_name = $3
		)
	`, userID, serverID, permission).Scan(&allowed)
	return allowed, err
}

func (s *Store) UserServerAccess(
	ctx context.Context,
	userID string,
) ([]ServerAccessBinding, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT server.id, server.name, role.name
		FROM role_bindings AS binding
		JOIN roles AS role ON role.id = binding.role_id
		JOIN servers AS server ON server.id = binding.server_id
		WHERE binding.user_id = $1
		  AND binding.server_id IS NOT NULL
		  AND server.deleted_at IS NULL
		ORDER BY lower(server.name)
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ServerAccessBinding, 0)
	for rows.Next() {
		var binding ServerAccessBinding
		if err := rows.Scan(&binding.ServerID, &binding.ServerName, &binding.Role); err != nil {
			return nil, err
		}
		result = append(result, binding)
	}
	return result, rows.Err()
}

func (s *Store) SetUserServerAccess(
	ctx context.Context,
	actorID, userID string,
	bindings map[string]string,
) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var targetRole, targetStatus string
	if err := tx.QueryRow(ctx, `
		SELECT panel_role, status FROM users WHERE id = $1 FOR UPDATE
	`, userID).Scan(&targetRole, &targetStatus); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if targetRole == "owner" || targetStatus != "active" {
		return ErrConflict
	}
	maximum := roleRank(targetRole)
	serverIDs := make([]string, 0, len(bindings))
	for serverID, roleName := range bindings {
		if roleName != "administrator" && roleName != "operator" && roleName != "viewer" {
			return ErrConflict
		}
		if roleRank(roleName) > maximum {
			return ErrConflict
		}
		serverIDs = append(serverIDs, serverID)
	}
	sort.Strings(serverIDs)
	if _, err := tx.Exec(ctx, `
		DELETE FROM role_bindings
		WHERE user_id = $1 AND server_id IS NOT NULL
	`, userID); err != nil {
		return err
	}
	for _, serverID := range serverIDs {
		roleName := bindings[serverID]
		id, err := identity.NewUUID()
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO role_bindings(
				id, user_id, role_id, server_id, created_by
			)
			SELECT $1, $2, role.id, server.id, $5
			FROM servers AS server
			JOIN installations AS installation ON installation.id = server.installation_id
			JOIN roles AS role
			  ON role.installation_id = installation.id AND role.name = $4
			WHERE server.id = $3 AND server.deleted_at IS NULL
		`, id, userID, serverID, roleName, actorID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrNotFound
		}
	}
	if err := insertAudit(
		ctx, tx, actorID, "user.server_access.update", "user", userID,
		map[string]any{"bindings": bindings},
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func roleRank(role string) int {
	switch role {
	case "owner":
		return 4
	case "administrator":
		return 3
	case "operator":
		return 2
	case "viewer":
		return 1
	default:
		return 0
	}
}

func (s *Store) UpdateUserAccess(
	ctx context.Context,
	actorID, userID, panelRole, status string,
) error {
	if roleRank(panelRole) < 1 || roleRank(panelRole) > 3 ||
		(status != "active" && status != "suspended") {
		return ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		UPDATE users
		SET panel_role = $2, status = $3, updated_at = now()
		WHERE id = $1 AND panel_role <> 'owner' AND status IN ('active', 'suspended')
	`, userID, panelRole, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM role_bindings AS binding
		USING roles AS role
		WHERE binding.role_id = role.id
		  AND binding.user_id = $1
		  AND binding.server_id IS NOT NULL
		  AND CASE role.name
			WHEN 'administrator' THEN 3
			WHEN 'operator' THEN 2
			WHEN 'viewer' THEN 1
			ELSE 99
		  END > $2
	`, userID, roleRank(panelRole)); err != nil {
		return err
	}
	if status == "suspended" {
		if _, err := tx.Exec(ctx, `
			UPDATE sessions SET revoked_at = now()
			WHERE user_id = $1 AND revoked_at IS NULL
		`, userID); err != nil {
			return err
		}
	}
	if err := insertAudit(
		ctx, tx, actorID, "user.access.update", "user", userID,
		map[string]any{"panel_role": panelRole, "status": status},
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
