package templates

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dockside-gg/game-panel/internal/identity"
	"github.com/dockside-gg/game-panel/templates/library"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func LoadBundle() (Bundle, error) {
	document, err := library.Catalog()
	if err != nil {
		return Bundle{}, err
	}
	var bundle Bundle
	if err := json.Unmarshal(document, &bundle); err != nil {
		return Bundle{}, fmt.Errorf("decode embedded template catalog: %w", err)
	}
	if bundle.FormatVersion != BundleFormatVersion {
		return Bundle{}, fmt.Errorf("unsupported embedded template catalog format %d", bundle.FormatVersion)
	}
	if len(bundle.Templates) == 0 {
		return Bundle{}, fmt.Errorf("embedded template catalog is empty")
	}
	for index, bundled := range bundle.Templates {
		normalized, err := Normalize(
			bundled.SourceKind,
			bundled.Category,
			bundled.UpstreamURL,
			bundled.SourceDocument,
		)
		if err != nil {
			return Bundle{}, fmt.Errorf("normalize embedded template %s: %w", bundled.Slug, err)
		}
		if normalized.Description == "" {
			normalized.Description = bundled.Description
			normalized.CanonicalDocument.Description = bundled.Description
		}
		bundle.Templates[index] = normalized
	}
	return bundle, nil
}

func Seed(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	bundle, err := LoadBundle()
	if err != nil {
		return 0, err
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("begin template seed: %w", err)
	}
	defer tx.Rollback(ctx)

	for _, entry := range bundle.Templates {
		templateID, err := identity.NewUUID()
		if err != nil {
			return 0, err
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO templates(
				id, slug, name, category, source_kind, catalog_managed, upstream_url,
				author, description, trust_state
			)
			VALUES ($1, $2, $3, $4, $5, false, NULLIF($6, ''), NULLIF($7, ''), $8, 'curated')
			ON CONFLICT (slug) DO UPDATE SET
				name = EXCLUDED.name,
				category = EXCLUDED.category,
				source_kind = EXCLUDED.source_kind,
				catalog_managed = false,
				upstream_url = EXCLUDED.upstream_url,
				author = EXCLUDED.author,
				description = EXCLUDED.description,
				archived_at = NULL,
				updated_at = now()
			WHERE NOT templates.catalog_managed
			RETURNING id
		`, templateID, entry.Slug, entry.Name, entry.Category, entry.SourceKind,
			entry.UpstreamURL, entry.Author, entry.Description,
		).Scan(&templateID); err != nil {
			return 0, fmt.Errorf("upsert template %s: %w", entry.Slug, err)
		}

		var exists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM template_versions
				WHERE template_id = $1 AND source_digest = $2
			)
		`, templateID, entry.SourceDigest).Scan(&exists); err != nil {
			return 0, fmt.Errorf("check template version %s: %w", entry.Slug, err)
		}
		if exists {
			continue
		}
		versionID, err := identity.NewUUID()
		if err != nil {
			return 0, err
		}
		canonical, err := json.Marshal(entry.CanonicalDocument)
		if err != nil {
			return 0, fmt.Errorf("encode canonical template %s: %w", entry.Slug, err)
		}
		report, err := json.Marshal(entry.CompatibilityReport)
		if err != nil {
			return 0, fmt.Errorf("encode compatibility report %s: %w", entry.Slug, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO template_versions(
				id, template_id, version, api_version, source_format,
				source_digest, source_document, canonical_document, compatibility_report
			)
			SELECT $1, $2, COALESCE(MAX(version), 0) + 1, $3, $4, $5, $6, $7, $8
			FROM template_versions
			WHERE template_id = $2
		`, versionID, templateID, entry.CanonicalDocument.APIVersion, entry.SourceKind,
			entry.SourceDigest, entry.SourceDocument, canonical, report,
		); err != nil {
			return 0, fmt.Errorf("insert template version %s: %w", entry.Slug, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit template seed: %w", err)
	}
	return len(bundle.Templates), nil
}
