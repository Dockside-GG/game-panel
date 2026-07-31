package templates

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/dockside-gg/game-panel/internal/identity"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const maxCatalogBytes = 32 << 20

var catalogVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)

type CatalogStatus struct {
	CatalogURL     string     `json:"catalog_url"`
	CatalogVersion *string    `json:"catalog_version"`
	ETag           *string    `json:"etag,omitempty"`
	GeneratedAt    *time.Time `json:"generated_at"`
	CheckedAt      *time.Time `json:"checked_at"`
	SyncedAt       *time.Time `json:"synced_at"`
	TemplateCount  int        `json:"template_count"`
	Status         string     `json:"status"`
	LastError      *string    `json:"last_error,omitempty"`
}

type CatalogSyncer struct {
	pool   *pgxpool.Pool
	url    string
	client *http.Client
	logger *slog.Logger
	mu     sync.Mutex
}

func NewCatalogSyncer(
	pool *pgxpool.Pool,
	catalogURL string,
	logger *slog.Logger,
) *CatalogSyncer {
	return &CatalogSyncer{
		pool: pool,
		url:  catalogURL,
		client: &http.Client{
			Timeout: 90 * time.Second,
		},
		logger: logger,
	}
}

func (s *CatalogSyncer) Status(ctx context.Context) (CatalogStatus, error) {
	var result CatalogStatus
	err := s.pool.QueryRow(ctx, `
		SELECT catalog_url, catalog_version, etag, generated_at, checked_at,
		       synced_at, template_count, status, last_error
		FROM template_catalog_state
		WHERE singleton
	`).Scan(
		&result.CatalogURL, &result.CatalogVersion, &result.ETag,
		&result.GeneratedAt, &result.CheckedAt, &result.SyncedAt,
		&result.TemplateCount, &result.Status, &result.LastError,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return CatalogStatus{CatalogURL: s.url, Status: "never"}, nil
	}
	return result, err
}

