package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/dockside-gg/game-panel/internal/config"
	"github.com/dockside-gg/game-panel/internal/engineclient"
	"github.com/dockside-gg/game-panel/internal/secure"
	"github.com/dockside-gg/game-panel/internal/store"
	"github.com/dockside-gg/game-panel/internal/templates"
	"github.com/dockside-gg/game-panel/internal/updates"
	"github.com/dockside-gg/game-panel/internal/webui"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	sessionCookieName = "dockside_session"
	csrfCookieName    = "dockside_csrf"
	oauthCookieName   = "dockside_oauth_state"
)

type Server struct {
	cfg       config.Config
	store     *store.Store
	pool      *pgxpool.Pool
	engine    *engineclient.Client
	logger    *slog.Logger
	webFS     fs.FS
	indexHTML []byte
	box       *secure.Box
	catalog   *templates.CatalogSyncer
	updates   *updates.Checker
}

func New(
	cfg config.Config,
	dataStore *store.Store,
	pool *pgxpool.Pool,
	engine *engineclient.Client,
	catalog *templates.CatalogSyncer,
	logger *slog.Logger,
) (*Server, error) {
	webFS := webui.FS()
	indexHTML, err := fs.ReadFile(webFS, "index.html")
	if err != nil {
		return nil, err
	}
	box, err := secure.NewBox(cfg.EncryptionKey)
	if err != nil {
		return nil, err
	}
	return &Server{
		cfg:       cfg,
		store:     dataStore,
		pool:      pool,
		engine:    engine,
		logger:    logger,
		webFS:     webFS,
		indexHTML: indexHTML,
		box:       box,
		catalog:   catalog,
		updates:   updates.NewChecker(),
	}, nil
}

