package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/dockside-gg/game-panel/internal/identity"
	"github.com/dockside-gg/game-panel/internal/secure"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound       = errors.New("not found")
	ErrUnauthorized   = errors.New("unauthorized")
	ErrInviteInvalid  = errors.New("invite is invalid, expired, revoked, or already used")
	ErrAlreadyClaimed = errors.New("installation owner has already been claimed")
	ErrMFARequired    = errors.New("discord MFA is required by panel policy")
	ErrConflict       = errors.New("resource conflict")
)

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

type SetupStatus struct {
	Claimed          bool   `json:"claimed"`
	PublicURL        string `json:"public_url"`
	DiscordClientID  string `json:"discord_client_id"`
	MFAPolicy        string `json:"mfa_policy"`
	BootstrapEnabled bool   `json:"bootstrap_enabled"`
}

type User struct {
	ID           string     `json:"id"`
	DiscordID    string     `json:"discord_id"`
	Username     string     `json:"username"`
	GlobalName   *string    `json:"global_name"`
	AvatarHash   *string    `json:"avatar_hash"`
	Locale       *string    `json:"locale"`
	MFAEnabled   bool       `json:"mfa_enabled"`
	MFACheckedAt time.Time  `json:"mfa_checked_at"`
	Status       string     `json:"status"`
	PanelRole    string     `json:"panel_role"`
	CreatedAt    time.Time  `json:"created_at"`
	LastLoginAt  *time.Time `json:"last_login_at"`
}

type DiscordUser struct {
	ID         string  `json:"id"`
	Username   string  `json:"username"`
	GlobalName *string `json:"global_name"`
	Avatar     *string `json:"avatar"`
	Locale     *string `json:"locale"`
	MFAEnabled bool    `json:"mfa_enabled"`
	Bot        bool    `json:"bot"`
	System     bool    `json:"system"`
}

type OAuthState struct {
	ID       string
	Purpose  string
	InviteID *string
}

type Session struct {
	ID                string
	User              User
	CSRFHash          string
	IdleExpiresAt     time.Time
	AbsoluteExpiresAt time.Time
}

type Invite struct {
	ID          string     `json:"id"`
	Label       *string    `json:"label"`
	CreatedBy   string     `json:"created_by"`
	ClaimedBy   *string    `json:"claimed_by"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	ClaimedAt   *time.Time `json:"claimed_at"`
	RevokedAt   *time.Time `json:"revoked_at"`
	ClaimedUser *User      `json:"claimed_user,omitempty"`
}

type Dashboard struct {
	Servers struct {
		Total      int64 `json:"total"`
		Running    int64 `json:"running"`
		Stopped    int64 `json:"stopped"`
		Installing int64 `json:"installing"`
		Degraded   int64 `json:"degraded"`
		Attention  int64 `json:"attention"`
	} `json:"servers"`
	RecentActivity []ActivityEvent `json:"recent_activity"`
}

type ActivityEvent struct {
	ID        string          `json:"id"`
	ServerID  *string         `json:"server_id"`
	ActorID   *string         `json:"actor_user_id"`
	EventType string          `json:"event_type"`
	Severity  string          `json:"severity"`
	Summary   string          `json:"summary"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
}

