package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/dockside-gg/game-panel/internal/identity"
	"github.com/dockside-gg/game-panel/internal/templates"
	"github.com/jackc/pgx/v5"
)

type TemplateSummary struct {
	ID                   string          `json:"id"`
	VersionID            string          `json:"version_id"`
	Slug                 string          `json:"slug"`
	Name                 string          `json:"name"`
	Category             string          `json:"category"`
	SourceKind           string          `json:"source_kind"`
	Author               *string         `json:"author"`
	Description          string          `json:"description"`
	TrustState           string          `json:"trust_state"`
	Version              int             `json:"version"`
	DefaultImage         string          `json:"default_image"`
	VariableCount        int             `json:"variable_count"`
	Compatibility        json.RawMessage `json:"compatibility_report"`
	DerivedFromVersionID *string         `json:"derived_from_version_id,omitempty"`
	CatalogManaged       bool            `json:"catalog_managed"`
	CatalogVersion       *string         `json:"catalog_version,omitempty"`
}

func (s *Store) ForkTemplate(
	ctx context.Context,
	actorID, parentVersionID string,
	entry templates.TemplateEntry,
) (TemplateDetail, error) {
	item, err := s.ImportCustomTemplate(ctx, actorID, entry)
	if err != nil {
		return TemplateDetail{}, err
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE templates
		SET derived_from_version_id = COALESCE(derived_from_version_id, $2)
		WHERE id = $1 AND source_kind = 'dockside' AND NOT catalog_managed
	`, item.ID, parentVersionID); err != nil {
		return TemplateDetail{}, err
	}
	if err := s.AddAudit(
		ctx, actorID, "template.fork", "template", item.ID,
		"", nil, "", map[string]any{"derived_from_version_id": parentVersionID},
	); err != nil {
		return TemplateDetail{}, err
	}
	return s.TemplateByVersion(ctx, item.VersionID)
}

func (s *Store) ImportCustomTemplate(
	ctx context.Context,
	actorID string,
	entry templates.TemplateEntry,
) (TemplateDetail, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return TemplateDetail{}, err
	}
	defer tx.Rollback(ctx)
	slug := entry.Slug
	var templateID, existingKind string
	var catalogManaged bool
	err = tx.QueryRow(ctx, `
		SELECT id, source_kind, catalog_managed
		FROM templates WHERE slug = $1 FOR UPDATE
	`, slug).Scan(&templateID, &existingKind, &catalogManaged)
	if errors.Is(err, pgx.ErrNoRows) {
		templateID, err = identity.NewUUID()
		if err != nil {
			return TemplateDetail{}, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO templates(
				id, slug, name, category, source_kind, author, description, trust_state
			) VALUES ($1, $2, $3, $4, 'dockside', NULLIF($5, ''), $6, 'community')
		`, templateID, slug, entry.Name, entry.Category, entry.Author, entry.Description); err != nil {
			return TemplateDetail{}, err
		}
	} else if err != nil {
		return TemplateDetail{}, err
	} else if existingKind != "dockside" || catalogManaged {
		slug += "-" + entry.SourceDigest[:10]
		templateID, err = identity.NewUUID()
		if err != nil {
			return TemplateDetail{}, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO templates(
				id, slug, name, category, source_kind, author, description, trust_state
			) VALUES ($1, $2, $3, $4, 'dockside', NULLIF($5, ''), $6, 'community')
		`, templateID, slug, entry.Name, entry.Category, entry.Author, entry.Description); err != nil {
			return TemplateDetail{}, err
		}
	} else {
		if _, err := tx.Exec(ctx, `
			UPDATE templates
			SET name = $2, category = $3, author = NULLIF($4, ''),
			    description = $5, archived_at = NULL, updated_at = now()
			WHERE id = $1
		`, templateID, entry.Name, entry.Category, entry.Author, entry.Description); err != nil {
			return TemplateDetail{}, err
		}
	}
	var existingVersionID string
	err = tx.QueryRow(ctx, `
		SELECT id FROM template_versions
		WHERE template_id = $1 AND source_digest = $2
	`, templateID, entry.SourceDigest).Scan(&existingVersionID)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return TemplateDetail{}, err
		}
		return s.TemplateByVersion(ctx, existingVersionID)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return TemplateDetail{}, err
	}
	versionID, err := identity.NewUUID()
	if err != nil {
		return TemplateDetail{}, err
	}
	var version int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(max(version), 0) + 1
		FROM template_versions WHERE template_id = $1
	`, templateID).Scan(&version); err != nil {
		return TemplateDetail{}, err
	}
	canonical, err := json.Marshal(entry.CanonicalDocument)
	if err != nil {
		return TemplateDetail{}, err
	}
	compatibility, err := json.Marshal(entry.CompatibilityReport)
	if err != nil {
		return TemplateDetail{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO template_versions(
			id, template_id, version, api_version, source_format, source_digest,
			source_document, canonical_document, compatibility_report, created_by
		) VALUES (
			$1, $2, $3, 'dockside.gg/templates/v1', 'compatible-json', $4,
			$5, $6, $7, $8
		)
	`, versionID, templateID, version, entry.SourceDigest, entry.SourceDocument,
		canonical, compatibility, actorID); err != nil {
		return TemplateDetail{}, err
	}
	if err := insertAudit(
		ctx, tx, actorID, "template.import", "template", templateID,
		map[string]any{"version_id": versionID, "source_digest": entry.SourceDigest},
	); err != nil {
		return TemplateDetail{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return TemplateDetail{}, err
	}
	return s.TemplateByVersion(ctx, versionID)
}

func (s *Store) ArchiveCustomTemplate(
	ctx context.Context,
	actorID, templateID, confirmName string,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var name, sourceKind string
	if err := tx.QueryRow(ctx, `
		SELECT name, source_kind
		FROM templates
		WHERE id = $1 AND archived_at IS NULL
		FOR UPDATE
	`, templateID).Scan(&name, &sourceKind); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	var catalogManaged bool
	if err := tx.QueryRow(ctx, `
		SELECT catalog_managed FROM templates WHERE id = $1
	`, templateID).Scan(&catalogManaged); err != nil {
		return err
	}
	if sourceKind != "dockside" || catalogManaged || confirmName != name {
		return ErrConflict
	}
	var inUse bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM servers AS server
			JOIN template_versions AS version ON version.id = server.template_version_id
			WHERE version.template_id = $1 AND server.deleted_at IS NULL
		)
	`, templateID).Scan(&inUse); err != nil {
		return err
	}
	if inUse {
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, `
		UPDATE templates SET archived_at = now(), updated_at = now() WHERE id = $1
	`, templateID); err != nil {
		return err
	}
	if err := insertAudit(
		ctx, tx, actorID, "template.archive", "template", templateID, nil,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type TemplateDetail struct {
	TemplateSummary
	CanonicalDocument json.RawMessage `json:"canonical_document"`
	SourceDocument    json.RawMessage `json:"source_document"`
}

func (s *Store) ListTemplates(ctx context.Context, search, category, source string, limit, offset int) ([]TemplateSummary, int64, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.pool.Query(ctx, `
		WITH latest AS (
			SELECT DISTINCT ON (template_id)
				id, template_id, version, catalog_version, canonical_document,
				compatibility_report
			FROM template_versions
			ORDER BY template_id, version DESC
		)
		SELECT
			t.id, latest.id, t.slug, t.name, t.category, t.source_kind,
			t.author, t.description, t.trust_state, latest.version,
			COALESCE(latest.canonical_document->>'default_image', ''),
			COALESCE(jsonb_array_length(latest.canonical_document->'variables'), 0),
			latest.compatibility_report, t.derived_from_version_id,
			t.catalog_managed, latest.catalog_version,
			count(*) OVER()
		FROM templates t
		JOIN latest ON latest.template_id = t.id
		WHERE t.archived_at IS NULL
		  AND ($1 = '' OR t.name ILIKE '%' || $1 || '%' OR t.description ILIKE '%' || $1 || '%')
		  AND ($2 = '' OR t.category = $2)
		  AND ($3 = '' OR t.source_kind = $3)
		ORDER BY t.category, t.name, t.source_kind
		LIMIT $4 OFFSET $5
	`, search, category, source, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list templates: %w", err)
	}
	defer rows.Close()
	result := make([]TemplateSummary, 0, limit)
	var total int64
	for rows.Next() {
		var item TemplateSummary
		if err := rows.Scan(
			&item.ID, &item.VersionID, &item.Slug, &item.Name, &item.Category,
			&item.SourceKind, &item.Author, &item.Description, &item.TrustState,
			&item.Version, &item.DefaultImage, &item.VariableCount,
			&item.Compatibility, &item.DerivedFromVersionID,
			&item.CatalogManaged, &item.CatalogVersion, &total,
		); err != nil {
			return nil, 0, fmt.Errorf("scan template: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate templates: %w", err)
	}
	return result, total, nil
}

func (s *Store) TemplateByVersion(ctx context.Context, versionID string) (TemplateDetail, error) {
	var item TemplateDetail
	err := s.pool.QueryRow(ctx, `
		SELECT
			t.id, v.id, t.slug, t.name, t.category, t.source_kind,
			t.author, t.description, t.trust_state, v.version,
			COALESCE(v.canonical_document->>'default_image', ''),
			COALESCE(jsonb_array_length(v.canonical_document->'variables'), 0),
			v.compatibility_report, t.derived_from_version_id,
			t.catalog_managed, v.catalog_version,
			v.canonical_document, v.source_document
		FROM template_versions v
		JOIN templates t ON t.id = v.template_id
		WHERE v.id = $1 AND t.archived_at IS NULL
	`, versionID).Scan(
		&item.ID, &item.VersionID, &item.Slug, &item.Name, &item.Category,
		&item.SourceKind, &item.Author, &item.Description, &item.TrustState,
		&item.Version, &item.DefaultImage, &item.VariableCount,
		&item.Compatibility, &item.DerivedFromVersionID,
		&item.CatalogManaged, &item.CatalogVersion,
		&item.CanonicalDocument, &item.SourceDocument,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, ErrNotFound
	}
	if err != nil {
		return item, fmt.Errorf("get template version: %w", err)
	}
	return item, nil
}

func (s *Store) TemplateFacets(ctx context.Context) ([]string, map[string]int64, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT category, source_kind, count(*)
		FROM templates
		WHERE archived_at IS NULL
		GROUP BY category, source_kind
		ORDER BY category, source_kind
	`)
	if err != nil {
		return nil, nil, fmt.Errorf("list template facets: %w", err)
	}
	defer rows.Close()
	categorySet := make(map[string]struct{})
	sources := make(map[string]int64)
	for rows.Next() {
		var category, source string
		var count int64
		if err := rows.Scan(&category, &source, &count); err != nil {
			return nil, nil, err
		}
		categorySet[category] = struct{}{}
		sources[source] += count
	}
	categories := make([]string, 0, len(categorySet))
	for category := range categorySet {
		categories = append(categories, category)
	}
	sort.Strings(categories)
	return categories, sources, rows.Err()
}