func (s *Server) Handler() http.Handler {
	router := chi.NewRouter()
	router.Use(s.middleware)

	router.Get("/health/live", s.live)
	router.Get("/health/ready", s.ready)

	router.Route("/api/v1", func(api chi.Router) {
		api.Get("/setup/status", s.setupStatus)
		api.Post("/auth/discord/begin", s.beginDiscord)
		api.Get("/auth/discord/callback", s.discordCallback)

		api.Group(func(authenticated chi.Router) {
			authenticated.Use(s.requireSession)
			authenticated.Get("/session", s.currentSession)
			authenticated.With(s.requireCSRF).Post("/auth/logout", s.logout)
		})

		api.Group(func(active chi.Router) {
			active.Use(s.requireActive)
			active.Get("/dashboard", s.dashboard)
			active.Get("/host", s.hostStatus)
			active.With(s.requireAdministrator).Get("/system/containers", s.listSystemContainers)
			active.With(s.requireAdministrator).Get("/system/containers/{component}/logs", s.systemContainerLogs)
			active.With(s.requireAdministrator).Get("/diagnostics", s.diagnostics)
			active.Get("/templates", s.listTemplates)
			active.Get("/templates/facets", s.templateFacets)
			active.Get("/templates/catalog", s.templateCatalogStatus)
			active.With(s.requireAdministrator, s.requireCSRF).Post("/templates/catalog/sync", s.syncTemplateCatalog)
			active.Get("/templates/{versionID}", s.templateDetail)
			active.Get("/templates/{versionID}/export", s.exportTemplate)
			active.With(s.requireCSRF).Post("/templates/import", s.importTemplate)
			active.With(s.requireCSRF).Post("/templates/{versionID}/fork", s.forkTemplate)
			active.With(s.requireCSRF).Delete("/templates/{templateID}", s.archiveTemplate)
			active.Get("/servers", s.listServers)
			active.With(s.requireCSRF).Post("/servers", s.createServer)
			active.With(s.requireServerPermission("server.view")).Get("/servers/{serverID}", s.serverDetail)
			active.With(s.requireServerPermission("server.view")).Get("/servers/{serverID}/configuration", s.serverConfiguration)
			active.With(s.requireServerPermission("server.startup.manage"), s.requireCSRF).Put("/servers/{serverID}/startup", s.updateServerStartup)
			active.With(s.requireServerPermission("server.startup.manage"), s.requireCSRF).Post("/servers/{serverID}/template", s.createTemplateFromServer)
			active.With(s.requireServerPermission("server.view")).Get("/servers/{serverID}/template-update", s.serverTemplateUpdateStatus)
			active.With(s.requireServerPermission("server.startup.manage"), s.requireCSRF).Post("/servers/{serverID}/template-update", s.updateServerTemplate)
			active.With(s.requireServerPermission("server.network.manage"), s.requireCSRF).Put("/servers/{serverID}/network", s.updateServerNetwork)
			active.With(s.requireServerPermission("server.resources.manage"), s.requireCSRF).Put("/servers/{serverID}/settings", s.updateServerSettings)
			active.With(s.requireServerPermission("server.view")).Get("/servers/{serverID}/activity", s.serverActivity)
			active.With(s.requireServerPermission("server.console.read")).Get("/servers/{serverID}/console", s.serverConsole)
			active.With(s.requireServerPermission("server.console.write"), s.requireCSRF).Post("/servers/{serverID}/console", s.serverCommand)
			active.With(s.requireServerPermission("server.files.read")).Get("/servers/{serverID}/files", s.listServerFiles)
			active.With(s.requireServerPermission("server.files.read")).Get("/servers/{serverID}/files/download", s.downloadServerFile)
			active.With(s.requireServerPermission("server.files.delete"), s.requireCSRF).Delete("/servers/{serverID}/files", s.deleteServerFile)
			active.With(s.requireServerPermission("server.files.write"), s.requireCSRF).Patch("/servers/{serverID}/files", s.renameServerFile)
			active.With(s.requireServerPermission("server.files.read")).Get("/servers/{serverID}/files/content", s.readServerFile)
			active.With(s.requireServerPermission("server.files.write"), s.requireCSRF).Put("/servers/{serverID}/files/content", s.writeServerFile)
			active.With(s.requireServerPermission("server.files.write"), s.requireCSRF).Post("/servers/{serverID}/files/upload", s.uploadServerFile)
			active.With(s.requireServerPermission("server.files.write"), s.requireCSRF).Post("/servers/{serverID}/files/directories", s.createServerDirectory)
			active.With(s.requireServerPermission("server.view")).Get("/servers/{serverID}/backups", s.listBackups)
			active.With(s.requireServerPermission("server.backups.download")).Get("/servers/{serverID}/backups/{backupID}/download", s.downloadBackup)
			active.With(s.requireServerPermission("server.backups.manage"), s.requireCSRF).Post("/servers/{serverID}/backups", s.createBackup)
			active.With(s.requireServerPermission("server.backups.manage"), s.requireCSRF).Patch("/servers/{serverID}/backups/{backupID}", s.lockBackup)
			active.With(s.requireServerPermission("server.backups.manage"), s.requireCSRF).Delete("/servers/{serverID}/backups/{backupID}", s.deleteBackup)
			active.With(s.requireServerPermission("server.backups.restore"), s.requireCSRF).Post("/servers/{serverID}/backups/{backupID}/restore", s.restoreBackup)
			active.With(s.requireServerPermission("server.backups.manage"), s.requireCSRF).Post("/servers/{serverID}/backups/{backupID}/deliveries/{deliveryID}/retry", s.retryBackupDelivery)
			active.With(s.requireServerPermission("server.view")).Get("/servers/{serverID}/databases", s.listServerDatabases)
			active.With(s.requireServerPermission("server.databases.manage"), s.requireCSRF).Post("/servers/{serverID}/databases", s.createServerDatabase)
			active.With(s.requireServerPermission("server.databases.manage"), s.requireCSRF).Delete("/servers/{serverID}/databases/{databaseID}", s.deleteServerDatabase)
			active.With(s.requireServerPermission("server.databases.manage"), s.requireCSRF).Post("/servers/{serverID}/databases/{databaseID}/password", s.rotateServerDatabasePassword)
			active.With(s.requireServerPermission("server.view")).Get("/servers/{serverID}/schedules", s.listSchedules)
			active.With(s.requireServerPermission("server.schedules.manage"), s.requireCSRF).Post("/servers/{serverID}/schedules", s.createSchedule)
			active.With(s.requireServerPermission("server.schedules.manage"), s.requireCSRF).Patch("/servers/{serverID}/schedules/{scheduleID}", s.setScheduleEnabled)
			active.With(s.requireServerPermission("server.schedules.manage"), s.requireCSRF).Post("/servers/{serverID}/schedules/{scheduleID}/run", s.runScheduleNow)
			active.With(s.requireServerPermission("server.schedules.manage"), s.requireCSRF).Delete("/servers/{serverID}/schedules/{scheduleID}", s.deleteSchedule)
			active.With(s.requireServerPermission("server.view")).Get("/servers/{serverID}/webhooks", s.listWebhooks)
			active.With(s.requireServerPermission("server.webhooks.manage"), s.requireCSRF).Post("/servers/{serverID}/webhooks", s.createWebhook)
			active.With(s.requireServerPermission("server.webhooks.manage"), s.requireCSRF).Patch("/servers/{serverID}/webhooks/{webhookID}", s.setWebhookEnabled)
			active.With(s.requireServerPermission("server.webhooks.manage"), s.requireCSRF).Post("/servers/{serverID}/webhooks/{webhookID}/test", s.testWebhook)
			active.With(s.requireServerPermission("server.webhooks.manage")).Get("/servers/{serverID}/webhooks/{webhookID}/deliveries/{deliveryID}", s.webhookDelivery)
			active.With(s.requireServerPermission("server.webhooks.manage"), s.requireCSRF).Post("/servers/{serverID}/webhooks/{webhookID}/deliveries/{deliveryID}/retry", s.retryWebhookDelivery)
			active.With(s.requireServerPermission("server.webhooks.manage"), s.requireCSRF).Delete("/servers/{serverID}/webhooks/{webhookID}", s.deleteWebhook)
			active.With(s.requireCSRF).Post("/servers/{serverID}/power", s.serverPower)
			active.With(s.requireServerPermission("server.delete"), s.requireCSRF).Delete("/servers/{serverID}", s.deleteServer)
		})

		api.Group(func(owner chi.Router) {
			owner.Use(s.requireOwner)
			owner.Get("/users", s.listUsers)
			owner.With(s.requireCSRF).Post("/users/{userID}/activate", s.activateUser)
			owner.With(s.requireCSRF).Post("/users/{userID}/reject", s.rejectUser)
			owner.With(s.requireCSRF).Patch("/users/{userID}", s.updateUserAccess)
			owner.Get("/invites", s.listInvites)
			owner.With(s.requireCSRF).Post("/invites", s.createInvite)
			owner.With(s.requireCSRF).Delete("/invites/{inviteID}", s.revokeInvite)
			owner.Get("/users/{userID}/server-access", s.userServerAccess)
			owner.With(s.requireCSRF).Put("/users/{userID}/server-access", s.setUserServerAccess)
			owner.Get("/installation/settings", s.installationSettings)
			owner.With(s.requireCSRF).Put("/installation/settings", s.updateInstallationSettings)
			owner.Get("/installation/update", s.panelUpdate)
			owner.With(s.requireCSRF).Post("/installation/update/check", s.checkPanelUpdate)
			owner.With(s.requireCSRF).Post("/installation/update/apply", s.applyPanelUpdate)
			owner.With(s.requireCSRF).Post("/system/containers/worker/restart", s.restartSystemWorker)
		})
	})

	router.NotFound(s.serveWeb)
	return router
}