func (s *CatalogSyncer) Sync(ctx context.Context) (CatalogStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	current, err := s.Status(ctx)
	if err != nil {
		return CatalogStatus{}, err
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO template_catalog_state(singleton, catalog_url, status, checked_at)
		VALUES (true, $1, 'syncing', now())
		ON CONFLICT (singleton) DO UPDATE
		SET catalog_url = EXCLUDED.catalog_url, status = 'syncing',
		    checked_at = now(), last_error = NULL
	`, s.url); err != nil {
		return CatalogStatus{}, err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return s.fail(ctx, err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Dockside-Game-Panel/template-sync")
	if current.CatalogURL == s.url && current.ETag != nil {
		request.Header.Set("If-None-Match", *current.ETag)
	}
	response, err := s.client.Do(request)
	if err != nil {
		return s.fail(ctx, fmt.Errorf("download template catalog: %w", err))
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotModified {
		if _, err := s.pool.Exec(ctx, `
			UPDATE template_catalog_state
			SET status = 'current', checked_at = now(), last_error = NULL
			WHERE singleton
		`); err != nil {
			return CatalogStatus{}, err
		}
		return s.Status(ctx)
	}
	if response.StatusCode != http.StatusOK {
		return s.fail(ctx, fmt.Errorf("template catalog returned HTTP %d", response.StatusCode))
	}

	document, err := io.ReadAll(io.LimitReader(response.Body, maxCatalogBytes+1))
	if err != nil {
		return s.fail(ctx, fmt.Errorf("read template catalog: %w", err))
	}
	if len(document) > maxCatalogBytes {
		return s.fail(ctx, errors.New("template catalog exceeds 32 MiB"))
	}
	bundle, err := decodeCatalog(document)
	if err != nil {
		return s.fail(ctx, err)
	}
	entries, err := validateCatalog(bundle)
	if err != nil {
		return s.fail(ctx, err)
	}
	generatedAt, err := time.Parse(time.RFC3339, bundle.GeneratedAt)
	if err != nil {
		return s.fail(ctx, errors.New("template catalog generated_at must be RFC3339"))
	}
	etag := strings.TrimSpace(response.Header.Get("ETag"))
	if err := s.replaceCatalog(ctx, bundle, entries, generatedAt, etag); err != nil {
		return s.fail(ctx, err)
	}
	return s.Status(ctx)
}

func (s *CatalogSyncer) Run(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			status, err := s.Sync(ctx)
			if err != nil {
				s.logger.Error("template catalog synchronization failed", "error", err)
				continue
			}
			s.logger.Info(
				"template catalog synchronized",
				"version", valueOrEmpty(status.CatalogVersion),
				"templates", status.TemplateCount,
			)
		}
	}
}

func decodeCatalog(document []byte) (Bundle, error) {
	var bundle Bundle
	decoder := json.NewDecoder(bytes.NewReader(document))
	if err := decoder.Decode(&bundle); err != nil {
		return Bundle{}, fmt.Errorf("decode template catalog: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Bundle{}, errors.New("template catalog contains trailing JSON")
	}
	return bundle, nil
}

func validateCatalog(bundle Bundle) ([]TemplateEntry, error) {
	if bundle.FormatVersion != BundleFormatVersion {
		return nil, fmt.Errorf("unsupported template catalog format %d", bundle.FormatVersion)
	}
	if !catalogVersionPattern.MatchString(bundle.CatalogVersion) {
		return nil, errors.New("template catalog version must use Semantic Versioning")
	}
	if len(bundle.Templates) == 0 || len(bundle.Templates) > 2000 {
		return nil, errors.New("template catalog must contain 1-2000 templates")
	}
	result := make([]TemplateEntry, 0, len(bundle.Templates))
	seen := make(map[string]struct{}, len(bundle.Templates))
	for _, bundled := range bundle.Templates {
		if bundled.SourceKind != "dockside" {
			return nil, fmt.Errorf(
				"template %q must use source_kind dockside; the remote catalog is Dockside-native only",
				bundled.Slug,
			)
		}
		if len(bundled.SourceDocument) == 0 || len(bundled.SourceDocument) > 2<<20 {
			return nil, fmt.Errorf("template %q source document is missing or too large", bundled.Slug)
		}
		entry, err := Normalize(
			bundled.SourceKind,
			bundled.Category,
			bundled.UpstreamURL,
			bundled.SourceDocument,
		)
		if err != nil {
			return nil, fmt.Errorf("normalize catalog template %q: %w", bundled.Slug, err)
		}
		collisionSlug := len(bundled.SourceDigest) >= 10 &&
			bundled.Slug == entry.Slug+"-"+bundled.SourceDigest[:10]
		if bundled.Slug == "" || (entry.Slug != bundled.Slug && !collisionSlug) {
			return nil, fmt.Errorf("template slug %q does not match normalized slug %q", bundled.Slug, entry.Slug)
		}
		entry.Slug = bundled.Slug
		if _, exists := seen[entry.Slug]; exists {
			return nil, fmt.Errorf("duplicate template slug %q", entry.Slug)
		}
		seen[entry.Slug] = struct{}{}
		if entry.Description == "" {
			entry.Description = bundled.Description
			entry.CanonicalDocument.Description = bundled.Description
		}
		result = append(result, entry)
	}
	return result, nil
}

func (s *CatalogSyncer) replaceCatalog(
	ctx context.Context,
	bundle Bundle,
	entries []TemplateEntry,
	generatedAt time.Time,
	etag string,
) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		UPDATE templates
		SET archived_at = now(), updated_at = now()
		WHERE catalog_managed AND source_kind = 'dockside'
	`); err != nil {
		return err
	}
	for _, entry := range entries {
		templateID, err := identity.NewUUID()
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO templates(
				id, slug, name, category, source_kind, catalog_managed,
				upstream_url, author, description, trust_state
			)
			VALUES (
				$1, $2, $3, $4, $5, true, NULLIF($6, ''),
				NULLIF($7, ''), $8, 'curated'
			)
			ON CONFLICT (slug) DO UPDATE SET
				name = EXCLUDED.name,
				category = EXCLUDED.category,
				source_kind = EXCLUDED.source_kind,
				upstream_url = EXCLUDED.upstream_url,
				author = EXCLUDED.author,
				description = EXCLUDED.description,
				trust_state = 'curated',
				archived_at = NULL,
				updated_at = now()
			WHERE templates.catalog_managed
			RETURNING id
		`, templateID, entry.Slug, entry.Name, entry.Category, entry.SourceKind,
			entry.UpstreamURL, entry.Author, entry.Description,
		).Scan(&templateID)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("catalog slug %q conflicts with a local Dockside template", entry.Slug)
		}
		if err != nil {
			return fmt.Errorf("upsert catalog template %q: %w", entry.Slug, err)
		}
		var exists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM template_versions
				WHERE template_id = $1 AND source_digest = $2
			)
		`, templateID, entry.SourceDigest).Scan(&exists); err != nil {
			return err
		}
		if exists {
			if _, err := tx.Exec(ctx, `
				UPDATE template_versions
				SET catalog_version = $3
				WHERE template_id = $1 AND source_digest = $2
			`, templateID, entry.SourceDigest, bundle.CatalogVersion); err != nil {
				return err
			}
			continue
		}
		versionID, err := identity.NewUUID()
		if err != nil {
			return err
		}
		canonical, err := json.Marshal(entry.CanonicalDocument)
		if err != nil {
			return err
		}
		report, err := json.Marshal(entry.CompatibilityReport)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO template_versions(
				id, template_id, version, api_version, source_format,
				source_digest, catalog_version, source_document,
				canonical_document, compatibility_report
			)
			SELECT $1, $2, COALESCE(MAX(version), 0) + 1, $3, $4,
			       $5, $6, $7, $8, $9
			FROM template_versions
			WHERE template_id = $2
		`, versionID, templateID, entry.CanonicalDocument.APIVersion, entry.SourceKind,
			entry.SourceDigest, bundle.CatalogVersion, entry.SourceDocument, canonical, report,
		); err != nil {
			return fmt.Errorf("insert catalog template version %q: %w", entry.Slug, err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO template_catalog_state(
			singleton, catalog_url, catalog_version, etag, generated_at,
			checked_at, synced_at, template_count, status, last_error
		)
		VALUES (true, $1, $2, NULLIF($3, ''), $4, now(), now(), $5, 'current', NULL)
		ON CONFLICT (singleton) DO UPDATE SET
			catalog_url = EXCLUDED.catalog_url,
			catalog_version = EXCLUDED.catalog_version,
			etag = EXCLUDED.etag,
			generated_at = EXCLUDED.generated_at,
			checked_at = EXCLUDED.checked_at,
			synced_at = EXCLUDED.synced_at,
			template_count = EXCLUDED.template_count,
			status = 'current',
			last_error = NULL
	`, s.url, bundle.CatalogVersion, etag, generatedAt, len(entries)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *CatalogSyncer) fail(ctx context.Context, syncErr error) (CatalogStatus, error) {
	if ctx.Err() == nil {
		detail := syncErr.Error()
		if len(detail) > 2000 {
			detail = detail[:2000]
		}
		_, _ = s.pool.Exec(ctx, `
			INSERT INTO template_catalog_state(
				singleton, catalog_url, checked_at, status, last_error
			)
			VALUES (true, $1, now(), 'failed', $2)
			ON CONFLICT (singleton) DO UPDATE SET
				catalog_url = EXCLUDED.catalog_url,
				checked_at = now(),
				status = 'failed',
				last_error = EXCLUDED.last_error
		`, s.url, detail)
	}
	return CatalogStatus{}, syncErr
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