func (s *Store) EnsureInstallation(
	ctx context.Context,
	id, publicURL, discordClientID, bootstrapToken, mfaPolicy string,
) error {
	bootstrapHash := ""
	if bootstrapToken != "" {
		bootstrapHash = secure.Hash(bootstrapToken)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO installations(
			id, public_url, discord_client_id, bootstrap_token_hash, mfa_policy
		)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5)
		ON CONFLICT (id) DO UPDATE SET
			public_url = EXCLUDED.public_url,
			discord_client_id = EXCLUDED.discord_client_id,
			bootstrap_token_hash = CASE
				WHEN installations.owner_user_id IS NULL
					THEN COALESCE(installations.bootstrap_token_hash, EXCLUDED.bootstrap_token_hash)
				ELSE NULL
			END,
			mfa_policy = CASE
				WHEN installations.owner_user_id IS NULL THEN EXCLUDED.mfa_policy
				ELSE installations.mfa_policy
			END,
			updated_at = now()
	`, id, publicURL, discordClientID, bootstrapHash, mfaPolicy)
	if err != nil {
		return fmt.Errorf("ensure installation: %w", err)
	}
	return s.ensureBuiltInRoles(ctx, id)
}

func (s *Store) UpdateMFAPolicy(ctx context.Context, actorID, policy string) error {
	if policy != "off" && policy != "administrators" &&
		policy != "operators" && policy != "everyone" {
		return ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		UPDATE installations SET mfa_policy = $1, updated_at = now()
	`, policy); err != nil {
		return err
	}
	if err := insertAudit(
		ctx, tx, actorID, "installation.mfa_policy.update",
		"installation", "", map[string]any{"mfa_policy": policy},
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ensureBuiltInRoles(ctx context.Context, installationID string) error {
	for _, role := range []string{"owner", "administrator", "operator", "viewer"} {
		id, err := identity.NewUUID()
		if err != nil {
			return err
		}
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO roles(id, installation_id, name, builtin)
			VALUES ($1, $2, $3, true)
			ON CONFLICT (installation_id, name) DO NOTHING
		`, id, installationID, role); err != nil {
			return fmt.Errorf("ensure role %s: %w", role, err)
		}
	}
	return s.ensureBuiltInRolePermissions(ctx, installationID)
}

func (s *Store) SetupStatus(ctx context.Context) (SetupStatus, error) {
	var status SetupStatus
	err := s.pool.QueryRow(ctx, `
		SELECT owner_user_id IS NOT NULL, public_url, discord_client_id, mfa_policy,
		       bootstrap_token_hash IS NOT NULL
		FROM installations
		LIMIT 1
	`).Scan(&status.Claimed, &status.PublicURL, &status.DiscordClientID, &status.MFAPolicy, &status.BootstrapEnabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return status, ErrNotFound
	}
	if err != nil {
		return status, fmt.Errorf("get setup status: %w", err)
	}
	return status, nil
}

func (s *Store) ValidateBootstrapToken(ctx context.Context, token string) error {
	if token == "" {
		return ErrUnauthorized
	}
	var valid bool
	err := s.pool.QueryRow(ctx, `
		SELECT owner_user_id IS NULL AND bootstrap_token_hash = $1
		FROM installations
		LIMIT 1
	`, secure.Hash(token)).Scan(&valid)
	if errors.Is(err, pgx.ErrNoRows) || !valid {
		return ErrUnauthorized
	}
	return err
}

func (s *Store) ResolveInvite(ctx context.Context, token string) (string, error) {
	if token == "" {
		return "", ErrInviteInvalid
	}
	var id string
	err := s.pool.QueryRow(ctx, `
		SELECT id
		FROM invites
		WHERE token_hash = $1
		  AND claimed_at IS NULL
		  AND revoked_at IS NULL
		  AND expires_at > now()
	`, secure.Hash(token)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrInviteInvalid
	}
	if err != nil {
		return "", fmt.Errorf("resolve invite: %w", err)
	}
	return id, nil
}

func (s *Store) CreateOAuthState(ctx context.Context, purpose string, inviteID *string, rawState string) error {
	id, err := identity.NewUUID()
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO oauth_states(id, state_hash, purpose, invite_id, expires_at)
		VALUES ($1, $2, $3, $4, now() + interval '10 minutes')
	`, id, secure.Hash(rawState), purpose, inviteID)
	if err != nil {
		return fmt.Errorf("create oauth state: %w", err)
	}
	return nil
}

func (s *Store) ConsumeOAuthState(ctx context.Context, rawState string) (OAuthState, error) {
	var state OAuthState
	err := s.pool.QueryRow(ctx, `
		UPDATE oauth_states
		SET consumed_at = now()
		WHERE state_hash = $1
		  AND consumed_at IS NULL
		  AND expires_at > now()
		RETURNING id, purpose, invite_id
	`, secure.Hash(rawState)).Scan(&state.ID, &state.Purpose, &state.InviteID)
	if errors.Is(err, pgx.ErrNoRows) {
		return state, ErrUnauthorized
	}
	if err != nil {
		return state, fmt.Errorf("consume oauth state: %w", err)
	}
	return state, nil
}

func (s *Store) CompleteOAuth(ctx context.Context, state OAuthState, discord DiscordUser) (User, error) {
	if discord.Bot || discord.System || discord.ID == "" || discord.Username == "" {
		return User{}, ErrUnauthorized
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return User{}, fmt.Errorf("begin oauth completion: %w", err)
	}
	defer tx.Rollback(ctx)

	var user User
	switch state.Purpose {
	case "claim":
		user, err = claimOwner(ctx, tx, discord)
	case "invite":
		if state.InviteID == nil {
			err = ErrInviteInvalid
			break
		}
		user, err = claimInvite(ctx, tx, *state.InviteID, discord)
	case "login":
		user, err = loginExisting(ctx, tx, discord)
	default:
		err = ErrUnauthorized
	}
	if err != nil {
		return User{}, err
	}

	var policy string
	if err := tx.QueryRow(ctx, "SELECT mfa_policy FROM installations LIMIT 1").Scan(&policy); err != nil {
		return User{}, fmt.Errorf("load mfa policy: %w", err)
	}
	if mfaRequired(policy, user.PanelRole) && !discord.MFAEnabled {
		return User{}, ErrMFARequired
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, fmt.Errorf("commit oauth completion: %w", err)
	}
	return user, nil
}

func claimOwner(ctx context.Context, tx pgx.Tx, discord DiscordUser) (User, error) {
	var ownerID *string
	if err := tx.QueryRow(ctx, "SELECT owner_user_id FROM installations FOR UPDATE").Scan(&ownerID); err != nil {
		return User{}, fmt.Errorf("lock installation: %w", err)
	}
	if ownerID != nil {
		return User{}, ErrAlreadyClaimed
	}
	user, err := upsertDiscordUser(ctx, tx, discord, "active", "owner")
	if err != nil {
		return User{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE installations
		SET owner_user_id = $1, bootstrap_token_hash = NULL, updated_at = now()
	`, user.ID); err != nil {
		return User{}, fmt.Errorf("claim installation: %w", err)
	}
	return user, nil
}

func claimInvite(ctx context.Context, tx pgx.Tx, inviteID string, discord DiscordUser) (User, error) {
	var available bool
	if err := tx.QueryRow(ctx, `
		SELECT claimed_at IS NULL AND revoked_at IS NULL AND expires_at > now()
		FROM invites
		WHERE id = $1
		FOR UPDATE
	`, inviteID).Scan(&available); errors.Is(err, pgx.ErrNoRows) || !available {
		return User{}, ErrInviteInvalid
	} else if err != nil {
		return User{}, fmt.Errorf("lock invite: %w", err)
	}

	user, err := upsertDiscordUser(ctx, tx, discord, "pending", "viewer")
	if err != nil {
		return User{}, err
	}
	if user.Status == "suspended" || user.Status == "rejected" {
		return User{}, ErrUnauthorized
	}
	if _, err := tx.Exec(ctx, `
		UPDATE invites
		SET claimed_by = $1, claimed_at = now()
		WHERE id = $2
	`, user.ID, inviteID); err != nil {
		return User{}, fmt.Errorf("consume invite: %w", err)
	}
	return user, nil
}

func loginExisting(ctx context.Context, tx pgx.Tx, discord DiscordUser) (User, error) {
	var status string
	if err := tx.QueryRow(ctx, "SELECT status FROM users WHERE discord_id = $1", discord.ID).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUnauthorized
	} else if err != nil {
		return User{}, fmt.Errorf("find discord user: %w", err)
	}
	if status == "suspended" || status == "rejected" {
		return User{}, ErrUnauthorized
	}
	return upsertDiscordUser(ctx, tx, discord, status, "")
}

func upsertDiscordUser(ctx context.Context, tx pgx.Tx, discord DiscordUser, initialStatus, initialRole string) (User, error) {
	id, err := identity.NewUUID()
	if err != nil {
		return User{}, err
	}
	if initialRole == "" {
		initialRole = "viewer"
	}
	var user User
	err = tx.QueryRow(ctx, `
		INSERT INTO users(
			id, discord_id, username, global_name, avatar_hash, locale,
			mfa_enabled, mfa_checked_at, status, panel_role, last_login_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now(), $8, $9, now())
		ON CONFLICT (discord_id) DO UPDATE SET
			username = EXCLUDED.username,
			global_name = EXCLUDED.global_name,
			avatar_hash = EXCLUDED.avatar_hash,
			locale = EXCLUDED.locale,
			mfa_enabled = EXCLUDED.mfa_enabled,
			mfa_checked_at = now(),
			last_login_at = now(),
			updated_at = now()
		RETURNING id, discord_id, username, global_name, avatar_hash, locale,
		          mfa_enabled, mfa_checked_at, status, panel_role, created_at, last_login_at
	`, id, discord.ID, discord.Username, discord.GlobalName, discord.Avatar, discord.Locale,
		discord.MFAEnabled, initialStatus, initialRole,
	).Scan(
		&user.ID, &user.DiscordID, &user.Username, &user.GlobalName, &user.AvatarHash, &user.Locale,
		&user.MFAEnabled, &user.MFACheckedAt, &user.Status, &user.PanelRole, &user.CreatedAt, &user.LastLoginAt,
	)
	if err != nil {
		return User{}, fmt.Errorf("upsert discord user: %w", err)
	}
	return user, nil
}

func mfaRequired(policy, role string) bool {
	switch policy {
	case "everyone":
		return true
	case "operators":
		return role == "owner" || role == "administrator" || role == "operator"
	case "administrators":
		return role == "owner" || role == "administrator"
	default:
		return false
	}
}

func (s *Store) CreateSession(
	ctx context.Context,
	userID, rawToken, rawCSRF string,
	ip net.IP,
	userAgent string,
	idle, absolute time.Duration,
) error {
	id, err := identity.NewUUID()
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO sessions(
			id, user_id, token_hash, csrf_hash, ip_address, user_agent,
			idle_expires_at, absolute_expires_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, now() + $7::interval, now() + $8::interval)
	`, id, userID, secure.Hash(rawToken), secure.Hash(rawCSRF), ipString(ip), truncate(userAgent, 512),
		durationInterval(idle), durationInterval(absolute),
	)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (s *Store) SessionByToken(ctx context.Context, rawToken string, idle time.Duration) (Session, error) {
	var session Session
	err := s.pool.QueryRow(ctx, `
		UPDATE sessions AS session
		SET last_seen_at = now(),
		    idle_expires_at = LEAST(now() + $2::interval, session.absolute_expires_at)
		FROM users AS user_account
		WHERE session.token_hash = $1
		  AND session.user_id = user_account.id
		  AND session.revoked_at IS NULL
		  AND session.idle_expires_at > now()
		  AND session.absolute_expires_at > now()
		  AND user_account.status IN ('pending', 'active')
		RETURNING
			session.id, session.csrf_hash, session.idle_expires_at, session.absolute_expires_at,
			user_account.id, user_account.discord_id, user_account.username, user_account.global_name,
			user_account.avatar_hash, user_account.locale, user_account.mfa_enabled,
			user_account.mfa_checked_at, user_account.status, user_account.panel_role,
			user_account.created_at, user_account.last_login_at
	`, secure.Hash(rawToken), durationInterval(idle)).Scan(
		&session.ID, &session.CSRFHash, &session.IdleExpiresAt, &session.AbsoluteExpiresAt,
		&session.User.ID, &session.User.DiscordID, &session.User.Username, &session.User.GlobalName,
		&session.User.AvatarHash, &session.User.Locale, &session.User.MFAEnabled,
		&session.User.MFACheckedAt, &session.User.Status, &session.User.PanelRole,
		&session.User.CreatedAt, &session.User.LastLoginAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrUnauthorized
	}
	if err != nil {
		return Session{}, fmt.Errorf("load session: %w", err)
	}
	return session, nil
}

func (s *Store) RevokeSession(ctx context.Context, sessionID string) error {
	_, err := s.pool.Exec(ctx, "UPDATE sessions SET revoked_at = now() WHERE id = $1", sessionID)
	return err
}

func (s *Store) CreateInvite(ctx context.Context, actorID, label, rawToken string, expiresAt time.Time) (Invite, error) {
	id, err := identity.NewUUID()
	if err != nil {
		return Invite{}, err
	}
	var invite Invite
	var normalizedLabel *string
	if value := strings.TrimSpace(label); value != "" {
		normalizedLabel = &value
	}
	err = s.pool.QueryRow(ctx, `
		INSERT INTO invites(id, token_hash, label, created_by, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, label, created_by, claimed_by, created_at, expires_at, claimed_at, revoked_at
	`, id, secure.Hash(rawToken), normalizedLabel, actorID, expiresAt).Scan(
		&invite.ID, &invite.Label, &invite.CreatedBy, &invite.ClaimedBy,
		&invite.CreatedAt, &invite.ExpiresAt, &invite.ClaimedAt, &invite.RevokedAt,
	)
	if err != nil {
		return Invite{}, fmt.Errorf("create invite: %w", err)
	}
	return invite, nil
}

func (s *Store) ListInvites(ctx context.Context) ([]Invite, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT
			invite.id, invite.label, invite.created_by, invite.claimed_by,
			invite.created_at, invite.expires_at, invite.claimed_at, invite.revoked_at,
			user_account.id, user_account.discord_id, user_account.username, user_account.global_name,
			user_account.avatar_hash, user_account.locale, user_account.mfa_enabled,
			user_account.mfa_checked_at, user_account.status, user_account.panel_role,
			user_account.created_at, user_account.last_login_at
		FROM invites AS invite
		LEFT JOIN users AS user_account ON user_account.id = invite.claimed_by
		ORDER BY invite.created_at DESC
		LIMIT 200
	`)
	if err != nil {
		return nil, fmt.Errorf("list invites: %w", err)
	}
	defer rows.Close()
	invites := make([]Invite, 0)
	for rows.Next() {
		var invite Invite
		var userID, discordID, username *string
		var globalName, avatarHash, locale *string
		var mfaEnabled *bool
		var mfaCheckedAt, userCreatedAt *time.Time
		var status, panelRole *string
		var lastLoginAt *time.Time
		if err := rows.Scan(
			&invite.ID, &invite.Label, &invite.CreatedBy, &invite.ClaimedBy,
			&invite.CreatedAt, &invite.ExpiresAt, &invite.ClaimedAt, &invite.RevokedAt,
			&userID, &discordID, &username, &globalName, &avatarHash, &locale, &mfaEnabled,
			&mfaCheckedAt, &status, &panelRole, &userCreatedAt, &lastLoginAt,
		); err != nil {
			return nil, fmt.Errorf("scan invite: %w", err)
		}
		if userID != nil {
			invite.ClaimedUser = &User{
				ID:           *userID,
				DiscordID:    deref(discordID),
				Username:     deref(username),
				GlobalName:   globalName,
				AvatarHash:   avatarHash,
				Locale:       locale,
				MFAEnabled:   derefBool(mfaEnabled),
				MFACheckedAt: derefTime(mfaCheckedAt),
				Status:       deref(status),
				PanelRole:    deref(panelRole),
				CreatedAt:    derefTime(userCreatedAt),
				LastLoginAt:  lastLoginAt,
			}
		}
		invites = append(invites, invite)
	}
	return invites, rows.Err()
}

func (s *Store) RevokeInvite(ctx context.Context, inviteID string) error {
	command, err := s.pool.Exec(ctx, `
		UPDATE invites
		SET revoked_at = now()
		WHERE id = $1 AND claimed_at IS NULL AND revoked_at IS NULL
	`, inviteID)
	if err != nil {
		return fmt.Errorf("revoke invite: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, discord_id, username, global_name, avatar_hash, locale,
		       mfa_enabled, mfa_checked_at, status, panel_role, created_at, last_login_at
		FROM users
		ORDER BY
			CASE status WHEN 'pending' THEN 0 WHEN 'active' THEN 1 ELSE 2 END,
			created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	users := make([]User, 0)
	for rows.Next() {
		var user User
		if err := rows.Scan(
			&user.ID, &user.DiscordID, &user.Username, &user.GlobalName, &user.AvatarHash, &user.Locale,
			&user.MFAEnabled, &user.MFACheckedAt, &user.Status, &user.PanelRole,
			&user.CreatedAt, &user.LastLoginAt,
		); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *Store) ActivateUser(ctx context.Context, actorID, userID, panelRole string) error {
	if panelRole != "administrator" && panelRole != "operator" && panelRole != "viewer" {
		return fmt.Errorf("%w: invalid role", ErrConflict)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	command, err := tx.Exec(ctx, `
		UPDATE users
		SET status = 'active', panel_role = $1, updated_at = now()
		WHERE id = $2 AND status = 'pending'
	`, panelRole, userID)
	if err != nil {
		return fmt.Errorf("activate user: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := insertAudit(ctx, tx, actorID, "user.activate", "user", userID, map[string]any{"panel_role": panelRole}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) RejectUser(ctx context.Context, actorID, userID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	command, err := tx.Exec(ctx, `
		UPDATE users
		SET status = 'rejected', updated_at = now()
		WHERE id = $1 AND status = 'pending'
	`, userID)
	if err != nil {
		return fmt.Errorf("reject user: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, "UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL", userID); err != nil {
		return fmt.Errorf("revoke rejected user sessions: %w", err)
	}
	if err := insertAudit(ctx, tx, actorID, "user.reject", "user", userID, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) Dashboard(ctx context.Context, userID, panelRole string) (Dashboard, error) {
	var result Dashboard
	privileged := panelRole == "owner" || panelRole == "administrator"
	err := s.pool.QueryRow(ctx, `
		WITH visible_servers AS (
			SELECT server.*
			FROM servers AS server
			WHERE $2::boolean
			   OR EXISTS(
					SELECT 1
					FROM role_bindings AS binding
					JOIN role_permissions AS granted ON granted.role_id = binding.role_id
					WHERE binding.user_id = $1
					  AND binding.server_id = server.id
					  AND granted.permission_name = 'server.view'
			   )
		)
		SELECT
			count(*) FILTER (WHERE deleted_at IS NULL),
			count(*) FILTER (WHERE deleted_at IS NULL AND status = 'running'),
			count(*) FILTER (WHERE deleted_at IS NULL AND status = 'stopped'),
			count(*) FILTER (WHERE deleted_at IS NULL AND status = 'installing'),
			count(*) FILTER (WHERE deleted_at IS NULL AND status = 'degraded'),
			count(*) FILTER (
				WHERE deleted_at IS NULL
				  AND (
					status IN ('degraded', 'failed')
					OR (
						status = 'stopped'
						AND stop_reason NOT IN ('requested', 'clean_exit')
					)
				  )
			)
		FROM visible_servers
	`, userID, privileged).Scan(
		&result.Servers.Total,
		&result.Servers.Running,
		&result.Servers.Stopped,
		&result.Servers.Installing,
		&result.Servers.Degraded,
		&result.Servers.Attention,
	)
	if err != nil {
		return result, fmt.Errorf("dashboard server totals: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, server_id, actor_user_id, event_type, severity, summary, data, created_at
		FROM activity_events AS event
		WHERE $2::boolean
		   OR EXISTS(
				SELECT 1
				FROM role_bindings AS binding
				JOIN role_permissions AS granted ON granted.role_id = binding.role_id
				WHERE binding.user_id = $1
				  AND binding.server_id = event.server_id
				  AND granted.permission_name = 'server.view'
		   )
		ORDER BY created_at DESC
		LIMIT 20
	`, userID, privileged)
	if err != nil {
		return result, fmt.Errorf("dashboard activity: %w", err)
	}
	defer rows.Close()
	result.RecentActivity = make([]ActivityEvent, 0)
	for rows.Next() {
		var event ActivityEvent
		if err := rows.Scan(
			&event.ID, &event.ServerID, &event.ActorID, &event.EventType,
			&event.Severity, &event.Summary, &event.Data, &event.CreatedAt,
		); err != nil {
			return result, fmt.Errorf("scan dashboard activity: %w", err)
		}
		result.RecentActivity = append(result.RecentActivity, event)
	}
	return result, rows.Err()
}

func (s *Store) AddAudit(
	ctx context.Context,
	actorID, action, targetType, targetID, requestID string,
	ip net.IP,
	userAgent string,
	data map[string]any,
) error {
	id, err := identity.NewUUID()
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal audit data: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO audit_events(
			id, actor_user_id, action, target_type, target_id,
			request_id, ip_address, user_agent, data
		)
		VALUES ($1, NULLIF($2, '')::uuid, $3, $4, NULLIF($5, ''), NULLIF($6, ''),
		        $7, NULLIF($8, ''), $9)
	`, id, actorID, action, targetType, targetID, requestID, ipString(ip), truncate(userAgent, 512), encoded)
	return err
}

func insertAudit(ctx context.Context, tx pgx.Tx, actorID, action, targetType, targetID string, data map[string]any) error {
	id, err := identity.NewUUID()
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO audit_events(id, actor_user_id, action, target_type, target_id, data)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, id, actorID, action, targetType, targetID, encoded)
	return err
}

func durationInterval(value time.Duration) string {
	return fmt.Sprintf("%f seconds", value.Seconds())
}

func ipString(ip net.IP) any {
	if ip == nil {
		return nil
	}
	return ip.String()
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func derefBool(value *bool) bool {
	return value != nil && *value
}

func derefTime(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}