func (s *Server) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"component": "app",
		"time":      time.Now().UTC(),
	})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.pool.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "unavailable", "database": "down"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready", "database": "up"})
}

func (s *Server) serveWeb(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.NotFound(w, r)
		return
	}
	clean := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if clean == "." || clean == "" {
		clean = "index.html"
	}
	if file, err := s.webFS.Open(clean); err == nil {
		defer file.Close()
		info, statErr := file.Stat()
		if statErr == nil && !info.IsDir() {
			if contentType := mime.TypeByExtension(path.Ext(clean)); contentType != "" {
				w.Header().Set("Content-Type", contentType)
			}
			if strings.HasPrefix(clean, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			http.ServeContent(w, r, info.Name(), info.ModTime(), readerSeeker{file})
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(s.indexHTML)
}

type readerSeeker struct {
	fs.File
}

func (r readerSeeker) Seek(offset int64, whence int) (int64, error) {
	if seeker, ok := r.File.(io.Seeker); ok {
		return seeker.Seek(offset, whence)
	}
	return 0, errors.New("embedded file is not seekable")
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeProblem(w, r, errors.Join(errBadRequest, err))
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeProblem(w, r, errors.Join(errBadRequest, errors.New("request body must contain one JSON value")))
		return false
	}
	return true
}
