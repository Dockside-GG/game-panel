import {
  Activity,
  AlertTriangle,
  Archive,
  Boxes,
  ChevronRight,
  CircleStop,
  Clock3,
  Copy,
  Cpu,
  Database,
  Download,
  Eye,
  File,
  FileArchive,
  FilePlus,
  Files,
  Folder,
  FolderPlus,
  Gauge,
  HardDrive,
  Info,
  KeyRound,
  Library,
  LockKeyhole,
  MemoryStick,
  Network,
  Play,
  Pencil,
  Plus,
  RefreshCw,
  RotateCw,
  Save,
  Search,
  Server,
  Settings,
  Skull,
  SquareTerminal,
  Trash2,
  UnlockKeyhole,
  Upload,
  Variable,
  Webhook,
  X,
} from "lucide-react";
import { FormEvent, ReactNode, useEffect, useRef, useState } from "react";
import {
  Link,
  NavLink,
  useNavigate,
  useParams,
  useSearchParams,
} from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./api";
import {
  EmptyState,
  ErrorPanel,
  PageHeader,
  StatusBadge,
} from "./components";
import type {
  ActivityEvent,
  ServerSummary,
  ServerFileContent,
  ServerFileList,
  ServerBackup,
  ServerSchedule,
  ServerWebhook,
  ServerConfiguration,
  ServerDatabase,
  ServerPort,
  Session,
  TemplateDetail,
  TemplateNetworkPort,
  TemplateSummary,
  TemplateVariable,
  CommandTransport,
} from "./types";
import { serverPresentation } from "./server-presentation";

type TemplateListResponse = {
  templates: TemplateSummary[];
  total: number;
  limit: number;
  offset: number;
};

function FieldHelp({ text }: { text: string }) {
  return <span className="field-help" tabIndex={0} aria-label={text}><Info size={12} /><span role="tooltip">{text}</span></span>;
}

export function TemplateLibraryPage() {
  const [search, setSearch] = useState("");
  const [category, setCategory] = useState("");
  const [source, setSource] = useState("");
  const [showImport, setShowImport] = useState(false);
  const navigate = useNavigate();
  const session = useQuery({
    queryKey: ["session"],
    queryFn: () => api<Session>("/api/v1/session"),
  });
  const canEditTemplates = session.data?.user.panel_role === "owner" ||
    session.data?.user.panel_role === "administrator";
  const facets = useQuery({
    queryKey: ["template-facets"],
    queryFn: () =>
      api<{ categories: string[]; sources: Record<string, number> }>(
        "/api/v1/templates/facets",
      ),
  });
  const templates = useQuery({
    queryKey: ["templates", search, category, source],
    queryFn: () => {
      const params = new URLSearchParams({ limit: "100" });
      if (search) params.set("search", search);
      if (category) params.set("category", category);
      if (source) params.set("source", source);
      return api<TemplateListResponse>(`/api/v1/templates?${params}`);
    },
  });

  return (
    <>
      <PageHeader
        eyebrow="BUNDLED TEMPLATE LIBRARY"
        title="Templates"
        description="Pelican and Pterodactyl-compatible templates bundled with this panel. No catalog download is required."
        actions={canEditTemplates ? <div className="page-actions"><Link className="button primary" to="/templates/new"><Plus size={17} /> Create template</Link><button className="button secondary" onClick={() => setShowImport(true)}>Import JSON</button></div> : undefined}
      />
      <section className="catalog-stats">
        <article>
          <span className="source-logo">P</span>
          <div>
            <strong>{facets.data?.sources.pelican ?? 321}</strong>
            <span>Pelican templates</span>
          </div>
          <StatusBadge tone="info">Bundled</StatusBadge>
        </article>
        <article>
          <span className="source-logo orange">PT</span>
          <div>
            <strong>{facets.data?.sources.pterodactyl ?? 300}</strong>
            <span>Pterodactyl templates</span>
          </div>
          <StatusBadge tone="info">Bundled</StatusBadge>
        </article>
      </section>
      <section className="catalog-toolbar">
        <label className="search-field">
          <Search size={17} />
          <input
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="Search games and software"
            aria-label="Search templates"
          />
        </label>
        <select
          aria-label="Filter by category"
          value={category}
          onChange={(event) => setCategory(event.target.value)}
        >
          <option value="">All categories</option>
          {facets.data?.categories.map((item) => (
            <option value={item} key={item}>
              {item}
            </option>
          ))}
        </select>
        <select
          aria-label="Filter by source"
          value={source}
          onChange={(event) => setSource(event.target.value)}
        >
          <option value="">Both sources</option>
          <option value="pelican">Pelican</option>
          <option value="pterodactyl">Pterodactyl</option>
        </select>
      </section>
      {templates.isError ? (
        <ErrorPanel
          error={templates.error}
          retry={() => void templates.refetch()}
        />
      ) : templates.isLoading ? (
        <div className="template-grid loading-cards">
          {Array.from({ length: 8 }, (_, index) => <span key={index} />)}
        </div>
      ) : templates.data?.templates.length ? (
        <>
          <div className="template-grid">
            {templates.data.templates.map((template) => (
              <TemplateCard
                template={template}
                key={template.version_id}
                onPreview={() => void navigate(`/templates/${template.version_id}`)}
              />
            ))}
          </div>
          {templates.data.total > templates.data.templates.length && (
            <p className="catalog-limit">
              Showing the first {templates.data.templates.length} of{" "}
              {templates.data.total}. Refine your search to narrow the catalog.
            </p>
          )}
        </>
      ) : (
        <section className="panel">
          <EmptyState
            icon={Search}
            title="No matching templates"
            description="Try a different game, category, or source filter."
          />
        </section>
      )}
      {showImport && <TemplateImportDialog onClose={() => setShowImport(false)} />}
    </>
  );
}

function TemplateCard({
  template,
  onPreview,
}: {
  template: TemplateSummary;
  onPreview: () => void;
}) {
  const queryClient = useQueryClient();
  const session = useQuery({
    queryKey: ["session"],
    queryFn: () => api<Session>("/api/v1/session"),
  });
  const canEdit = session.data?.user.panel_role === "owner" ||
    session.data?.user.panel_role === "administrator";
  const remove = useMutation({
    mutationFn: (confirmName: string) =>
      api<void>(`/api/v1/templates/${template.id}`, {
        method: "DELETE",
        body: JSON.stringify({ confirm_name: confirmName }),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["templates"] });
      void queryClient.invalidateQueries({ queryKey: ["template-facets"] });
    },
  });
  return (
    <article className="template-card">
      <div className="template-card-top">
        <span className={`template-source-pill ${template.source_kind}`}>
          {template.source_kind === "pelican"
            ? "Pelican compatible"
            : template.source_kind === "pterodactyl"
              ? "Pterodactyl compatible"
              : "Custom template"}
        </span>
      </div>
      <span className="template-category">{template.category}</span>
      <h3>{template.name}</h3>
      <p>{template.description || "No description supplied by the template author."}</p>
      <div className="template-meta">
        <span><Boxes size={13} /> v{template.version}</span>
        <span><Variable size={13} /> {template.variable_count} variables</span>
      </div>
      <div className="template-card-actions">
        <button className="button ghost" type="button" onClick={onPreview}>
          <Eye size={15} /> Preview
        </button>
        <Link
          className="button secondary"
          to={`/servers/new?template=${template.version_id}`}
        >
          Use template <ChevronRight size={16} />
        </Link>
      </div>
      {template.source_kind === "custom" && canEdit && (
        <button
          className="button danger wide"
          disabled={remove.isPending}
          onClick={() => {
            const confirmation = window.prompt(`Type “${template.name}” to remove this custom template.`);
            if (confirmation !== null) remove.mutate(confirmation);
          }}
        ><Trash2 size={14} /> Remove custom template</button>
      )}
      {remove.isError && <div className="form-error">{remove.error.message}</div>}
    </article>
  );
}

export function TemplatePreviewDialog({
  versionID,
  onClose,
}: {
  versionID: string;
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const session = useQuery({
    queryKey: ["session"],
    queryFn: () => api<Session>("/api/v1/session"),
  });
  const canEdit = session.data?.user.panel_role === "owner" ||
    session.data?.user.panel_role === "administrator";
  const detail = useQuery({
    queryKey: ["template", versionID],
    queryFn: () => api<TemplateDetail>(`/api/v1/templates/${versionID}`),
  });
  const [editing, setEditing] = useState(false);
  const [category, setCategory] = useState("");
  const [document, setDocument] = useState("");
  const save = useMutation({
    mutationFn: () => {
      let parsed: unknown;
      try {
        parsed = JSON.parse(document);
      } catch {
        throw new Error("The template document is not valid JSON.");
      }
      return api<TemplateDetail>(
        `/api/v1/templates/${versionID}/fork`,
        {
          method: "POST",
          body: JSON.stringify({ category: category.trim(), document: parsed }),
        },
      );
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["templates"] });
      void queryClient.invalidateQueries({ queryKey: ["template-facets"] });
      onClose();
    },
  });
  const item = detail.data;
  return (
    <div className="dialog-backdrop">
      <div className="dialog template-preview-dialog" role="dialog" aria-modal="true">
        <div className="dialog-title-row">
          <div>
            <span className={`template-source-pill ${item?.source_kind ?? ""}`}>
              {item?.source_kind === "pelican"
                ? "Pelican compatible"
                : item?.source_kind === "pterodactyl"
                  ? "Pterodactyl compatible"
                  : "Dockside custom"}
            </span>
            <h2>{item?.name ?? "Template preview"}</h2>
          </div>
          <button className="icon-button" type="button" onClick={onClose} aria-label="Close template preview">
            <X size={18} />
          </button>
        </div>
        {detail.isLoading ? (
          <div className="wizard-loading"><span className="loader" /></div>
        ) : detail.isError || !item ? (
          <ErrorPanel error={detail.error} retry={() => void detail.refetch()} />
        ) : editing ? (
          <>
            <label>Category<input value={category} maxLength={80} onChange={(event) => setCategory(event.target.value)} /></label>
            <label>Compatible template JSON<textarea className="template-json-editor" value={document} spellCheck={false} onChange={(event) => setDocument(event.target.value)} /></label>
            <div className="notice info">
              {item.source_kind === "custom"
                ? "Saving creates a new immutable version of this custom template."
                : "Bundled templates remain unchanged. Saving creates a versioned Dockside custom copy."}
            </div>
          </>
        ) : (
          <div className="template-preview-content">
            <p>{item.description || "No description supplied."}</p>
            <dl className="detail-list">
              <div><dt>Category</dt><dd>{item.category}</dd></div>
              <div><dt>Version</dt><dd>v{item.version}</dd></div>
              <div><dt>Runtime image</dt><dd className="mono">{item.canonical_document.default_image}</dd></div>
              <div><dt>Installer image</dt><dd className="mono">{item.canonical_document.install_container || "None"}</dd></div>
              <div><dt>Stop command</dt><dd className="mono">{item.canonical_document.stop_command || "Not declared"}</dd></div>
            </dl>
            <div className="template-command-preview">
              <strong>Startup command</strong>
              <code>{item.canonical_document.startup_command}</code>
            </div>
            <div className="template-variable-preview">
              <strong>Published network defaults</strong>
              {(item.canonical_document.network_ports ?? []).map((port, index) => (
                <div key={`${port.environment ?? port.name}-${index}`}>
                  <span>{port.name}{port.primary ? " · Primary" : ""}</span>
                  <small>
                    {port.container_port || "Installer chooses"}/{port.protocol || "protocol required"}
                    {" · "}{port.published ? "Published" : "Internal by default"}
                    {port.environment ? ` · ${port.environment}` : ""}
                  </small>
                </div>
              ))}
            </div>
            <div className="template-variable-preview">
              <strong>Variables</strong>
              {item.canonical_document.variables.map((variable) => (
                <div key={variable.environment}>
                  <span>{variable.name || variable.environment}</span>
                  <small>{variable.user_editable ? "Editable" : "Locked"} · {variable.secret ? "Secret" : variable.default_value || "No default"}</small>
                </div>
              ))}
            </div>
          </div>
        )}
        {save.isError && <div className="form-error">{save.error.message}</div>}
        <div className="dialog-actions">
          <button className="button ghost" type="button" onClick={onClose}>Close</button>
          {!editing && canEdit ? (
            <button
              className="button secondary"
              type="button"
              onClick={() => {
                if (item) {
                  setCategory(item.category);
                  setDocument(JSON.stringify(item.source_document, null, 2));
                }
                setEditing(true);
              }}
            >
              <Pencil size={15} /> {item?.source_kind === "custom" ? "Edit template" : "Customize template"}
            </button>
          ) : editing ? (
            <>
              <button className="button secondary" type="button" onClick={() => setEditing(false)}>Cancel edit</button>
              <button className="button primary" type="button" disabled={save.isPending || !category.trim()} onClick={() => save.mutate()}>
                {save.isPending ? "Saving…" : "Save new version"}
              </button>
            </>
          ) : null}
        </div>
      </div>
    </div>
  );
}

export function TemplateDetailPage() {
  const { versionID = "" } = useParams();
  const [params] = useSearchParams();
  const detail = useQuery({
    queryKey: ["template", versionID],
    queryFn: () => api<TemplateDetail>(`/api/v1/templates/${versionID}`),
  });
  const session = useQuery({
    queryKey: ["session"],
    queryFn: () => api<Session>("/api/v1/session"),
  });
  if (detail.isLoading) return <div className="wizard-loading"><span className="loader" /></div>;
  if (detail.isError || !detail.data) {
    return <ErrorPanel error={detail.error} retry={() => void detail.refetch()} />;
  }
  const item = detail.data;
  const fromServer = params.get("fromServer");
  const canEdit = ["owner", "administrator"].includes(session.data?.user.panel_role ?? "");
  return (
    <>
      <nav className="breadcrumbs" aria-label="Breadcrumb">
        <Link to="/templates">Templates</Link><ChevronRight size={13} />
        <span>{item.category}</span><ChevronRight size={13} />
        <strong>{item.name}</strong>
      </nav>
      <PageHeader
        eyebrow="TEMPLATE SPECIFICATION"
        title={item.name}
        description={item.description || "No description supplied by the template author."}
        actions={<div className="page-actions">
          {fromServer && <Link className="button ghost" to={`/servers/${fromServer}`}>Return to server</Link>}
          {canEdit && <Link className="button secondary" to={`/templates/${item.version_id}/edit`}><Pencil size={15} /> {item.source_kind === "custom" ? "Edit template" : "Customize template"}</Link>}
          <Link className="button primary" to={`/servers/new?template=${item.version_id}`}>Use template</Link>
        </div>}
      />
      <section className="template-detail-grid">
        <article className="panel">
          <div className="panel-heading"><div><span className="eyebrow">RUNTIME</span><h2>Container and startup</h2></div></div>
          <dl className="detail-list">
            <div><dt>Compatibility</dt><dd>{item.source_kind === "custom" ? "Dockside" : `${item.source_kind} compatible`}</dd></div>
            <div><dt>Version</dt><dd>{item.version}</dd></div>
            <div><dt>Runtime image</dt><dd className="mono">{item.canonical_document.default_image}</dd></div>
            <div><dt>Installer image</dt><dd className="mono">{item.canonical_document.install_container || "None"}</dd></div>
            <div><dt>Command transport</dt><dd>{item.canonical_document.command_transport?.type || "auto"}</dd></div>
          </dl>
          <div className="template-command-preview"><strong>Startup command</strong><code>{item.canonical_document.startup_command}</code></div>
          <div className="template-command-preview"><strong>Stop command</strong><code>{item.canonical_document.stop_command || "Not declared"}</code></div>
        </article>
        <article className="panel">
          <div className="panel-heading"><div><span className="eyebrow">DEFAULTS</span><h2>Backup and resources</h2></div></div>
          <dl className="detail-list">
            <div><dt>Backup includes</dt><dd>{item.canonical_document.backup_defaults?.include_paths?.join(", ") || "Everything"}</dd></div>
            <div><dt>Backup excludes</dt><dd>{item.canonical_document.backup_defaults?.exclude_globs?.join(", ") || "None"}</dd></div>
            <div><dt>Retention</dt><dd>{item.canonical_document.backup_defaults?.retention_days ? `${item.canonical_document.backup_defaults.retention_days} days` : "Indefinite"}</dd></div>
            <div><dt>CPU</dt><dd>{item.canonical_document.resource_defaults?.cpu_limit_millicores ? `${item.canonical_document.resource_defaults.cpu_limit_millicores} millicores` : "Unlimited"}</dd></div>
            <div><dt>Memory</dt><dd>{item.canonical_document.resource_defaults?.memory_limit_mb ? `${item.canonical_document.resource_defaults.memory_limit_mb} MB` : "Unlimited"}</dd></div>
          </dl>
        </article>
      </section>
      <section className="panel">
        <div className="panel-heading"><div><span className="eyebrow">PROVISIONING INPUTS</span><h2>Variables and network allocations</h2></div></div>
        <div className="template-preview-columns">
          <div className="template-variable-preview">
            <strong>Variables</strong>
            {item.canonical_document.variables.map((variable) => <div key={variable.environment}><span>{variable.name || variable.environment}</span><small>{variable.environment} · {variable.secret ? "Secret" : variable.default_value || "No default"}</small></div>)}
          </div>
          <div className="template-variable-preview">
            <strong>Network</strong>
            {(item.canonical_document.network_ports ?? []).map((port, index) => <div key={`${port.name}-${index}`}><span>{port.name}{port.primary ? " · Primary" : ""}</span><small>{port.container_port}/{port.protocol} · {port.environment || "No variable"}</small></div>)}
          </div>
        </div>
      </section>
    </>
  );
}

type TemplateEditorDraft = {
  name: string;
  author: string;
  description: string;
  category: string;
  images: string;
  startup: string;
  stop: string;
  installContainer: string;
  installEntrypoint: string;
  installScript: string;
  transport: CommandTransport;
  backupIncludes: string;
  backupExcludes: string;
  retention: string;
  cpu: string;
  memory: string;
  disk: string;
  variables: TemplateVariable[];
  ports: TemplateNetworkPort[];
};

const blankTemplateDraft: TemplateEditorDraft = {
  name: "", author: "Dockside panel owner", description: "", category: "Custom",
  images: "Default=ghcr.io/dockside-gg/game-server-base:latest",
  startup: "./server {{SERVER_PORT}}", stop: "stop",
  installContainer: "alpine:3.22", installEntrypoint: "sh", installScript: "# Install game files into /mnt/server",
  transport: { type: "stdin" }, backupIncludes: "", backupExcludes: "logs/\n*.log",
  retention: "", cpu: "", memory: "", disk: "",
  variables: [], ports: [{ name: "Game", purpose: "Primary game traffic", container_port: 25565, protocol: "tcp", primary: true, required: true, published: true, environment: "SERVER_PORT" }],
};

export function TemplateEditorPage({ create = false }: { create?: boolean }) {
  const { versionID = "" } = useParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const detail = useQuery({
    queryKey: ["template", versionID],
    queryFn: () => api<TemplateDetail>(`/api/v1/templates/${versionID}`),
    enabled: !create,
  });
  const [draft, setDraft] = useState<TemplateEditorDraft>(blankTemplateDraft);
  const [loadedVersion, setLoadedVersion] = useState("");
  if (detail.data && loadedVersion !== detail.data.version_id) {
    const item = detail.data;
    setLoadedVersion(item.version_id);
    setDraft({
      name: item.name, author: item.author || "Dockside panel owner",
      description: item.description, category: item.category,
      images: Object.entries(item.canonical_document.images).map(([name, image]) => `${name}=${image}`).join("\n"),
      startup: item.canonical_document.startup_command,
      stop: item.canonical_document.stop_command || "",
      installContainer: item.canonical_document.install_container || "",
      installEntrypoint: item.canonical_document.install_entrypoint || "sh",
      installScript: item.canonical_document.install_script || "",
      transport: item.canonical_document.command_transport || { type: "auto" },
      backupIncludes: item.canonical_document.backup_defaults?.include_paths?.join("\n") || "",
      backupExcludes: item.canonical_document.backup_defaults?.exclude_globs?.join("\n") || "",
      retention: item.canonical_document.backup_defaults?.retention_days?.toString() || "",
      cpu: item.canonical_document.resource_defaults?.cpu_limit_millicores?.toString() || "",
      memory: item.canonical_document.resource_defaults?.memory_limit_mb?.toString() || "",
      disk: item.canonical_document.resource_defaults?.disk_alert_limit_mb?.toString() || "",
      variables: item.canonical_document.variables,
      ports: item.canonical_document.network_ports,
    });
  }
  const set = <K extends keyof TemplateEditorDraft>(key: K, value: TemplateEditorDraft[K]) =>
    setDraft((current) => ({ ...current, [key]: value }));
  const save = useMutation({
    mutationFn: () => {
      const images = Object.fromEntries(draft.images.split(/\r?\n/).map((line) => line.trim()).filter(Boolean).map((line) => {
        const [name, ...rest] = line.split("=");
        return [(name ?? "").trim(), rest.join("=").trim()];
      }));
      const document = {
        name: draft.name.trim(), author: draft.author.trim(), description: draft.description.trim(),
        docker_images: images, startup: draft.startup, config: { stop: draft.stop },
        dockside: {
          network_ports: draft.ports,
          command_transport: draft.transport,
          backup_defaults: {
            include_paths: parseRules(draft.backupIncludes),
            exclude_globs: parseRules(draft.backupExcludes),
            retention_days: draft.retention ? Number(draft.retention) : null,
          },
          resource_defaults: {
            cpu_limit_millicores: draft.cpu ? Number(draft.cpu) : null,
            memory_limit_mb: draft.memory ? Number(draft.memory) : null,
            disk_alert_limit_mb: draft.disk ? Number(draft.disk) : null,
          },
        },
        scripts: { installation: { script: draft.installScript, container: draft.installContainer, entrypoint: draft.installEntrypoint } },
        variables: draft.variables.map((variable) => ({
          name: variable.name, description: variable.description,
          env_variable: variable.environment, default_value: variable.default_value,
          user_viewable: variable.user_viewable, user_editable: variable.user_editable,
          rules: variable.rules, field_type: variable.field_type,
          secret: variable.secret,
        })),
      };
      return api<TemplateDetail>(create ? "/api/v1/templates/import" : `/api/v1/templates/${versionID}/fork`, {
        method: "POST", body: JSON.stringify({ category: draft.category.trim(), document }),
      });
    },
    onSuccess: (result) => {
      void queryClient.invalidateQueries({ queryKey: ["templates"] });
      void queryClient.invalidateQueries({ queryKey: ["template-facets"] });
      void navigate(`/templates/${result.version_id}`);
    },
  });
  if (!create && detail.isLoading) return <div className="wizard-loading"><span className="loader" /></div>;
  if (!create && detail.isError) return <ErrorPanel error={detail.error} retry={() => void detail.refetch()} />;
  return (
    <>
      <nav className="breadcrumbs" aria-label="Breadcrumb"><Link to="/templates">Templates</Link><ChevronRight size={13} /><span>{create ? "Create" : detail.data?.name}</span><ChevronRight size={13} /><strong>Editor</strong></nav>
      <PageHeader eyebrow="DOCKSIDE TEMPLATE BUILDER" title={create ? "Create template" : `Edit ${detail.data?.name ?? "template"}`} description="Build a versioned provisioning template with Dockside extensions. Secret values are never embedded." />
      <div className="template-editor-layout">
        <section className="panel configuration-form">
          <div className="panel-heading"><div><span className="eyebrow">IDENTITY</span><h2>Template details</h2></div></div>
          <div className="form-grid two"><label>Name<input value={draft.name} onChange={(event) => set("name", event.target.value)} /></label><label>Category<input value={draft.category} onChange={(event) => set("category", event.target.value)} /></label></div>
          <label>Author<input value={draft.author} onChange={(event) => set("author", event.target.value)} /></label>
          <label>Description<textarea value={draft.description} onChange={(event) => set("description", event.target.value)} /></label>
          <label>Runtime images <span className="label-hint">One Label=image reference per line.</span><textarea className="mono" value={draft.images} onChange={(event) => set("images", event.target.value)} /></label>
        </section>
        <section className="panel configuration-form">
          <div className="panel-heading"><div><span className="eyebrow">BOOT</span><h2>Installation and startup</h2></div></div>
          <label>Startup command<textarea className="startup-editor" value={draft.startup} onChange={(event) => set("startup", event.target.value)} /></label>
          <label>Stop command<input value={draft.stop} onChange={(event) => set("stop", event.target.value)} /></label>
          <div className="form-grid two"><label>Installer image<input value={draft.installContainer} onChange={(event) => set("installContainer", event.target.value)} /></label><label>Entrypoint<input value={draft.installEntrypoint} onChange={(event) => set("installEntrypoint", event.target.value)} /></label></div>
          <label>Installation script<textarea className="startup-editor" value={draft.installScript} onChange={(event) => set("installScript", event.target.value)} /></label>
        </section>
      </div>
      <TemplateNetworkEditor ports={draft.ports} onChange={(ports) => set("ports", ports)} />
      <TemplateVariableEditor variables={draft.variables} onChange={(variables) => set("variables", variables)} />
      <div className="template-editor-layout">
        <section className="panel configuration-form">
          <div className="panel-heading"><div><span className="eyebrow">COMMANDS</span><h2>Console transport</h2></div></div>
          <label>Transport<select value={draft.transport.type} onChange={(event) => {
            const type = event.target.value as CommandTransport["type"];
            set("transport", type === "http_rest" ? { ...draft.transport, type, rest: draft.transport.rest ?? { method: "POST", port: 8080, path: "/command", body_template: "{\"command\":{{COMMAND_JSON}}}", timeout_seconds: 10 } } : { ...draft.transport, type });
          }}><option value="auto">Auto detect</option><option value="stdin">Standard input</option><option value="rcon">RCON</option><option value="http_rest">HTTP REST</option><option value="disabled">Disabled</option></select></label>
          {draft.transport.type === "rcon" && <div className="form-grid two"><label>Port variable<input value={draft.transport.rcon_port_env || "RCON_PORT"} onChange={(event) => set("transport", { ...draft.transport, rcon_port_env: event.target.value })} /></label><label>Password variable<input value={draft.transport.rcon_password_env || "ADMIN_PASSWORD"} onChange={(event) => set("transport", { ...draft.transport, rcon_password_env: event.target.value })} /></label></div>}
          {draft.transport.type === "http_rest" && <RESTTransportEditor value={draft.transport} onChange={(value) => set("transport", value)} />}
        </section>
        <section className="panel configuration-form">
          <div className="panel-heading"><div><span className="eyebrow">DEFAULTS</span><h2>Backups and resources</h2></div></div>
          <label>Backup includes<textarea value={draft.backupIncludes} onChange={(event) => set("backupIncludes", event.target.value)} placeholder="Blank includes everything" /></label>
          <label>Backup excludes<textarea value={draft.backupExcludes} onChange={(event) => set("backupExcludes", event.target.value)} /></label>
          <div className="form-grid two"><label>Retention days<input type="number" value={draft.retention} onChange={(event) => set("retention", event.target.value)} placeholder="Indefinite" /></label><label>CPU millicores<input type="number" value={draft.cpu} onChange={(event) => set("cpu", event.target.value)} placeholder="Unlimited" /></label><label>Memory MB<input type="number" value={draft.memory} onChange={(event) => set("memory", event.target.value)} placeholder="Unlimited" /></label><label>Disk alert MB<input type="number" value={draft.disk} onChange={(event) => set("disk", event.target.value)} placeholder="Unlimited" /></label></div>
        </section>
      </div>
      {save.isError && <div className="form-error">{save.error.message}</div>}
      <div className="configuration-actions"><Link className="button ghost" to={create ? "/templates" : `/templates/${versionID}`}>Cancel</Link><button className="button primary" disabled={save.isPending || !draft.name.trim() || !draft.category.trim()} onClick={() => save.mutate()}><Save size={15} /> {save.isPending ? "Validating…" : "Save immutable version"}</button></div>
    </>
  );
}

function TemplateNetworkEditor({ ports, onChange }: { ports: TemplateNetworkPort[]; onChange: (ports: TemplateNetworkPort[]) => void }) {
  const update = (index: number, value: Partial<TemplateNetworkPort>) => onChange(ports.map((port, candidate) => candidate === index ? { ...port, ...value } : port));
  return <section className="panel"><div className="panel-heading"><div><span className="eyebrow">NETWORK</span><h2>Container allocations</h2></div><button className="button secondary compact" onClick={() => onChange([...ports, { name: "Additional", purpose: "Additional game traffic", container_port: 25566, protocol: "tcp", primary: false, required: false, published: true, environment: "" }])}><Plus size={13} /> Add port</button></div><div className="template-port-list">{ports.map((port, index) => <article className="template-editor-row" key={index}><label>Name<input value={port.name} onChange={(event) => update(index, { name: event.target.value })} /></label><label>Container port<input type="number" value={port.container_port} onChange={(event) => update(index, { container_port: Number(event.target.value) })} /></label><label>Protocol<select value={port.protocol} onChange={(event) => update(index, { protocol: event.target.value as "tcp" | "udp" })}><option value="tcp">TCP</option><option value="udp">UDP</option></select></label><label>Variable<input value={port.environment || ""} onChange={(event) => update(index, { environment: event.target.value.toUpperCase() })} /></label><label className="compact-check"><input type="checkbox" checked={port.primary} onChange={(event) => onChange(ports.map((candidate, candidateIndex) => ({ ...candidate, primary: event.target.checked && candidateIndex === index, required: event.target.checked && candidateIndex === index ? true : candidate.required })))} /><span>Primary</span></label><button className="icon-button danger" disabled={ports.length === 1 || port.primary} onClick={() => onChange(ports.filter((_, candidate) => candidate !== index))}><Trash2 size={13} /></button></article>)}</div></section>;
}

function TemplateVariableEditor({ variables, onChange }: { variables: TemplateVariable[]; onChange: (variables: TemplateVariable[]) => void }) {
  const update = (index: number, value: Partial<TemplateVariable>) => onChange(variables.map((variable, candidate) => candidate === index ? { ...variable, ...value } : variable));
  return <section className="panel"><div className="panel-heading"><div><span className="eyebrow">ENVIRONMENT</span><h2>Template variables</h2></div><button className="button secondary compact" onClick={() => onChange([...variables, { name: "New variable", description: "", environment: "NEW_VARIABLE", default_value: "", user_viewable: true, user_editable: true, rules: "nullable|string", field_type: "text", secret: false }])}><Plus size={13} /> Add variable</button></div><div className="template-variable-editor">{variables.map((variable, index) => <article className="template-editor-row variable" key={`${variable.environment}-${index}`}><label>Name<input value={variable.name} onChange={(event) => update(index, { name: event.target.value })} /></label><label>Environment<input value={variable.environment} onChange={(event) => update(index, { environment: event.target.value.toUpperCase().replace(/[^A-Z0-9_]/g, "") })} /></label><label>Default<input type={variable.secret ? "password" : "text"} value={variable.default_value} onChange={(event) => update(index, { default_value: event.target.value })} /></label><label>Rules<input value={variable.rules || ""} onChange={(event) => update(index, { rules: event.target.value })} /></label><label className="compact-check"><input type="checkbox" checked={variable.user_editable} onChange={(event) => update(index, { user_editable: event.target.checked })} /><span>Editable</span></label><label className="compact-check"><input type="checkbox" checked={variable.secret} onChange={(event) => update(index, { secret: event.target.checked, field_type: event.target.checked ? "password" : variable.field_type })} /><span>Secret</span></label><button className="icon-button danger" onClick={() => onChange(variables.filter((_, candidate) => candidate !== index))}><Trash2 size={13} /></button></article>)}</div></section>;
}

function RESTTransportEditor({ value, onChange }: { value: CommandTransport; onChange: (value: CommandTransport) => void }) {
  const rest = value.rest ?? { method: "POST", port: 8080, path: "/command", body_template: "{\"command\":{{COMMAND_JSON}}}", timeout_seconds: 10 };
  const setRest = (change: Partial<NonNullable<CommandTransport["rest"]>>) => onChange({ ...value, rest: { ...rest, ...change } });
  return <><div className="form-grid two"><label>Method<select value={rest.method} onChange={(event) => setRest({ method: event.target.value as typeof rest.method })}>{["GET", "POST", "PUT", "PATCH", "DELETE"].map((method) => <option key={method}>{method}</option>)}</select></label><label>Internal port<input type="number" value={rest.port} onChange={(event) => setRest({ port: Number(event.target.value) })} /></label></div><label>Path<input value={rest.path} onChange={(event) => setRest({ path: event.target.value })} placeholder="/api/command" /></label><label>Body template<textarea value={rest.body_template || ""} onChange={(event) => setRest({ body_template: event.target.value })} placeholder={'{"command":{{COMMAND_JSON}}}'} /></label><p className="fine-print">Use {"{{COMMAND}}"}, {"{{COMMAND_JSON}}"}, or {"{{ENV:VARIABLE_NAME}}"}. REST requests can only reach localhost inside this game container.</p></>;
}

const customTemplateExample = JSON.stringify({
  name: "My Game Server",
  author: "Dockside owner",
  description: "A custom compatible game server template.",
  docker_images: { "Java 21": "ghcr.io/pelican-eggs/yolks:java_21" },
  startup: "java -Xms128M -Xmx{{SERVER_MEMORY}}M -jar server.jar",
  config: { stop: "stop" },
  dockside: {
    network_ports: [{
      name: "Game",
      purpose: "Primary game traffic",
      container_port: 25565,
      protocol: "tcp",
      primary: true,
      required: true,
      published: true,
      environment: "SERVER_PORT",
    }],
  },
  scripts: {
    installation: {
      script: "#!/bin/ash\ncd /mnt/server\n# Download or install game files here",
      container: "ghcr.io/pelican-eggs/installers:alpine",
      entrypoint: "ash",
    },
  },
  variables: [{
    name: "Server Jar",
    description: "Jar filename",
    env_variable: "SERVER_JAR",
    default_value: "server.jar",
    user_viewable: true,
    user_editable: true,
    rules: "required|string|max:120",
    field_type: "text",
  }],
}, null, 2);

function TemplateImportDialog({ onClose }: { onClose: () => void }) {
  const queryClient = useQueryClient();
  const [category, setCategory] = useState("Custom");
  const [document, setDocument] = useState(customTemplateExample);
  const [parseError, setParseError] = useState("");
  const importTemplate = useMutation({
    mutationFn: () => {
      let parsed: unknown;
      try {
        parsed = JSON.parse(document);
      } catch {
        throw new Error("The template document is not valid JSON.");
      }
      return api<TemplateDetail>("/api/v1/templates/import", {
        method: "POST",
        body: JSON.stringify({ category: category.trim(), document: parsed }),
      });
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["templates"] });
      void queryClient.invalidateQueries({ queryKey: ["template-facets"] });
      onClose();
    },
  });
  function formatJSON() {
    try {
      setDocument(JSON.stringify(JSON.parse(document), null, 2));
      setParseError("");
    } catch {
      setParseError("The document is not valid JSON.");
    }
  }
  return (
    <div className="dialog-backdrop">
      <div className="dialog template-import-dialog" role="dialog" aria-modal="true">
        <h2>Import compatible template</h2>
        <p>Paste a Pelican/Pterodactyl-compatible JSON definition. It is normalized, validated, and stored locally; the running panel does not fetch a catalog from the web.</p>
        <label>Category<input value={category} maxLength={80} onChange={(event) => setCategory(event.target.value)} /></label>
        <label>Template JSON<textarea className="template-json-editor" value={document} spellCheck={false} onChange={(event) => setDocument(event.target.value)} /></label>
        {(parseError || importTemplate.isError) && <div className="form-error">{parseError || importTemplate.error?.message}</div>}
        <div className="dialog-actions">
          <button className="button ghost" onClick={onClose}>Cancel</button>
          <button className="button secondary" onClick={formatJSON}>Format JSON</button>
          <button className="button primary" disabled={importTemplate.isPending || !category.trim() || !document.trim()} onClick={() => importTemplate.mutate()}>{importTemplate.isPending ? "Validating..." : "Import template"}</button>
        </div>
      </div>
    </div>
  );
}

export function ServersPage() {
  const servers = useQuery({
    queryKey: ["servers"],
    queryFn: () => api<{ servers: ServerSummary[] }>("/api/v1/servers"),
    refetchInterval: 5_000,
  });
  return (
    <>
      <PageHeader
        eyebrow="FLEET"
        title="Game servers"
        description="Provision and operate isolated Docker game server workloads."
        actions={
          <Link className="button primary" to="/servers/new">
            <Plus size={18} /> New server
          </Link>
        }
      />
      {servers.isError ? (
        <ErrorPanel error={servers.error} retry={() => void servers.refetch()} />
      ) : servers.isLoading ? (
        <div className="server-grid loading-cards">
          {Array.from({ length: 4 }, (_, index) => <span key={index} />)}
        </div>
      ) : servers.data?.servers.length ? (
        <div className="server-grid">
          {servers.data.servers.map((server) => (
            <ServerCard server={server} key={server.id} />
          ))}
        </div>
      ) : (
        <section className="panel">
          <EmptyState
            icon={Server}
            title="No game servers provisioned"
            description="Choose from the bundled template library and create your first isolated server."
            action={
              <Link className="button secondary" to="/servers/new">
                Create a server
              </Link>
            }
          />
        </section>
      )}
    </>
  );
}

function ServerCard({ server }: { server: ServerSummary }) {
  const presentation = serverPresentation(server);
  const tone = presentation.tone;
  return (
    <Link className="server-card" to={`/servers/${server.id}`}>
      <div className="server-card-heading">
        <span className={`server-game-mark ${tone}`}><Server size={20} /></span>
        <StatusBadge tone={tone}>{presentation.label}</StatusBadge>
      </div>
      <h3>{server.name}</h3>
      <p>{server.template_name} · template v{server.template_version}</p>
      {server.runtime.last_error && (
        <div className="server-error"><AlertTriangle size={14} /> {server.runtime.last_error}</div>
      )}
      <div className="server-live-metrics">
        <span><Cpu size={14} /> {formatPercent(server.runtime.cpu_percent)}</span>
        <span><MemoryStick size={14} /> {formatBytes(server.runtime.memory_bytes)}</span>
        <span><Network size={14} /> {server.primary_port ? `${server.primary_port.host_port}/${server.primary_port.protocol}` : "—"}</span>
      </div>
      <div className="server-card-footer">
        <span>Updated {formatTimestamp(server.updated_at)}</span>
        <ChevronRight size={16} />
      </div>
    </Link>
  );
}

export function NewServerPage() {
  const [params, setParams] = useSearchParams();
  const selectedVersion = params.get("template") || "";
  const [search, setSearch] = useState("");
  const templateList = useQuery({
    queryKey: ["template-picker", search],
    queryFn: () => {
      const query = new URLSearchParams({ limit: "50" });
      if (search) query.set("search", search);
      return api<TemplateListResponse>(`/api/v1/templates?${query}`);
    },
  });
  const detail = useQuery({
    queryKey: ["template", selectedVersion],
    queryFn: () =>
      api<TemplateDetail>(`/api/v1/templates/${selectedVersion}`),
    enabled: selectedVersion !== "",
  });

  return (
    <>
      <PageHeader
        eyebrow="PROVISION"
        title="Create game server"
        description="Select an embedded template, answer its setup questions, then choose explicit resource limits or leave them unlimited."
      />
      <div className="wizard-layout">
        <section className="panel wizard-template-panel">
          <div className="panel-heading">
            <div><span className="step-number">1</span><h2>Choose a template</h2></div>
          </div>
          <div className="picker-search">
            <Search size={16} />
            <input
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder="Search bundled templates"
            />
          </div>
          <div className="template-picker">
            {templateList.isLoading && <span className="loader" />}
            {templateList.data?.templates.map((template) => (
              <button
                type="button"
                key={template.version_id}
                className={selectedVersion === template.version_id ? "selected" : ""}
                onClick={() => {
                  const next = new URLSearchParams(params);
                  next.set("template", template.version_id);
                  setParams(next);
                }}
              >
                <span className={`template-source-pill ${template.source_kind}`}>
                  {template.source_kind === "pelican"
                    ? "Pelican compatible"
                    : template.source_kind === "pterodactyl"
                      ? "Pterodactyl compatible"
                      : "Custom template"}
                </span>
                <span><strong>{template.name}</strong><small>{template.category}</small></span>
                {selectedVersion === template.version_id && <span className="selected-check">✓</span>}
              </button>
            ))}
          </div>
        </section>
        <section className="panel wizard-config-panel">
          <div className="panel-heading">
            <div><span className="step-number">2</span><h2>Configure and provision</h2></div>
          </div>
          {!selectedVersion ? (
            <EmptyState
              icon={Library}
              title="Select a template"
              description="Choose a definition from the bundled template library to see its provisioning questions."
            />
          ) : detail.isLoading ? (
            <div className="wizard-loading"><span className="loader" /></div>
          ) : detail.isError || !detail.data ? (
            <ErrorPanel error={detail.error} retry={() => void detail.refetch()} />
          ) : (
            <ProvisionForm key={detail.data.version_id} template={detail.data} />
          )}
        </section>
      </div>
    </>
  );
}

function ProvisionForm({ template }: { template: TemplateDetail }) {
  const navigate = useNavigate();
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [image, setImage] = useState(template.canonical_document.default_image);
  const [ports, setPorts] = useState(() =>
    (template.canonical_document.network_ports ?? []).map((port, index) => ({
      key: `${port.environment || port.name}-${index}`,
      name: port.name,
      purpose: port.purpose,
      containerPort: port.container_port ? String(port.container_port) : "",
      protocol: port.protocol,
      environment: port.environment ?? "",
      primary: port.primary,
      enabled: port.primary || port.required || port.published,
      required: port.required,
    })),
  );
  const [memory, setMemory] = useState(template.canonical_document.resource_defaults?.memory_limit_mb?.toString() ?? "");
  const [cpu, setCPU] = useState(template.canonical_document.resource_defaults?.cpu_limit_millicores?.toString() ?? "");
  const [disk, setDisk] = useState(template.canonical_document.resource_defaults?.disk_alert_limit_mb?.toString() ?? "");
  const [start, setStart] = useState(true);
  const [variables, setVariables] = useState<Record<string, string>>(() =>
    Object.fromEntries(
      template.canonical_document.variables
        .filter((variable) => variable.user_editable)
        .map((variable) => [
          variable.environment,
          variable.default_value,
        ]),
    ),
  );
  const create = useMutation({
    mutationFn: () =>
      api<{ server_id: string; operation_id: string; host_port: number }>(
        "/api/v1/servers",
        {
          method: "POST",
          body: JSON.stringify({
            template_version_id: template.version_id,
            name,
            description,
            image,
            ports: ports.filter((port) => port.enabled).map((port) => ({
              container_port: Number(port.containerPort),
              protocol: port.protocol,
              purpose: port.purpose || port.name,
              environment: port.environment,
              primary: port.primary,
            })),
            cpu_limit_millicores: cpu === "" ? null : Number(cpu),
            memory_limit_mb: memory === "" ? null : Number(memory),
            disk_limit_mb: disk === "" ? null : Number(disk),
            variables,
            start_after_provisioning: start,
          }),
        },
      ),
    onSuccess: (result) => navigate(`/servers/${result.server_id}`),
  });
  const editableVariables = template.canonical_document.variables.filter(
    (variable) => variable.user_viewable && variable.user_editable,
  );
  return (
    <form
      className="provision-form"
      onSubmit={(event) => {
        event.preventDefault();
        create.mutate();
      }}
    >
      <div className="selected-template">
        <span className={`template-source-pill ${template.source_kind}`}>
          {template.source_kind === "pelican"
            ? "Pelican compatible"
            : template.source_kind === "pterodactyl"
              ? "Pterodactyl compatible"
              : "Custom template"}
        </span>
        <div><strong>{template.name}</strong><span>{template.category} · {template.source_kind}</span></div>
        <StatusBadge tone="success">Validated</StatusBadge>
      </div>
      <div className="form-grid three">
        <label>Server name
          <input required minLength={2} maxLength={80} value={name} onChange={(event) => setName(event.target.value)} placeholder="Community Survival" />
        </label>
        <label>Runtime image
          <select value={image} onChange={(event) => setImage(event.target.value)}>
            {Object.entries(template.canonical_document.images).map(([label, value]) => <option value={value} key={value}>{label}</option>)}
          </select>
        </label>
      </div>
      <label>Description <input maxLength={500} value={description} onChange={(event) => setDescription(event.target.value)} placeholder="Optional note for panel users" /></label>
      <div className="form-section-title"><Network size={16} /> Template network allocations <FieldHelp text="Container ports are the ports used by the game process. Dockside assigns conflict-free host ports for players to connect to." /></div>
      <p className="form-help">Docker publishes each enabled allocation using the selected protocol. The host port is assigned automatically and may differ from the container port.</p>
      <div className="template-port-list">
        {ports.map((port) => (
          <div className={`template-port-row ${port.enabled ? "" : "disabled"}`} key={port.key}>
            <label className="check-row">
              <input
                type="checkbox"
                checked={port.enabled}
                disabled={port.primary || port.required}
                onChange={(event) => setPorts((current) => current.map((candidate) =>
                  candidate.key === port.key ? { ...candidate, enabled: event.target.checked } : candidate
                ))}
              />
              <span>{port.name || "Game port"}{port.primary ? " · Primary" : ""}</span>
            </label>
            <label>Container port
              <input
                required={port.enabled}
                disabled={!port.enabled}
                type="number"
                min={1}
                max={65535}
                value={port.containerPort}
                placeholder="Required"
                onChange={(event) => setPorts((current) => current.map((candidate) =>
                  candidate.key === port.key ? { ...candidate, containerPort: event.target.value } : candidate
                ))}
              />
            </label>
            <label>Protocol
              <select
                required={port.enabled}
                disabled={!port.enabled}
                value={port.protocol}
                onChange={(event) => setPorts((current) => current.map((candidate) =>
                  candidate.key === port.key
                    ? { ...candidate, protocol: event.target.value as "tcp" | "udp" | "" }
                    : candidate
                ))}
              >
                <option value="">Select protocol</option>
                <option value="tcp">TCP</option>
                <option value="udp">UDP</option>
              </select>
            </label>
            <small>{port.purpose}{port.environment ? ` · ${port.environment}` : ""}</small>
            {!port.primary && (
              <button
                type="button"
                className="icon-button danger"
                title="Remove allocation"
                onClick={() => setPorts((current) => current.filter((candidate) => candidate.key !== port.key))}
              ><Trash2 size={13} /></button>
            )}
          </div>
        ))}
        <button
          type="button"
          className="secondary-button compact"
          onClick={() => setPorts((current) => [...current, {
            key: crypto.randomUUID(), name: "Additional port", purpose: "Additional game traffic",
            containerPort: "", protocol: "", environment: "", primary: false,
            enabled: true, required: false,
          }])}
        ><Plus size={13} /> Add port</button>
      </div>
      <div className="form-section-title"><Gauge size={16} /> Resource limits <FieldHelp text="Leave resource limits blank to allow the server to use available host capacity." /></div>
      <div className="form-grid two">
        <label>Memory limit (MB) <span className="label-hint">Blank = unlimited</span>
          <input type="number" min={64} value={memory} onChange={(event) => setMemory(event.target.value)} placeholder="Unlimited" />
        </label>
        <label>CPU limit (millicores) <span className="label-hint">Blank = unlimited; 1000 = one core</span>
          <input type="number" min={100} value={cpu} onChange={(event) => setCPU(event.target.value)} placeholder="Unlimited" />
        </label>
        <label>Disk alert limit (MB) <span className="label-hint">Blank = no per-server alert threshold</span>
          <input type="number" min={64} value={disk} onChange={(event) => setDisk(event.target.value)} placeholder="Unlimited" />
        </label>
      </div>
      {editableVariables.length > 0 && (
        <>
          <div className="form-section-title"><Variable size={16} /> Template questions</div>
          <div className="form-grid two">
            {editableVariables.map((variable) => (
              <label key={variable.environment}>{variable.name || variable.environment}
                <span className="label-hint" title={variable.description}>{variable.environment}</span>
                <input
                  type={variable.secret ? "password" : "text"}
                  value={variables[variable.environment] ?? ""}
                  required={variable.rules?.split("|").includes("required")}
                  onChange={(event) => setVariables((current) => ({ ...current, [variable.environment]: event.target.value }))}
                />
              </label>
            ))}
          </div>
        </>
      )}
      <label className="checkbox-row">
        <input type="checkbox" checked={start} onChange={(event) => setStart(event.target.checked)} />
        <span><strong>Start after installation</strong><small>Otherwise the new container remains stopped.</small></span>
      </label>
      {create.isError && <div className="form-error">{create.error.message}</div>}
      <button className="button primary provision-submit" type="submit" disabled={create.isPending || name.trim().length < 2}>
        {create.isPending ? <><RefreshCw className="spin-icon" size={17} /> Queuing server…</> : <><Plus size={17} /> Create server</>}
      </button>
      <p className="fine-print">The worker pulls the selected images and game files only after you submit. Catalog data is already local.</p>
    </form>
  );
}

const serverTabs = [
  { path: "", label: "Overview", icon: Gauge },
  { path: "console", label: "Console", icon: SquareTerminal },
  { path: "files", label: "Files", icon: Files },
  { path: "backups", label: "Backups", icon: Archive },
  { path: "schedules", label: "Schedules", icon: Clock3 },
  { path: "databases", label: "Databases", icon: Database },
  { path: "network", label: "Network", icon: Network },
  { path: "activity", label: "Activity", icon: Activity },
  { path: "startup", label: "Startup", icon: Variable },
  { path: "settings", label: "Settings", icon: Settings },
];

export function ServerDetailPage() {
  const { serverID = "", "*": tab = "" } = useParams();
  const queryClient = useQueryClient();
  const server = useQuery({
    queryKey: ["server", serverID],
    queryFn: () => api<ServerSummary>(`/api/v1/servers/${serverID}`),
    refetchInterval: 3_000,
  });
  const refreshPowerState = () => {
    void queryClient.invalidateQueries({ queryKey: ["server", serverID] });
    void queryClient.invalidateQueries({ queryKey: ["servers"] });
  };
  const power = useMutation({
    mutationFn: (action: string) =>
      api<void>(`/api/v1/servers/${serverID}/power`, {
        method: "POST",
        body: JSON.stringify({ action }),
      }),
    onSuccess: refreshPowerState,
  });
  const emergencyKill = useMutation({
    mutationFn: () =>
      api<void>(`/api/v1/servers/${serverID}/power`, {
        method: "POST",
        body: JSON.stringify({ action: "kill" }),
      }),
    onSuccess: refreshPowerState,
  });
  if (server.isLoading) return <div className="wizard-loading"><span className="loader" /></div>;
  if (server.isError || !server.data) return <ErrorPanel error={server.error} retry={() => void server.refetch()} />;
  const item = server.data;
  const presentation = serverPresentation(item);
  const transitioning = ["installing", "starting", "restarting", "stopping", "deleting"].includes(item.status);
  return (
    <>
      <div className="server-detail-header">
        <div>
          <Link to="/servers" className="back-link">Servers /</Link>
          <div className="server-title-line"><h1>{item.name}</h1><StatusBadge tone={presentation.tone}>{presentation.label}</StatusBadge></div>
          <p>{item.template_name} · {item.primary_port ? `${item.primary_port.bind_address === "0.0.0.0" ? "127.0.0.1" : item.primary_port.bind_address}:${item.primary_port.host_port}/${item.primary_port.protocol}` : "No allocation"}</p>
          <Link className="template-inline-link" to={`/templates/${item.template_version_id}?fromServer=${item.id}`}>
            <Eye size={13} /> Preview template v{item.template_version}
          </Link>
        </div>
        <div className="power-controls">
          {item.status === "stopped" && (
            <button className="icon-action start" title="Start" disabled={power.isPending} onClick={() => power.mutate("start")}><Play size={17} /></button>
          )}
          {item.status === "running" && (
            <>
              <button className="icon-action stop" title="Stop" disabled={power.isPending} onClick={() => power.mutate("stop")}><CircleStop size={17} /></button>
              <button className="icon-action restart" title="Restart" disabled={power.isPending} onClick={() => power.mutate("restart")}><RotateCw size={17} /></button>
            </>
          )}
          {["running", "starting", "restarting", "stopping"].includes(item.status) && (
            <button className="icon-action kill" title="Kill immediately" disabled={emergencyKill.isPending} onClick={() => emergencyKill.mutate()}><Skull size={17} /></button>
          )}
          {transitioning && (
            <span className={`power-transition ${item.status}`}>
              <RotateCw size={16} className="spin-icon" /> {presentation.label}
            </span>
          )}
        </div>
      </div>
      {power.isError && <div className="form-error">{power.error.message}</div>}
      {emergencyKill.isError && <div className="form-error">{emergencyKill.error.message}</div>}
      {item.runtime.last_error && <div className="notice danger"><AlertTriangle size={16} /> {item.runtime.last_error}</div>}
      <nav className="server-tabs">
        {serverTabs.map(({ path, label, icon: Icon }) => (
          <NavLink key={path} end={path === ""} to={`/servers/${serverID}${path ? `/${path}` : ""}`}>
            <Icon size={15} /> {label}
          </NavLink>
        ))}
      </nav>
      <ServerTabContent server={item} tab={tab} />
    </>
  );
}

function ServerTabContent({ server, tab }: { server: ServerSummary; tab: string }) {
  if (tab === "") return <ServerOverview server={server} />;
  if (tab === "console") return <ServerConsole server={server} />;
  if (tab === "files") return <ServerFiles server={server} />;
  if (tab === "backups") return <ServerBackups server={server} />;
  if (tab === "schedules") return <ServerSchedules server={server} />;
  if (tab === "databases") return <ServerDatabases server={server} />;
  if (tab === "network") return <ServerNetwork server={server} />;
  if (tab === "activity") return <ServerActivity server={server} />;
  if (tab === "startup") return <ServerStartup server={server} />;
  if (tab === "settings") return <ServerSettings server={server} />;
  const content = { icon: Files, title: "Server feature", description: "This route is not available." };
  return (
    <section className="panel">
      <EmptyState icon={content.icon} title={content.title} description={`${content.description} This management surface is the next engine API slice.`} />
    </section>
  );
}

function BackupPathPicker({
  serverID, includes, excludes, onChange,
}: {
  serverID: string;
  includes: string;
  excludes: string;
  onChange: (includes: string, excludes: string) => void;
}) {
  const [directories, setDirectories] = useState<Record<string, ServerFileList>>({});
  const [expanded, setExpanded] = useState<Set<string>>(new Set(["."]));
  const [loading, setLoading] = useState<Set<string>>(new Set(["."]));
  const [error, setError] = useState("");
  const includeRules = parseRules(includes);
  const excludeRules = parseRules(excludes);
  async function load(path: string) {
    if (directories[path] || loading.has(path)) return;
    setLoading((current) => new Set(current).add(path));
    try {
      const result = await api<ServerFileList>(`/api/v1/servers/${serverID}/files?path=${encodeURIComponent(path)}`);
      setDirectories((current) => ({ ...current, [path]: result }));
      setError("");
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Could not load backup paths.");
    } finally {
      setLoading((current) => {
        const next = new Set(current);
        next.delete(path);
        return next;
      });
    }
  }
  useEffect(() => {
    let active = true;
    void api<ServerFileList>(`/api/v1/servers/${serverID}/files?path=.`)
      .then((result) => {
        if (!active) return;
        setDirectories({ ".": result });
        setError("");
      })
      .catch((reason: unknown) => {
        if (!active) return;
        setError(reason instanceof Error ? reason.message : "Could not load backup paths.");
      })
      .finally(() => {
        if (!active) return;
        setLoading((current) => {
          const next = new Set(current);
          next.delete(".");
          return next;
        });
      });
    return () => {
      active = false;
    };
  }, [serverID]);
  function ruleCovers(rule: string, path: string) {
    const normalized = rule.replace(/\/$/, "");
    return normalized === path || path.startsWith(`${normalized}/`);
  }
  function checked(path: string) {
    const included = includeRules.length === 0 || includeRules.some((rule) => ruleCovers(rule, path) || ruleCovers(path, rule));
    return included && !excludeRules.some((rule) => ruleCovers(rule, path));
  }
  function toggle(path: string, select: boolean) {
    if (select) {
      const nextExcludes = excludeRules.filter((rule) => !ruleCovers(rule, path) && !ruleCovers(path, rule));
      const nextIncludes = includeRules.length === 0
        ? []
        : [...includeRules.filter((rule) => !ruleCovers(path, rule)), path];
      onChange(nextIncludes.join("\n"), nextExcludes.join("\n"));
      return;
    }
    if (includeRules.length === 0) {
      const next = [...excludeRules.filter((rule) => !ruleCovers(path, rule)), path];
      onChange("", next.join("\n"));
    } else {
      onChange(includeRules.filter((rule) => !ruleCovers(path, rule) && !ruleCovers(rule, path)).join("\n"), excludes);
    }
  }
  function renderDirectory(path: string, depth = 0): ReactNode {
    const listing = directories[path];
    if (!listing) return loading.has(path) ? <div className="backup-tree-loading" style={{ paddingLeft: depth * 18 }}>Loading…</div> : null;
    return listing.entries.map((entry) => {
      const isDirectory = entry.type === "directory";
      const isExpanded = expanded.has(entry.path);
      return <div key={entry.path}>
        <div className="backup-tree-row" style={{ paddingLeft: depth * 18 }}>
          {isDirectory ? <button className="tree-expand" onClick={() => {
            setExpanded((current) => {
              const next = new Set(current);
              if (next.has(entry.path)) next.delete(entry.path); else next.add(entry.path);
              return next;
            });
            if (!isExpanded) void load(entry.path);
          }}>{isExpanded ? "−" : "+"}</button> : <span className="tree-spacer" />}
          <label className="compact-check"><input type="checkbox" checked={checked(entry.path)} onChange={(event) => toggle(entry.path, event.target.checked)} /><span>{isDirectory ? <Folder size={14} /> : <File size={14} />}{entry.name}</span></label>
          <small>{isDirectory ? "Folder" : formatBytes(entry.size)}</small>
        </div>
        {isDirectory && isExpanded && renderDirectory(entry.path, depth + 1)}
      </div>;
    });
  }
  return <div className="backup-path-picker">
    <div className="backup-picker-heading"><div><strong>Files to include</strong><span>Folder selections apply to every file beneath them.</span></div><div><button className="button ghost compact" onClick={() => onChange("", "")}>Check all</button><button className="button ghost compact" onClick={() => onChange("__dockside_no_files_selected__", "")}>Uncheck all</button></div></div>
    <div className="backup-tree">{loading.has(".") ? <div className="file-state"><span className="loader" /></div> : renderDirectory(".")}</div>
    {error && <div className="form-error">{error}</div>}
  </div>;
}

function ServerBackups({ server }: { server: ServerSummary }) {
  const queryClient = useQueryClient();
  const [name, setName] = useState(`Backup ${new Date().toLocaleDateString()}`);
  const [includes, setIncludes] = useState("");
  const [excludes, setExcludes] = useState("logs/*\n*.log");
  const [retentionDays, setRetentionDays] = useState("");
  const [discordWebhookID, setDiscordWebhookID] = useState("");
  const [discordFormat, setDiscordFormat] = useState<"zip" | "archive">("zip");
  const [confirmation, setConfirmation] = useState<{ mode: "restore" | "delete"; backup: ServerBackup } | null>(null);
  const [confirmBackup, setConfirmBackup] = useState("");
  const [confirmServer, setConfirmServer] = useState("");
  const defaultsApplied = useRef(false);
  const configuration = useQuery({
    queryKey: ["server-configuration", server.id],
    queryFn: () => api<ServerConfiguration>(`/api/v1/servers/${server.id}/configuration`),
  });
  useEffect(() => {
    if (!configuration.data || defaultsApplied.current) return;
    defaultsApplied.current = true;
    const defaults = configuration.data.backup_defaults;
    setIncludes(defaults?.include_paths?.join("\n") || "");
    setExcludes(defaults?.exclude_globs?.join("\n") || "logs/*\n*.log");
    setRetentionDays(defaults?.retention_days?.toString() || "");
  }, [configuration.data]);
  const backups = useQuery({
    queryKey: ["server-backups", server.id],
    queryFn: () => api<{ backups: ServerBackup[] }>(`/api/v1/servers/${server.id}/backups`),
    refetchInterval: (query) =>
      query.state.data?.backups.some((item) =>
        ["queued", "running", "deleting"].includes(item.status) ||
        (item.discord_delivery && ["pending", "queued", "uploading"].includes(item.discord_delivery.status))
      ) ? 2_000 : 10_000,
  });
  const webhooks = useQuery({
    queryKey: ["server-webhooks", server.id],
    queryFn: () => api<{ webhooks: ServerWebhook[] }>(`/api/v1/servers/${server.id}/webhooks`),
  });
  const discordWebhooks = webhooks.data?.webhooks.filter(
    (destination) => destination.kind === "discord" && destination.enabled,
  ) ?? [];
  const refresh = () => queryClient.invalidateQueries({ queryKey: ["server-backups", server.id] });
  const create = useMutation({
    mutationFn: () =>
      api<ServerBackup>(`/api/v1/servers/${server.id}/backups`, {
        method: "POST",
        body: JSON.stringify({
          name: name.trim(),
          include_paths: parseRules(includes),
          exclude_globs: parseRules(excludes),
          retention_days: retentionDays === "" ? null : Number(retentionDays),
          discord_webhook_id: discordWebhookID || null,
          discord_format: discordWebhookID ? discordFormat : "",
        }),
      }),
    onSuccess: () => {
      setName(`Backup ${new Date().toLocaleString()}`);
      void refresh();
    },
  });
  const lock = useMutation({
    mutationFn: ({ backup, locked }: { backup: ServerBackup; locked: boolean }) =>
      api<void>(`/api/v1/servers/${server.id}/backups/${backup.id}`, {
        method: "PATCH",
        body: JSON.stringify({ locked }),
      }),
    onSuccess: () => void refresh(),
  });
  const restore = useMutation({
    mutationFn: (backup: ServerBackup) =>
      api<void>(`/api/v1/servers/${server.id}/backups/${backup.id}/restore`, {
        method: "POST",
        body: JSON.stringify({
          confirm_server_name: confirmServer,
          confirm_backup_name: confirmBackup,
        }),
      }),
    onSuccess: () => {
      setConfirmation(null);
      setConfirmBackup("");
      setConfirmServer("");
      void queryClient.invalidateQueries({ queryKey: ["server", server.id] });
    },
  });
  const remove = useMutation({
    mutationFn: (backup: ServerBackup) =>
      api<void>(`/api/v1/servers/${server.id}/backups/${backup.id}`, {
        method: "DELETE",
        body: JSON.stringify({ confirm_name: confirmBackup }),
      }),
    onSuccess: () => {
      setConfirmation(null);
      setConfirmBackup("");
      void refresh();
    },
  });
  const actionError = create.error || lock.error || restore.error || remove.error;
  return (
    <>
      <div className="backup-layout">
        <section className="panel backup-create">
          <div className="panel-heading"><div><span className="eyebrow">NEW ARCHIVE</span><h2>Create backup</h2></div></div>
          <div className="backup-form">
            <label>Name <FieldHelp text="A descriptive label for this backup archive." /><input value={name} maxLength={120} onChange={(event) => setName(event.target.value)} /></label>
            <BackupPathPicker key={server.id} serverID={server.id} includes={includes} excludes={excludes} onChange={(nextIncludes, nextExcludes) => { setIncludes(nextIncludes); setExcludes(nextExcludes); }} />
            <details className="advanced-filters"><summary>Advanced path and glob rules</summary><label>Include paths <span className="label-hint">One path or glob per line; blank includes everything.</span><textarea value={includes} onChange={(event) => setIncludes(event.target.value)} placeholder={"world/\nconfig/"} /></label><label>Exclude globs<textarea value={excludes} onChange={(event) => setExcludes(event.target.value)} /></label></details>
            <label>
              Retention days <FieldHelp text="Expired unlocked backups are removed automatically after this many days." />
              <span className="label-hint">Leave blank to keep this backup until it is manually deleted.</span>
              <input
                type="number"
                min={1}
                max={3650}
                value={retentionDays}
                onChange={(event) => setRetentionDays(event.target.value)}
                placeholder="Keep indefinitely"
              />
            </label>
            <label>
              Discord delivery <FieldHelp text="Optionally attach the completed archive to an enabled Discord webhook when it fits Discord's upload limit." />
              <span className="label-hint">Optional. Discord normally accepts files up to 10 MiB; larger backups remain stored locally.</span>
              <select value={discordWebhookID} onChange={(event) => setDiscordWebhookID(event.target.value)}>
                <option value="">Do not send this backup</option>
                {discordWebhooks.map((destination) => (
                  <option value={destination.id} key={destination.id}>{destination.name}</option>
                ))}
              </select>
            </label>
            {discordWebhookID && (
              <label>Discord attachment format
                <select value={discordFormat} onChange={(event) => setDiscordFormat(event.target.value as "zip" | "archive")}>
                  <option value="zip">ZIP export</option>
                  <option value="archive">Native restore archive (.tar.gz)</option>
                </select>
              </label>
            )}
            <button
              className="primary-button"
              disabled={
                create.isPending ||
                !name.trim() ||
                (retentionDays !== "" && (Number(retentionDays) < 1 || Number(retentionDays) > 3650))
              }
              onClick={() => create.mutate()}
            ><Archive size={15} /> {create.isPending ? "Queuing..." : "Create backup"}</button>
          </div>
        </section>
        <section className="panel">
          <div className="panel-heading"><div><span className="eyebrow">VERIFIED ARCHIVES</span><h2>Backups</h2></div><button className="icon-button" onClick={() => void backups.refetch()}><RefreshCw size={14} /></button></div>
          {backups.isLoading && <div className="file-state"><span className="loader" /></div>}
          {backups.isError && <ErrorPanel error={backups.error} retry={() => void backups.refetch()} />}
          {backups.data?.backups.length === 0 && <EmptyState icon={FileArchive} title="No backups yet" description="Create a checksummed archive of this server volume." />}
          <div className="backup-list">
            {backups.data?.backups.map((backup) => (
              <article key={backup.id}>
                <div className="backup-icon"><FileArchive size={18} /></div>
                <div className="backup-copy">
                  <div><strong>{backup.name}</strong><StatusBadge tone={backup.status === "succeeded" ? "success" : backup.status === "failed" ? "danger" : "info"}>{backup.status}</StatusBadge></div>
                  <span>{formatTimestamp(backup.created_at)} · {formatBytes(backup.size_bytes)}</span>
                  <span>
                    {backup.expires_at
                      ? `Expires ${formatTimestamp(backup.expires_at)}`
                      : "Kept until manually deleted"}
                  </span>
                  {backup.sha256 && <code title={backup.sha256}>SHA-256 {backup.sha256.slice(0, 16)}…</code>}
                  {backup.discord_delivery && (
                    <span
                      className={`backup-delivery ${backup.discord_delivery.status}`}
                      title={backup.discord_delivery.last_error ?? undefined}
                    >
                      Discord: {backup.discord_delivery.status.replace("_", " ")}
                      {" · "}{backup.discord_delivery.destination_name}
                      {" · "}{backup.discord_delivery.format === "zip" ? "ZIP" : "tar.gz"}
                    </span>
                  )}
                </div>
                <div className="backup-actions">
                  <button className="icon-button" disabled={!["succeeded", "failed"].includes(backup.status) || lock.isPending} onClick={() => lock.mutate({ backup, locked: !backup.locked })} title={backup.locked ? "Unlock backup" : "Lock backup"}>{backup.locked ? <UnlockKeyhole size={14} /> : <LockKeyhole size={14} />}</button>
                  <button className="secondary-button compact" disabled={backup.status !== "succeeded" || server.status !== "stopped"} onClick={() => setConfirmation({ mode: "restore", backup })}><RotateCw size={13} /> Restore</button>
                  <button className="icon-button danger" disabled={backup.locked || !["succeeded", "failed"].includes(backup.status)} onClick={() => setConfirmation({ mode: "delete", backup })} title="Delete backup"><Trash2 size={14} /></button>
                </div>
              </article>
            ))}
          </div>
        </section>
      </div>
      {actionError && <div className="form-error">{actionError.message}</div>}
      {confirmation && (
        <div className="dialog-backdrop" role="presentation">
          <div className="dialog" role="dialog" aria-modal="true">
            <h2>{confirmation.mode === "restore" ? "Restore backup" : "Delete backup"}</h2>
            <p>{confirmation.mode === "restore" ? "Restoring replaces every file in the stopped server volume. This cannot be undone." : "This permanently removes the archive and cannot be undone."}</p>
            <label>Type backup name<input value={confirmBackup} onChange={(event) => setConfirmBackup(event.target.value)} placeholder={confirmation.backup.name} /></label>
            {confirmation.mode === "restore" && <label>Type server name<input value={confirmServer} onChange={(event) => setConfirmServer(event.target.value)} placeholder={server.name} /></label>}
            {(restore.isError || remove.isError) && <div className="form-error">{(restore.error || remove.error)?.message}</div>}
            <div className="dialog-actions">
              <button className="secondary-button" onClick={() => setConfirmation(null)}>Cancel</button>
              <button
                className={confirmation.mode === "delete" ? "danger-button" : "primary-button"}
                disabled={confirmBackup !== confirmation.backup.name || (confirmation.mode === "restore" && confirmServer !== server.name) || restore.isPending || remove.isPending}
                onClick={() => confirmation.mode === "restore" ? restore.mutate(confirmation.backup) : remove.mutate(confirmation.backup)}
              >
                {confirmation.mode === "restore" ? "Replace files and restore" : "Permanently delete"}
              </button>
            </div>
          </div>
        </div>
      )}
    </>
  );
}

function parseRules(value: string) {
  return value.split(/\r?\n|,/).map((item) => item.trim()).filter(Boolean);
}

type DroppedEntry = {
  name: string;
  isFile: boolean;
  isDirectory: boolean;
  file?: (callback: (file: globalThis.File) => void, error?: () => void) => void;
  createReader?: () => { readEntries: (callback: (entries: DroppedEntry[]) => void, error?: () => void) => void };
};

async function filesFromDrop(data: DataTransfer) {
  const result: Array<{ file: globalThis.File; relativePath: string }> = [];
  async function visit(entry: DroppedEntry, prefix: string): Promise<void> {
    const relativePath = prefix ? `${prefix}/${entry.name}` : entry.name;
    if (entry.isFile && entry.file) {
      const file = await new Promise<globalThis.File>((resolve, reject) => entry.file?.(resolve, reject));
      result.push({ file, relativePath });
      return;
    }
    if (entry.isDirectory && entry.createReader) {
      const reader = entry.createReader();
      for (;;) {
        const children = await new Promise<DroppedEntry[]>((resolve, reject) => reader.readEntries(resolve, reject));
        if (children.length === 0) break;
        for (const child of children) await visit(child, relativePath);
      }
    }
  }
  const entries: DroppedEntry[] = [];
  for (const item of Array.from(data.items)) {
    const entry = (item as unknown as { webkitGetAsEntry?: () => DroppedEntry | null }).webkitGetAsEntry?.();
    if (entry) entries.push(entry);
  }
  if (entries.length) {
    for (const entry of entries) await visit(entry, "");
    return result;
  }
  return Array.from(data.files).map((file) => ({ file, relativePath: file.name }));
}

function ServerFiles({ server }: { server: ServerSummary }) {
  const queryClient = useQueryClient();
  const [directory, setDirectory] = useState(".");
  const [selected, setSelected] = useState("");
  const [edits, setEdits] = useState<Record<string, string>>({});
  const [newName, setNewName] = useState("");
  const [fileError, setFileError] = useState("");
  const uploadInput = useRef<HTMLInputElement>(null);
  const folderInput = useRef<HTMLInputElement>(null);
  const [dragging, setDragging] = useState(false);
  const [uploadProgress, setUploadProgress] = useState({ completed: 0, total: 0 });
  const listing = useQuery({
    queryKey: ["server-files", server.id, directory],
    queryFn: () =>
      api<ServerFileList>(
        `/api/v1/servers/${server.id}/files?path=${encodeURIComponent(directory)}`,
      ),
  });
  const content = useQuery({
    queryKey: ["server-file", server.id, selected],
    queryFn: () =>
      api<ServerFileContent>(
        `/api/v1/servers/${server.id}/files/content?path=${encodeURIComponent(selected)}`,
      ),
    enabled: selected !== "",
  });
  const editor = selected ? (edits[selected] ?? content.data?.content ?? "") : "";

  const save = useMutation({
    mutationFn: ({ path, value }: { path: string; value: string }) =>
      api<ServerFileContent>(`/api/v1/servers/${server.id}/files/content`, {
        method: "PUT",
        body: JSON.stringify({ path, content: value }),
      }),
    onSuccess: (result) => {
      setSelected(result.path);
      setEdits((current) => ({ ...current, [result.path]: result.content }));
      setNewName("");
      setFileError("");
      void queryClient.invalidateQueries({ queryKey: ["server-files", server.id, directory] });
      void queryClient.invalidateQueries({ queryKey: ["server-file", server.id, result.path] });
    },
    onError: (error) => setFileError(error.message),
  });
  const createDirectory = useMutation({
    mutationFn: (path: string) =>
      api<void>(`/api/v1/servers/${server.id}/files/directories`, {
        method: "POST",
        body: JSON.stringify({ path }),
      }),
    onSuccess: () => {
      setNewName("");
      setFileError("");
      void queryClient.invalidateQueries({ queryKey: ["server-files", server.id, directory] });
    },
    onError: (error) => setFileError(error.message),
  });
  const remove = useMutation({
    mutationFn: (path: string) =>
      api<void>(`/api/v1/servers/${server.id}/files`, {
        method: "DELETE",
        body: JSON.stringify({ path }),
      }),
    onSuccess: (_, path) => {
      if (selected === path) {
        setSelected("");
      }
      setEdits((current) => {
        const next = { ...current };
        delete next[path];
        return next;
      });
      setFileError("");
      void queryClient.invalidateQueries({ queryKey: ["server-files", server.id, directory] });
    },
    onError: (error) => setFileError(error.message),
  });

  function childPath(name: string) {
    return directory === "." ? name : `${directory}/${name}`;
  }

  function parentPath() {
    if (directory === ".") return ".";
    const parts = directory.split("/");
    parts.pop();
    return parts.join("/") || ".";
  }

  function create(kind: "file" | "directory") {
    const name = newName.trim();
    if (!name) {
      setFileError("Enter a file or directory name.");
      return;
    }
    const target = childPath(name);
    if (kind === "directory") createDirectory.mutate(target);
    else save.mutate({ path: target, value: "" });
  }

  async function upload(files: Array<{ file: globalThis.File; relativePath: string }>) {
    if (files.length === 0) return;
    setFileError("");
    setUploadProgress({ completed: 0, total: files.length });
    try {
      const directories = new Set<string>();
      for (const item of files) {
        const parts = item.relativePath.replaceAll("\\", "/").split("/");
        parts.pop();
        let current = "";
        for (const part of parts) {
          current = current ? `${current}/${part}` : part;
          directories.add(childPath(current));
        }
      }
      for (const path of [...directories].sort((left, right) => left.split("/").length - right.split("/").length)) {
        try {
          await api<void>(`/api/v1/servers/${server.id}/files/directories`, {
            method: "POST", body: JSON.stringify({ path }),
          });
        } catch {
          // Existing folders are expected during merges; each upload is still
          // independently validated by the engine.
        }
      }
      let completed = 0;
      for (const item of files) {
        const target = childPath(item.relativePath.replaceAll("\\", "/"));
        await api<{ path: string }>(
          `/api/v1/servers/${server.id}/files/upload?path=${encodeURIComponent(target)}`,
          { method: "POST", headers: { "Content-Type": "application/octet-stream" }, body: item.file },
        );
        completed++;
        setUploadProgress({ completed, total: files.length });
      }
      void queryClient.invalidateQueries({ queryKey: ["server-files", server.id, directory] });
    } catch (error) {
      setFileError(error instanceof Error ? error.message : "Could not upload the selected files.");
    } finally {
      if (uploadInput.current) uploadInput.current.value = "";
      if (folderInput.current) folderInput.current.value = "";
      setDragging(false);
    }
  }

  function download(path: string) {
    if (!path) return;
    const anchor = document.createElement("a");
    anchor.href = `/api/v1/servers/${server.id}/files/download?path=${encodeURIComponent(path)}`;
    anchor.click();
  }

  const busy = save.isPending || createDirectory.isPending || remove.isPending;
  return (
    <section
      className={`panel file-manager ${dragging ? "dragging" : ""}`}
      onDragEnter={(event) => { event.preventDefault(); setDragging(true); }}
      onDragOver={(event) => event.preventDefault()}
      onDragLeave={(event) => { if (event.currentTarget === event.target) setDragging(false); }}
      onDrop={(event) => {
        event.preventDefault();
        void filesFromDrop(event.dataTransfer).then(upload);
      }}
    >
      <div className="panel-heading file-toolbar">
        <div>
          <span className="eyebrow">VOLUME-BACKED FILES</span>
          <h2>File manager</h2>
        </div>
        <div className="file-create-controls">
          <input value={newName} onChange={(event) => setNewName(event.target.value)} maxLength={255} placeholder="config.yml" />
          <button className="secondary-button compact" disabled={busy} onClick={() => create("file")}><FilePlus size={14} /> New file</button>
          <button className="secondary-button compact" disabled={busy} onClick={() => create("directory")}><FolderPlus size={14} /> New folder</button>
          <button className="secondary-button compact" disabled={busy} onClick={() => uploadInput.current?.click()}><Upload size={14} /> Upload files</button>
          <button className="secondary-button compact" disabled={busy} onClick={() => folderInput.current?.click()}><FolderPlus size={14} /> Upload folder</button>
          <button className="secondary-button compact" disabled={busy} onClick={() => download(directory)}><Download size={14} /> Download folder</button>
          <input ref={uploadInput} hidden multiple type="file" onChange={(event) => void upload(Array.from(event.target.files ?? []).map((file) => ({ file, relativePath: file.name })))} />
          <input ref={folderInput} hidden multiple type="file" {...{ webkitdirectory: "" }} onChange={(event) => void upload(Array.from(event.target.files ?? []).map((file) => ({ file, relativePath: file.webkitRelativePath || file.name })))} />
        </div>
      </div>
      {fileError && <div className="console-error">{fileError}</div>}
      {uploadProgress.total > 0 && uploadProgress.completed < uploadProgress.total && <div className="upload-progress"><span style={{ width: `${(uploadProgress.completed / uploadProgress.total) * 100}%` }} /><small>Uploading {uploadProgress.completed} of {uploadProgress.total}</small></div>}
      {dragging && <div className="file-drop-overlay"><Upload size={28} /><strong>Drop files or folders here</strong><span>Paths are preserved under the current directory.</span></div>}
      <div className="file-manager-layout">
        <div className="file-browser">
          <div className="file-path">
            <button className="icon-button" disabled={directory === "."} onClick={() => setDirectory(parentPath())} title="Parent directory">↑</button>
            <span>/home/container/{directory === "." ? "" : directory}</span>
            <button className="icon-button" onClick={() => void listing.refetch()} title="Refresh"><RefreshCw size={14} /></button>
          </div>
          {listing.isLoading && <div className="file-state"><span className="loader" /></div>}
          {listing.isError && <ErrorPanel error={listing.error} retry={() => void listing.refetch()} />}
          {listing.data && listing.data.entries.length === 0 && <div className="file-state">This directory is empty.</div>}
          <div className="file-list">
            {listing.data?.entries.map((entry) => (
              <button
                className={selected === entry.path ? "selected" : ""}
                key={entry.path}
                onClick={() => {
                  if (entry.type === "directory") {
                    setDirectory(entry.path);
                    setSelected("");
                  } else if (entry.type === "file") {
                    setSelected(entry.path);
                  }
                }}
                disabled={!["file", "directory"].includes(entry.type)}
              >
                {entry.type === "directory" ? <Folder size={16} /> : <File size={16} />}
                <span><strong>{entry.name}</strong><small>{entry.type === "directory" ? "Directory" : formatBytes(entry.size)}</small></span>
                <time>{formatTimestamp(entry.modified_at)}</time>
                <span
                  className="file-download"
                  role="button"
                  tabIndex={0}
                  title={`Download ${entry.name}`}
                  onClick={(event) => {
                    event.stopPropagation();
                    download(entry.path);
                  }}
                ><Download size={13} /></span>
                <span
                  className="file-delete"
                  role="button"
                  tabIndex={0}
                  title={`Delete ${entry.name}`}
                  onClick={(event) => {
                    event.stopPropagation();
                    if (window.confirm(`Permanently delete “${entry.name}”?`)) remove.mutate(entry.path);
                  }}
                ><Trash2 size={13} /></span>
              </button>
            ))}
          </div>
        </div>
        <div className="file-editor">
          <div className="file-editor-heading">
            <span className="mono">{selected || "Select a text file"}</span>
            <div>
              <button className="icon-button" disabled={!selected} onClick={() => download(selected)} title="Download"><Download size={14} /></button>
              <button className="primary-button compact" disabled={!selected || busy || content.isLoading} onClick={() => save.mutate({ path: selected, value: editor })}><Save size={14} /> Save</button>
            </div>
          </div>
          {content.isLoading && <div className="file-state"><span className="loader" /></div>}
          {content.isError && <ErrorPanel error={content.error} retry={() => void content.refetch()} />}
          {!selected && <EmptyState icon={Files} title="No file selected" description="Choose a text file to inspect and edit it safely." />}
          {selected && content.data && (
            <textarea
              value={editor}
              onChange={(event) => setEdits((current) => ({ ...current, [selected]: event.target.value }))}
              spellCheck={false}
              aria-label={`Editing ${selected}`}
            />
          )}
        </div>
      </div>
    </section>
  );
}

function ServerDatabases({ server }: { server: ServerSummary }) {
  const queryClient = useQueryClient();
  const [name, setName] = useState("");
  const [credentials, setCredentials] = useState<{ database: ServerDatabase; password: string } | null>(null);
  const [deleting, setDeleting] = useState<ServerDatabase | null>(null);
  const [confirmation, setConfirmation] = useState("");
  const databases = useQuery({
    queryKey: ["server-databases", server.id],
    queryFn: () => api<{ databases: ServerDatabase[] }>(`/api/v1/servers/${server.id}/databases`),
    refetchInterval: (query) =>
      query.state.data?.databases.some((item) => ["provisioning", "deleting"].includes(item.status))
        ? 2_000
        : 15_000,
  });
  const refresh = () => queryClient.invalidateQueries({ queryKey: ["server-databases", server.id] });
  const create = useMutation({
    mutationFn: () =>
      api<{ database: ServerDatabase; password: string }>(`/api/v1/servers/${server.id}/databases`, {
        method: "POST",
        body: JSON.stringify({ name: name.trim().toLowerCase() }),
      }),
    onSuccess: (result) => {
      setCredentials(result);
      setName("");
      void refresh();
    },
  });
  const rotate = useMutation({
    mutationFn: (database: ServerDatabase) =>
      api<{ password: string }>(`/api/v1/servers/${server.id}/databases/${database.id}/password`, {
        method: "POST",
      }).then((result) => ({ database, password: result.password })),
    onSuccess: (result) => {
      setCredentials(result);
      void refresh();
    },
  });
  const remove = useMutation({
    mutationFn: (database: ServerDatabase) =>
      api<void>(`/api/v1/servers/${server.id}/databases/${database.id}`, {
        method: "DELETE",
        body: JSON.stringify({ confirm_name: confirmation }),
      }),
    onSuccess: () => {
      setDeleting(null);
      setConfirmation("");
      void refresh();
    },
  });
  const actionError = create.error || rotate.error || remove.error;
  return (
    <>
      <div className="database-layout">
        <section className="panel configuration-form">
          <div className="panel-heading"><div><span className="eyebrow">SCOPED POSTGRESQL</span><h2>Create database</h2></div><Database size={18} /></div>
          <p className="section-description">The first database starts a private PostgreSQL container on this server's isolated Docker network. It is never published on the host.</p>
          <label>Database name
            <input
              value={name}
              maxLength={48}
              pattern="[a-z][a-z0-9_]*"
              placeholder="game_data"
              onChange={(event) => setName(event.target.value.toLowerCase().replace(/[^a-z0-9_]/g, ""))}
            />
          </label>
          <button className="primary-button" disabled={create.isPending || !/^[a-z][a-z0-9_]{0,47}$/.test(name)} onClick={() => create.mutate()}>
            <Plus size={15} /> {create.isPending ? "Preparing PostgreSQL..." : "Create database"}
          </button>
          {create.isError && <div className="form-error">{create.error.message}</div>}
          <p className="fine-print">Passwords are generated cryptographically, encrypted at rest, and shown only when created or rotated.</p>
        </section>
        <section className="panel">
          <div className="panel-heading"><div><span className="eyebrow">PRIVATE DATA SERVICES</span><h2>Databases</h2></div><button className="icon-button" onClick={() => void databases.refetch()}><RefreshCw size={14} /></button></div>
          {databases.isLoading && <div className="file-state"><span className="loader" /></div>}
          {databases.isError && <ErrorPanel error={databases.error} retry={() => void databases.refetch()} />}
          {databases.data?.databases.length === 0 && <EmptyState icon={Database} title="No databases" description="Create an isolated PostgreSQL database and credentials for this game server." />}
          <div className="database-list">
            {databases.data?.databases.map((database) => (
              <article key={database.id}>
                <span className={`database-mark ${database.status}`}><Database size={16} /></span>
                <div>
                  <div><strong>{database.name}</strong><StatusBadge tone={database.status === "ready" ? "success" : database.status === "failed" ? "danger" : "info"}>{database.status}</StatusBadge></div>
                  <code>{database.username}@{database.host}:{database.port}</code>
                  {database.last_error && <small className="danger-text">{database.last_error}</small>}
                </div>
                <div>
                  <button className="secondary-button compact" disabled={database.status !== "ready" || rotate.isPending} onClick={() => rotate.mutate(database)}><KeyRound size={13} /> Rotate</button>
                  <button className="icon-button danger" disabled={database.status === "deleting"} onClick={() => setDeleting(database)}><Trash2 size={13} /></button>
                </div>
              </article>
            ))}
          </div>
          {actionError && <div className="form-error">{actionError.message}</div>}
        </section>
      </div>
      {credentials && (
        <div className="dialog-backdrop">
          <div className="dialog credentials-dialog" role="dialog" aria-modal="true">
            <h2>Copy database credentials now</h2>
            <p>The password is not shown again. Rotating it later invalidates the current password.</p>
            <label>Host<input readOnly value={credentials.database.host} /></label>
            <div className="form-grid two">
              <label>Port<input readOnly value={credentials.database.port} /></label>
              <label>Database<input readOnly value={credentials.database.name} /></label>
            </div>
            <label>Username<input readOnly value={credentials.database.username} /></label>
            <label>Password
              <div className="secret-copy-row"><input readOnly value={credentials.password} /><button className="icon-button" onClick={() => void navigator.clipboard.writeText(credentials.password)}><Copy size={14} /></button></div>
            </label>
            <label>Connection URL
              <div className="secret-copy-row"><input readOnly value={`postgresql://${credentials.database.username}:${credentials.password}@${credentials.database.host}:${credentials.database.port}/${credentials.database.name}`} /><button className="icon-button" onClick={() => void navigator.clipboard.writeText(`postgresql://${credentials.database.username}:${credentials.password}@${credentials.database.host}:${credentials.database.port}/${credentials.database.name}`)}><Copy size={14} /></button></div>
            </label>
            <div className="dialog-actions"><button className="primary-button" onClick={() => setCredentials(null)}>I saved the credentials</button></div>
          </div>
        </div>
      )}
      {deleting && (
        <div className="dialog-backdrop">
          <div className="dialog" role="dialog" aria-modal="true">
            <h2>Delete database {deleting.name}?</h2>
            <p>This permanently drops the database and its login role. If it is the last database, its private PostgreSQL container and volume are also removed.</p>
            <label>Type database name<input value={confirmation} onChange={(event) => setConfirmation(event.target.value)} placeholder={deleting.name} /></label>
            {remove.isError && <div className="form-error">{remove.error.message}</div>}
            <div className="dialog-actions"><button className="secondary-button" onClick={() => setDeleting(null)}>Cancel</button><button className="danger-button" disabled={confirmation !== deleting.name || remove.isPending} onClick={() => remove.mutate(deleting)}>{remove.isPending ? "Dropping database..." : "Permanently delete"}</button></div>
          </div>
        </div>
      )}
    </>
  );
}

function ServerStartup({ server }: { server: ServerSummary }) {
  const configuration = useQuery({
    queryKey: ["server-configuration", server.id],
    queryFn: () => api<ServerConfiguration>(`/api/v1/servers/${server.id}/configuration`),
  });
  if (configuration.isLoading) return <div className="wizard-loading"><span className="loader" /></div>;
  if (configuration.isError || !configuration.data) {
    return <ErrorPanel error={configuration.error} retry={() => void configuration.refetch()} />;
  }
  return <ServerStartupEditor key={configuration.data.version} server={server} data={configuration.data} />;
}

function ServerStartupEditor({ server, data }: { server: ServerSummary; data: ServerConfiguration }) {
  const queryClient = useQueryClient();
  const variableDefinitions = data.variables ?? [];
  const imageOptions = data.images ?? { "Current image": data.image };
  const [image, setImage] = useState(data.image);
  const [startupOverride, setStartupOverride] = useState(data.startup_override ?? "");
  const [variables, setVariables] = useState<Record<string, string>>(
    Object.fromEntries(variableDefinitions.map((item) => [item.name, item.value ?? ""])),
  );
  const [customDefinitions, setCustomDefinitions] = useState(
    variableDefinitions.filter((item) => item.custom),
  );
  const [showTemplateSave, setShowTemplateSave] = useState(false);
  const [templateName, setTemplateName] = useState(`${server.name} Template`);
  const [templateCategory, setTemplateCategory] = useState("Custom");
  const effectiveDefinitions = [
    ...variableDefinitions.filter((item) => !item.custom),
    ...customDefinitions,
  ];
  const save = useMutation({
    mutationFn: () => {
      const updates: Record<string, string> = {};
      for (const definition of effectiveDefinitions) {
        if (!definition.user_editable) continue;
        const value = variables[definition.name] ?? "";
        if (!definition.secret || value !== "") updates[definition.name] = value;
      }
      return api<ServerConfiguration>(`/api/v1/servers/${server.id}/startup`, {
        method: "PUT",
        body: JSON.stringify({
          version: data.version,
          image,
          startup_override: startupOverride,
          variables: updates,
          variable_definitions: customDefinitions.map((definition) => ({
            environment: definition.name,
            display_name: definition.display_name,
            description: definition.description,
            default_value: definition.default_value,
            user_viewable: definition.user_viewable,
            user_editable: definition.user_editable,
            rules: definition.rules,
            field_type: definition.field_type || "text",
            secret: definition.secret,
          })),
        }),
      });
    },
    onSuccess: (result) => {
      queryClient.setQueryData(["server-configuration", server.id], result);
      void queryClient.invalidateQueries({ queryKey: ["server", server.id] });
      void queryClient.invalidateQueries({ queryKey: ["servers"] });
    },
  });
  const saveTemplate = useMutation({
    mutationFn: () => api<TemplateDetail>(`/api/v1/servers/${server.id}/template`, {
      method: "POST",
      body: JSON.stringify({
        name: templateName.trim(), category: templateCategory.trim(),
        description: `Dockside template created from ${server.name}.`,
      }),
    }),
    onSuccess: () => {
      setShowTemplateSave(false);
      void queryClient.invalidateQueries({ queryKey: ["templates"] });
    },
  });
  function addCustomVariable() {
    const name = `CUSTOM_VARIABLE_${customDefinitions.length + 1}`;
    setCustomDefinitions((current) => [...current, {
      name, display_name: "Custom variable", description: "",
      default_value: "", value: "", has_value: false, secret: false,
      user_viewable: true, user_editable: true, rules: "nullable|string",
      field_type: "text", custom: true,
    }]);
    setVariables((current) => ({ ...current, [name]: "" }));
  }
  return (
    <div className="configuration-layout">
      <section className="panel configuration-form">
        <div className="panel-heading">
          <div><span className="eyebrow">CONTAINER BOOT</span><h2>Startup configuration</h2></div>
          <Variable size={18} />
        </div>
        {server.status !== "stopped" && (
          <div className="notice warning"><AlertTriangle size={16} /> Stop the server before applying startup changes.</div>
        )}
        <label>Runtime image <FieldHelp text="The container image used to run the game server. Only images declared by the template are allowed." />
          <select value={image} onChange={(event) => setImage(event.target.value)}>
            {Object.entries(imageOptions).map(([label, value]) => (
              <option key={`${label}-${value}`} value={value}>{label} — {value}</option>
            ))}
          </select>
        </label>
        <label>Custom startup command <FieldHelp text="Override the template command and reference variables with double braces, for example {{SERVER_PORT}}." />
          <span className="label-hint">Leave blank to keep the bundled template command.</span>
          <textarea
            className="startup-editor"
            value={startupOverride}
            onChange={(event) => setStartupOverride(event.target.value)}
            placeholder={data.template_startup}
            spellCheck={false}
          />
        </label>
        <div className="effective-command">
          <span>Effective command preview</span>
          <code>{startupOverride.trim() || data.template_startup}</code>
        </div>
        <button className="primary-button" disabled={save.isPending || server.status !== "stopped"} onClick={() => save.mutate()}>
          <Save size={15} /> {save.isPending ? "Replacing container..." : "Apply startup changes"}
        </button>
        {save.isError && <div className="form-error">{save.error.message}</div>}
        {save.isSuccess && <div className="form-success">Startup configuration applied to a replacement stopped container.</div>}
        <button className="button secondary" type="button" onClick={() => setShowTemplateSave(true)}>
          <Library size={15} /> Save server as template
        </button>
      </section>
      <section className="panel configuration-form">
        <div className="panel-heading">
          <div><span className="eyebrow">TEMPLATE PARAMETERS</span><h2>Variables</h2></div>
          <KeyRound size={18} />
        </div>
        <div className="variable-settings-list">
          {variableDefinitions.map((definition) => (
            definition.custom ? null :
            <label key={definition.name}>
              <span>{definition.display_name || definition.name}{definition.secret && <LockKeyhole size={12} />}</span>
              <small>{definition.description || definition.rules || "Template variable"}</small>
              <input
                type={definition.secret ? "password" : definition.field_type === "number" ? "number" : "text"}
                value={variables[definition.name] ?? ""}
                disabled={!definition.user_editable}
                placeholder={definition.secret && definition.has_value ? "Stored — enter a new value to replace" : ""}
                onChange={(event) => setVariables((current) => ({ ...current, [definition.name]: event.target.value }))}
              />
            </label>
          ))}
        </div>
        <div className="panel-heading compact-heading"><div><span className="eyebrow">SERVER-ONLY</span><h3>Custom variables <FieldHelp text="Server-only variables become container environment values and can be referenced by the startup command. Saving as a template copies their definitions but never secret values." /></h3></div><button className="button secondary compact" onClick={addCustomVariable}><Plus size={13} /> Add variable</button></div>
        <div className="custom-variable-list">
          {customDefinitions.map((definition, index) => (
            <article key={`${definition.name}-${index}`}>
              <div className="form-grid two">
                <label>Display name<input value={definition.display_name} onChange={(event) => setCustomDefinitions((current) => current.map((item, candidate) => candidate === index ? { ...item, display_name: event.target.value } : item))} /></label>
                <label>Environment<input value={definition.name} onChange={(event) => {
                  const nextName = event.target.value.toUpperCase().replace(/[^A-Z0-9_]/g, "");
                  setVariables((current) => {
                    const next = { ...current, [nextName]: current[definition.name] ?? "" };
                    delete next[definition.name];
                    return next;
                  });
                  setCustomDefinitions((current) => current.map((item, candidate) => candidate === index ? { ...item, name: nextName } : item));
                }} /></label>
              </div>
              <label>Description<input value={definition.description} onChange={(event) => setCustomDefinitions((current) => current.map((item, candidate) => candidate === index ? { ...item, description: event.target.value } : item))} /></label>
              <label>Value<input type={definition.secret ? "password" : "text"} value={variables[definition.name] ?? ""} onChange={(event) => setVariables((current) => ({ ...current, [definition.name]: event.target.value }))} /></label>
              <div className="row-actions"><label className="compact-check"><input type="checkbox" checked={definition.secret} onChange={(event) => setCustomDefinitions((current) => current.map((item, candidate) => candidate === index ? { ...item, secret: event.target.checked, field_type: event.target.checked ? "password" : "text" } : item))} /><span>Secret</span></label><button className="icon-button danger" onClick={() => setCustomDefinitions((current) => current.filter((_, candidate) => candidate !== index))}><Trash2 size={13} /></button></div>
            </article>
          ))}
          {customDefinitions.length === 0 && <p className="fine-print">Add a variable to use it as <code>{"{{VARIABLE_NAME}}"}</code> in the startup command.</p>}
        </div>
      </section>
      {showTemplateSave && <div className="dialog-backdrop"><div className="dialog" role="dialog" aria-modal="true"><h2>Save server as template</h2><p>Runtime IDs, assigned host ports, credentials, and secret values are excluded.</p><label>Template name<input value={templateName} onChange={(event) => setTemplateName(event.target.value)} /></label><label>Category<input value={templateCategory} onChange={(event) => setTemplateCategory(event.target.value)} /></label>{saveTemplate.isError && <div className="form-error">{saveTemplate.error.message}</div>}<div className="dialog-actions"><button className="button ghost" onClick={() => setShowTemplateSave(false)}>Cancel</button><button className="button primary" disabled={saveTemplate.isPending || !templateName.trim() || !templateCategory.trim()} onClick={() => saveTemplate.mutate()}>{saveTemplate.isPending ? "Creating…" : "Create template"}</button></div></div></div>}
    </div>
  );
}

type DraftPort = Omit<ServerPort, "id"> & { key: string };

function ServerNetwork({ server }: { server: ServerSummary }) {
  const configuration = useQuery({
    queryKey: ["server-configuration", server.id],
    queryFn: () => api<ServerConfiguration>(`/api/v1/servers/${server.id}/configuration`),
  });
  if (configuration.isLoading) return <div className="wizard-loading"><span className="loader" /></div>;
  if (configuration.isError || !configuration.data) {
    return <ErrorPanel error={configuration.error} retry={() => void configuration.refetch()} />;
  }
  return <ServerNetworkEditor key={configuration.data.version} server={server} data={configuration.data} />;
}

function ServerNetworkEditor({ server, data }: { server: ServerSummary; data: ServerConfiguration }) {
  const queryClient = useQueryClient();
  const [ports, setPorts] = useState<DraftPort[]>(
    data.ports.map((port) => ({ ...port, key: port.id })),
  );
  const save = useMutation({
    mutationFn: () => {
      return api<ServerConfiguration>(`/api/v1/servers/${server.id}/network`, {
        method: "PUT",
        body: JSON.stringify({
          version: data.version,
          ports: ports.map((port) => ({
            bind_address: port.bind_address,
            host_port: port.host_port,
            container_port: port.container_port,
            protocol: port.protocol,
            purpose: port.purpose,
            environment: port.environment ?? "",
            is_primary: port.is_primary,
          })),
        }),
      });
    },
    onSuccess: (result) => {
      queryClient.setQueryData(["server-configuration", server.id], result);
      void queryClient.invalidateQueries({ queryKey: ["server", server.id] });
      void queryClient.invalidateQueries({ queryKey: ["servers"] });
    },
  });
  function updatePort(key: string, values: Partial<DraftPort>) {
    setPorts((current) => current.map((port) => port.key === key ? { ...port, ...values } : port));
  }
  function addPort() {
    const last = ports.reduce((highest, port) => Math.max(highest, port.host_port), 19999);
    setPorts((current) => [...current, {
      key: crypto.randomUUID(),
      bind_address: "0.0.0.0",
      host_port: Math.min(last + 1, 65535),
      container_port: Math.min(last + 1, 65535),
      protocol: "tcp",
      purpose: "additional allocation",
      environment: "",
      is_primary: false,
    }]);
  }
  return (
    <section className="panel network-settings">
      <div className="panel-heading">
        <div><span className="eyebrow">PUBLISHED PORTS</span><h2>Network allocations</h2></div>
        <button className="secondary-button compact" disabled={ports.length >= 64} onClick={addPort}><Plus size={14} /> Add port</button>
      </div>
      <p className="section-description">Each server has a private Docker bridge network. Only the allocations below are published on the host.</p>
      <div className="notice info">
        <Network size={16} />
        <span>
          <strong>Use 127.0.0.1 for local connections.</strong>
          {" "}0.0.0.0 means “bind every host interface” and is not a connection address. LAN and internet clients use the host IP or DNS name plus the host port; external access also requires the matching TCP/UDP firewall and router rule.
        </span>
      </div>
      {server.status !== "stopped" && (
        <div className="notice warning"><AlertTriangle size={16} /> Stop the server before changing published ports.</div>
      )}
      <div className="network-port-list">
        {ports.map((port) => (
          <article key={port.key}>
            <label>Bind
              <select value={port.bind_address} onChange={(event) => updatePort(port.key, { bind_address: event.target.value })}>
                <option value="0.0.0.0">All IPv4 interfaces</option>
                <option value="127.0.0.1">Localhost only</option>
                <option value="::">All IPv6 interfaces</option>
                <option value="::1">IPv6 localhost</option>
              </select>
            </label>
            <label>Host port<input type="number" min={1} max={65535} value={port.host_port} onChange={(event) => updatePort(port.key, { host_port: Number(event.target.value) })} /></label>
            <label>Container port<input type="number" min={1} max={65535} value={port.container_port} onChange={(event) => updatePort(port.key, { container_port: Number(event.target.value) })} /></label>
            <label>Protocol<select value={port.protocol} onChange={(event) => updatePort(port.key, { protocol: event.target.value as "tcp" | "udp" })}><option value="tcp">TCP</option><option value="udp">UDP</option></select></label>
            <label>Purpose<input maxLength={120} value={port.purpose} onChange={(event) => updatePort(port.key, { purpose: event.target.value })} /></label>
            <label>Template variable<input maxLength={80} value={port.environment ?? ""} placeholder="Optional, e.g. QUERY_PORT" onChange={(event) => updatePort(port.key, { environment: event.target.value.toUpperCase() })} /></label>
            <label className="primary-allocation"><input type="radio" name="primary-port" checked={port.is_primary} onChange={() => setPorts((current) => current.map((item) => ({ ...item, is_primary: item.key === port.key })))} /> Primary</label>
            <button className="icon-button danger" disabled={ports.length === 1} title="Remove allocation" onClick={() => setPorts((current) => {
              const next = current.filter((item) => item.key !== port.key);
              if (port.is_primary && next[0]) next[0] = { ...next[0], is_primary: true };
              return next;
            })}><Trash2 size={14} /></button>
          </article>
        ))}
      </div>
      <div className="configuration-actions">
        <button className="primary-button" disabled={save.isPending || server.status !== "stopped" || ports.length === 0} onClick={() => save.mutate()}>
          <Save size={15} /> {save.isPending ? "Replacing container..." : "Apply network changes"}
        </button>
        {save.isError && <div className="form-error">{save.error.message}</div>}
        {save.isSuccess && <div className="form-success">Network allocations applied.</div>}
      </div>
    </section>
  );
}

function ServerGeneralSettings({ server }: { server: ServerSummary }) {
  const configuration = useQuery({
    queryKey: ["server-configuration", server.id],
    queryFn: () => api<ServerConfiguration>(`/api/v1/servers/${server.id}/configuration`),
  });
  if (configuration.isLoading) return <section className="panel"><div className="wizard-loading"><span className="loader" /></div></section>;
  if (configuration.isError || !configuration.data) {
    return <section className="panel"><ErrorPanel error={configuration.error} retry={() => void configuration.refetch()} /></section>;
  }
  return <ServerGeneralSettingsEditor key={configuration.data.version} server={server} data={configuration.data} />;
}

function ServerGeneralSettingsEditor({ server, data }: { server: ServerSummary; data: ServerConfiguration }) {
  const queryClient = useQueryClient();
  const mb = (value: number | null) => value == null ? "" : String(Math.round(value / 1024 / 1024));
  const [form, setForm] = useState({
    name: data.name,
    description: data.description,
    cpu: data.resources.cpu_limit_millicores == null ? "" : String(data.resources.cpu_limit_millicores),
    cpuSet: data.resources.cpu_set ?? "",
    memory: mb(data.resources.memory_limit_bytes),
    reservation: mb(data.resources.memory_reservation_bytes),
    swap: mb(data.resources.swap_limit_bytes),
    disk: mb(data.resources.disk_limit_bytes),
    pids: data.resources.pids_limit == null ? "" : String(data.resources.pids_limit),
    ioWeight: data.resources.io_weight == null ? "" : String(data.resources.io_weight),
    autoRecovery: server.auto_recovery_enabled,
  });
  const optionalNumber = (value: string) => value.trim() === "" ? null : Number(value);
  const save = useMutation({
    mutationFn: () => {
      return api<ServerConfiguration>(`/api/v1/servers/${server.id}/settings`, {
        method: "PUT",
        body: JSON.stringify({
          version: data.version,
          name: form.name.trim(),
          description: form.description.trim(),
          cpu_limit_millicores: optionalNumber(form.cpu),
          cpu_set: form.cpuSet.trim() || null,
          memory_limit_mb: optionalNumber(form.memory),
          memory_reservation_mb: optionalNumber(form.reservation),
          swap_limit_mb: optionalNumber(form.swap),
          disk_limit_mb: optionalNumber(form.disk),
          pids_limit: optionalNumber(form.pids),
          io_weight: optionalNumber(form.ioWeight),
          auto_recovery_enabled: form.autoRecovery,
        }),
      });
    },
    onSuccess: (result) => {
      queryClient.setQueryData(["server-configuration", server.id], result);
      void queryClient.invalidateQueries({ queryKey: ["server", server.id] });
      void queryClient.invalidateQueries({ queryKey: ["servers"] });
    },
  });
  const set = (name: Exclude<keyof typeof form, "autoRecovery">, value: string) =>
    setForm((current) => ({ ...current, [name]: value }));
  return (
    <section className="panel configuration-form general-settings">
      <div className="panel-heading"><div><span className="eyebrow">IDENTITY & LIMITS</span><h2>Server settings</h2></div><Settings size={18} /></div>
      {server.status !== "stopped" && <div className="notice warning"><AlertTriangle size={16} /> Stop the server before changing its limits.</div>}
      <div className="form-grid two">
        <label>Server name<input value={form.name} maxLength={80} onChange={(event) => set("name", event.target.value)} /></label>
        <label>CPU limit (millicores)<input type="number" min={100} max={128000} value={form.cpu} placeholder="Unlimited" onChange={(event) => set("cpu", event.target.value)} /></label>
      </div>
      <label>Description<textarea value={form.description} maxLength={500} onChange={(event) => set("description", event.target.value)} /></label>
      <div className="form-grid four resource-grid">
        <label>Memory MB<input type="number" min={64} value={form.memory} placeholder="Unlimited" onChange={(event) => set("memory", event.target.value)} /></label>
        <label>Reservation MB<input type="number" min={1} value={form.reservation} placeholder="None" onChange={(event) => set("reservation", event.target.value)} /></label>
        <label>Memory + swap MB<input type="number" min={64} value={form.swap} placeholder="Docker default" onChange={(event) => set("swap", event.target.value)} /></label>
        <label>Disk alert limit MB<input type="number" min={64} value={form.disk} placeholder="Unlimited" onChange={(event) => set("disk", event.target.value)} /></label>
        <label>CPU set<input value={form.cpuSet} placeholder="0,2-3" onChange={(event) => set("cpuSet", event.target.value)} /></label>
        <label>PID limit<input type="number" min={16} value={form.pids} placeholder="Unlimited" onChange={(event) => set("pids", event.target.value)} /></label>
        <label>I/O weight<input type="number" min={10} max={1000} value={form.ioWeight} placeholder="Docker default" onChange={(event) => set("ioWeight", event.target.value)} /></label>
      </div>
      <p className="fine-print">Blank limits are unlimited. Disk limits are monitored thresholds because portable Docker named volumes do not provide a consistent hard quota across Windows and Linux.</p>
      <label className="toggle-row">
        <input
          type="checkbox"
          checked={form.autoRecovery}
          onChange={(event) => setForm((current) => ({ ...current, autoRecovery: event.target.checked }))}
        />
        <span><strong>Automatic recovery</strong><small>Restart this server after an unexpected stop, using bounded backoff.</small></span>
      </label>
      <button className="button primary" disabled={save.isPending || server.status !== "stopped" || form.name.trim().length < 2} onClick={() => save.mutate()}>
        <Save size={15} /> {save.isPending ? "Replacing container..." : "Apply settings"}
      </button>
      {save.isError && <div className="form-error">{save.error.message}</div>}
      {save.isSuccess && <div className="form-success">Server settings applied.</div>}
    </section>
  );
}

const webhookEventOptions = [
  { value: "severity:error", label: "All errors" },
  { value: "severity:warning", label: "All warnings" },
  { value: "server.power.start", label: "Server starts" },
  { value: "server.power.restart", label: "Server restarts" },
  { value: "server.unexpected_exit", label: "Unexpected server stops" },
  { value: "server.backup.failed", label: "Backup failures" },
  { value: "server.backup.succeeded", label: "Backup completed" },
  { value: "server.schedule.failed", label: "Schedule failures" },
];

function ServerSettings({ server }: { server: ServerSummary }) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [webhookName, setWebhookName] = useState("Discord operations");
  const [webhookKind, setWebhookKind] = useState<"discord" | "generic">("discord");
  const [webhookURL, setWebhookURL] = useState("");
  const [filters, setFilters] = useState<string[]>([
    "severity:error", "severity:warning", "server.power.restart", "server.unexpected_exit",
  ]);
  const [generatedSecret, setGeneratedSecret] = useState("");
  const [deleteName, setDeleteName] = useState("");
  const [showDelete, setShowDelete] = useState(false);
  const destinations = useQuery({
    queryKey: ["server-webhooks", server.id],
    queryFn: () => api<{ webhooks: ServerWebhook[] }>(`/api/v1/servers/${server.id}/webhooks`),
  });
  const refresh = () => queryClient.invalidateQueries({ queryKey: ["server-webhooks", server.id] });
  const create = useMutation({
    mutationFn: () =>
      api<{ webhook: ServerWebhook; signing_secret: string }>(`/api/v1/servers/${server.id}/webhooks`, {
        method: "POST",
        body: JSON.stringify({
          name: webhookName.trim(),
          kind: webhookKind,
          url: webhookURL.trim(),
          signing_secret: "",
          event_filters: filters,
        }),
      }),
    onSuccess: (result) => {
      setWebhookURL("");
      setGeneratedSecret(result.signing_secret);
      void refresh();
    },
  });
  const toggle = useMutation({
    mutationFn: ({ destination, enabled }: { destination: ServerWebhook; enabled: boolean }) =>
      api<void>(`/api/v1/servers/${server.id}/webhooks/${destination.id}`, {
        method: "PATCH",
        body: JSON.stringify({ enabled }),
      }),
    onSuccess: () => void refresh(),
  });
  const test = useMutation({
    mutationFn: (destination: ServerWebhook) =>
      api<void>(`/api/v1/servers/${server.id}/webhooks/${destination.id}/test`, { method: "POST" }),
  });
  const removeWebhook = useMutation({
    mutationFn: (destination: ServerWebhook) =>
      api<void>(`/api/v1/servers/${server.id}/webhooks/${destination.id}`, { method: "DELETE" }),
    onSuccess: () => void refresh(),
  });
  const deleteServer = useMutation({
    mutationFn: () =>
      api<void>(`/api/v1/servers/${server.id}`, {
        method: "DELETE",
        body: JSON.stringify({ confirm_name: deleteName }),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["servers"] });
      navigate("/servers");
    },
  });
  const webhookError = create.error || toggle.error || test.error || removeWebhook.error;
  return (
    <>
      <ServerGeneralSettings server={server} />
      <div className="settings-layout">
        <section className="panel webhook-config">
          <div className="panel-heading"><div><span className="eyebrow">EVENT DELIVERY</span><h2>New webhook</h2></div><Webhook size={18} /></div>
          <div className="webhook-form">
            <div className="form-grid two">
              <label>Name<input value={webhookName} onChange={(event) => setWebhookName(event.target.value)} maxLength={120} /></label>
              <label>Type<select value={webhookKind} onChange={(event) => setWebhookKind(event.target.value as "discord" | "generic")}><option value="discord">Discord incoming webhook</option><option value="generic">Generic signed webhook</option></select></label>
            </div>
            <label>HTTPS endpoint<input type="url" value={webhookURL} onChange={(event) => setWebhookURL(event.target.value)} placeholder={webhookKind === "discord" ? "https://discord.com/api/webhooks/…" : "https://events.example.com/dockside"} /></label>
            <fieldset>
              <legend>Deliver events</legend>
              <div className="webhook-filter-grid">
                {webhookEventOptions.map((option) => (
                  <label key={option.value}>
                    <input
                      type="checkbox"
                      checked={filters.includes(option.value)}
                      onChange={(event) => setFilters((current) =>
                        event.target.checked
                          ? [...current, option.value]
                          : current.filter((value) => value !== option.value)
                      )}
                    /> {option.label}
                  </label>
                ))}
              </div>
            </fieldset>
            <button className="primary-button" disabled={create.isPending || !webhookName.trim() || !webhookURL.trim()} onClick={() => create.mutate()}><Webhook size={14} /> {create.isPending ? "Saving..." : "Add webhook"}</button>
            {create.isError && <div className="form-error">{create.error.message}</div>}
            {generatedSecret && (
              <div className="generated-secret">
                <strong>Copy the signing secret now</strong>
                <p>It is shown once and signs generic webhook bodies with HMAC-SHA256.</p>
                <div><code>{generatedSecret}</code><button className="icon-button" onClick={() => void navigator.clipboard.writeText(generatedSecret)}><Copy size={13} /></button></div>
              </div>
            )}
          </div>
        </section>
        <section className="panel">
          <div className="panel-heading"><div><span className="eyebrow">DESTINATIONS</span><h2>Configured webhooks</h2></div><button className="icon-button" onClick={() => void destinations.refetch()}><RefreshCw size={14} /></button></div>
          {destinations.isLoading && <div className="file-state"><span className="loader" /></div>}
          {destinations.isError && <ErrorPanel error={destinations.error} retry={() => void destinations.refetch()} />}
          {destinations.data?.webhooks.length === 0 && <EmptyState icon={Webhook} title="No webhooks" description="Send errors, warnings, starts, restarts, and automation events to Discord or another HTTPS endpoint." />}
          <div className="webhook-list">
            {destinations.data?.webhooks.map((destination) => (
              <article key={destination.id}>
                <span className={`webhook-state ${destination.enabled ? "on" : ""}`}><Webhook size={15} /></span>
                <div><strong>{destination.name}</strong><span>{destination.kind} · {destination.url_preview}</span><small>{destination.event_filters.length ? destination.event_filters.join(", ") : "All server events"}</small></div>
                <div>
                  <button className="secondary-button compact" onClick={() => test.mutate(destination)}>Test</button>
                  <button className="icon-button" onClick={() => toggle.mutate({ destination, enabled: !destination.enabled })}>{destination.enabled ? <CircleStop size={13} /> : <Play size={13} />}</button>
                  <button className="icon-button danger" onClick={() => window.confirm(`Delete webhook “${destination.name}”?`) && removeWebhook.mutate(destination)}><Trash2 size={13} /></button>
                </div>
              </article>
            ))}
          </div>
          {(webhookError || test.isSuccess) && <div className={webhookError ? "form-error" : "form-success"}>{webhookError ? webhookError.message : "Test event queued for delivery."}</div>}
        </section>
      </div>
      <section className="panel danger-zone">
        <div><span className="eyebrow">DANGER ZONE</span><h2>Delete server permanently</h2><p>Removes the container, isolated network, data volume, backup archives, schedules, and database metadata.</p></div>
        <button className="danger-button" onClick={() => setShowDelete(true)}><Trash2 size={14} /> Delete server</button>
      </section>
      {showDelete && (
        <div className="dialog-backdrop">
          <div className="dialog" role="dialog" aria-modal="true">
            <h2>Delete {server.name}?</h2>
            <p>This permanently removes the complete managed server and its data. Type the exact server name to continue.</p>
            <label>Server name<input value={deleteName} onChange={(event) => setDeleteName(event.target.value)} placeholder={server.name} /></label>
            {deleteServer.isError && <div className="form-error">{deleteServer.error.message}</div>}
            <div className="dialog-actions"><button className="secondary-button" onClick={() => setShowDelete(false)}>Cancel</button><button className="danger-button" disabled={deleteName !== server.name || deleteServer.isPending} onClick={() => deleteServer.mutate()}>{deleteServer.isPending ? "Deleting everything..." : "Permanently delete"}</button></div>
          </div>
        </div>
      )}
    </>
  );
}

function ServerActivity({ server }: { server: ServerSummary }) {
  const activity = useQuery({
    queryKey: ["server-activity", server.id],
    queryFn: () => api<{ activity: ActivityEvent[] }>(`/api/v1/servers/${server.id}/activity?limit=150`),
    refetchInterval: 5_000,
  });
  return (
    <section className="panel">
      <div className="panel-heading">
        <div><span className="eyebrow">IMMUTABLE EVENT HISTORY</span><h2>Server activity</h2></div>
        <button className="icon-button" onClick={() => void activity.refetch()}><RefreshCw size={14} /></button>
      </div>
      {activity.isLoading && <div className="file-state"><span className="loader" /></div>}
      {activity.isError && <ErrorPanel error={activity.error} retry={() => void activity.refetch()} />}
      {activity.data?.activity.length === 0 && <EmptyState icon={Activity} title="No activity yet" description="Power actions, schedules, backups, file changes, and crashes will appear here." />}
      <div className="server-activity-list">
        {activity.data?.activity.map((event) => (
          <article key={event.id}>
            <span className={`activity-mark ${event.severity}`}><Activity size={14} /></span>
            <div>
              <strong>{event.summary}</strong>
              <code>{event.event_type}</code>
              <span>{formatTimestamp(event.created_at)}</span>
            </div>
            {Boolean(event.data) && Object.keys(event.data as object).length > 0 && (
              <details>
                <summary>Details</summary>
                <pre>{JSON.stringify(event.data, null, 2)}</pre>
              </details>
            )}
          </article>
        ))}
      </div>
    </section>
  );
}

type DraftScheduleTask = {
  key: number;
  task_type: "backup" | "power" | "command" | "delay" | "notify";
  value: string;
  includes: string;
  excludes: string;
  retention: string;
  discordWebhookID: string;
  discordFormat: "zip" | "archive";
};

const cronPresets = [
  { label: "Every 5 minutes", value: "*/5 * * * *" },
  { label: "Every 15 minutes", value: "*/15 * * * *" },
  { label: "Every hour", value: "0 * * * *" },
  { label: "Daily at 4:00 AM", value: "0 4 * * *" },
  { label: "Weekly Sunday 4:00 AM", value: "0 4 * * 0" },
];

function ServerSchedules({ server }: { server: ServerSummary }) {
  const queryClient = useQueryClient();
  const [name, setName] = useState("Automated maintenance");
  const [cronExpression, setCronExpression] = useState("0 4 * * *");
  const [timezone, setTimezone] = useState(Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC");
  const [enabled, setEnabled] = useState(true);
  const [tasks, setTasks] = useState<DraftScheduleTask[]>([
    { key: 1, task_type: "backup", value: "Scheduled backup", includes: "", excludes: "logs/*\n*.log", retention: "", discordWebhookID: "", discordFormat: "zip" },
    { key: 2, task_type: "power", value: "restart", includes: "", excludes: "", retention: "", discordWebhookID: "", discordFormat: "zip" },
  ]);
  const schedules = useQuery({
    queryKey: ["server-schedules", server.id],
    queryFn: () => api<{ schedules: ServerSchedule[] }>(`/api/v1/servers/${server.id}/schedules`),
    refetchInterval: 10_000,
  });
  const webhooks = useQuery({
    queryKey: ["server-webhooks", server.id],
    queryFn: () => api<{ webhooks: ServerWebhook[] }>(`/api/v1/servers/${server.id}/webhooks`),
  });
  const discordWebhooks = webhooks.data?.webhooks.filter(
    (destination) => destination.kind === "discord" && destination.enabled,
  ) ?? [];
  const refresh = () => queryClient.invalidateQueries({ queryKey: ["server-schedules", server.id] });
  const create = useMutation({
    mutationFn: () =>
      api<ServerSchedule>(`/api/v1/servers/${server.id}/schedules`, {
        method: "POST",
        body: JSON.stringify({
          name: name.trim(),
          cron_expression: cronExpression.trim(),
          timezone: timezone.trim(),
          enabled,
          tasks: tasks.map((task) => ({
            task_type: task.task_type,
            timeout_seconds: task.task_type === "delay" ? Math.max(Number(task.value) + 30, 60) : 300,
            config: scheduleTaskConfig(task),
          })),
        }),
      }),
    onSuccess: () => void refresh(),
  });
  const toggle = useMutation({
    mutationFn: ({ schedule, enabled }: { schedule: ServerSchedule; enabled: boolean }) =>
      api<void>(`/api/v1/servers/${server.id}/schedules/${schedule.id}`, {
        method: "PATCH",
        body: JSON.stringify({ enabled }),
      }),
    onSuccess: () => void refresh(),
  });
  const run = useMutation({
    mutationFn: (schedule: ServerSchedule) =>
      api<void>(`/api/v1/servers/${server.id}/schedules/${schedule.id}/run`, { method: "POST" }),
    onSuccess: () => void refresh(),
  });
  const remove = useMutation({
    mutationFn: (schedule: ServerSchedule) =>
      api<void>(`/api/v1/servers/${server.id}/schedules/${schedule.id}`, { method: "DELETE" }),
    onSuccess: () => void refresh(),
  });
  const actionError = create.error || toggle.error || run.error || remove.error;

  function updateTask(key: number, patch: Partial<DraftScheduleTask>) {
    setTasks((current) => current.map((task) => task.key === key ? { ...task, ...patch } : task));
  }

  return (
    <div className="schedule-layout">
      <section className="panel schedule-builder">
        <div className="panel-heading"><div><span className="eyebrow">CRON BUILDER</span><h2>New schedule</h2></div></div>
        <div className="schedule-form">
          <label>Name<input value={name} maxLength={120} onChange={(event) => setName(event.target.value)} /></label>
          <div className="form-grid two">
            <label>Preset<select value={cronPresets.some((preset) => preset.value === cronExpression) ? cronExpression : "custom"} onChange={(event) => event.target.value !== "custom" && setCronExpression(event.target.value)}>{cronPresets.map((preset) => <option value={preset.value} key={preset.value}>{preset.label}</option>)}<option value="custom">Custom expression</option></select></label>
            <label>Timezone<input value={timezone} onChange={(event) => setTimezone(event.target.value)} /></label>
          </div>
          <label>Cron expression<input className="mono" value={cronExpression} onChange={(event) => setCronExpression(event.target.value)} placeholder="0 4 * * *" /></label>
          <div className="schedule-task-heading"><strong>Ordered tasks</strong><button className="secondary-button compact" onClick={() => setTasks((current) => [...current, { key: Date.now(), task_type: "command", value: "", includes: "", excludes: "", retention: "", discordWebhookID: "", discordFormat: "zip" }])}><Plus size={13} /> Add task</button></div>
          <div className="schedule-task-list">
            {tasks.map((task, index) => (
              <div className="schedule-task" key={task.key}>
                <span>{index + 1}</span>
                <select value={task.task_type} onChange={(event) => updateTask(task.key, { task_type: event.target.value as DraftScheduleTask["task_type"], value: event.target.value === "power" ? "restart" : "" })}>
                  <option value="backup">Backup</option><option value="power">Power</option><option value="command">Console command</option><option value="delay">Delay</option><option value="notify">Notification</option>
                </select>
                {task.task_type === "power" ? (
                  <select value={task.value} onChange={(event) => updateTask(task.key, { value: event.target.value })}><option value="restart">Restart</option><option value="start">Start</option><option value="stop">Stop</option><option value="kill">Kill</option></select>
                ) : (
                  <input value={task.value} onChange={(event) => updateTask(task.key, { value: event.target.value })} type={task.task_type === "delay" ? "number" : "text"} min={task.task_type === "delay" ? 1 : undefined} max={task.task_type === "delay" ? 3600 : undefined} placeholder={task.task_type === "backup" ? "Backup name" : task.task_type === "command" ? "say Restarting soon" : task.task_type === "delay" ? "Seconds" : "Activity notification"} />
                )}
                <button className="icon-button danger" disabled={tasks.length === 1} onClick={() => setTasks((current) => current.filter((item) => item.key !== task.key))}><Trash2 size={13} /></button>
                {task.task_type === "backup" && (
                  <div className="schedule-backup-rules">
                    <textarea value={task.includes} onChange={(event) => updateTask(task.key, { includes: event.target.value })} placeholder="Include paths (blank = all)" />
                    <textarea value={task.excludes} onChange={(event) => updateTask(task.key, { excludes: event.target.value })} placeholder="Exclude globs" />
                    <input type="number" min={1} max={3650} value={task.retention} onChange={(event) => updateTask(task.key, { retention: event.target.value })} placeholder="Retention days (blank = indefinitely)" />
                    <select value={task.discordWebhookID} onChange={(event) => updateTask(task.key, { discordWebhookID: event.target.value })}>
                      <option value="">No Discord attachment</option>
                      {discordWebhooks.map((destination) => <option value={destination.id} key={destination.id}>{destination.name}</option>)}
                    </select>
                    {task.discordWebhookID && (
                      <select value={task.discordFormat} onChange={(event) => updateTask(task.key, { discordFormat: event.target.value as "zip" | "archive" })}>
                        <option value="zip">ZIP export</option>
                        <option value="archive">Native tar.gz</option>
                      </select>
                    )}
                  </div>
                )}
              </div>
            ))}
          </div>
          <label className="checkbox-row"><input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} /><span><strong>Enable immediately</strong><small>The worker will calculate the first run in {timezone}.</small></span></label>
          <button className="primary-button schedule-create-button" disabled={create.isPending || !name.trim() || !cronExpression.trim() || tasks.some((task) => !task.value.trim())} onClick={() => create.mutate()}><Clock3 size={15} /> {create.isPending ? "Creating..." : "Create schedule"}</button>
          {create.isError && <div className="form-error">{create.error.message}</div>}
        </div>
      </section>
      <section className="panel">
        <div className="panel-heading"><div><span className="eyebrow">AUTOMATION</span><h2>Schedules</h2></div><button className="icon-button" onClick={() => void schedules.refetch()}><RefreshCw size={14} /></button></div>
        {schedules.isLoading && <div className="file-state"><span className="loader" /></div>}
        {schedules.isError && <ErrorPanel error={schedules.error} retry={() => void schedules.refetch()} />}
        {schedules.data?.schedules.length === 0 && <EmptyState icon={Clock3} title="No schedules" description="Build ordered cron automations for this server." />}
        <div className="schedule-list">
          {schedules.data?.schedules.map((schedule) => (
            <article key={schedule.id}>
              <div className={`schedule-enabled-dot ${schedule.enabled ? "on" : ""}`} />
              <div>
                <strong>{schedule.name}</strong>
                <code>{schedule.cron_expression}</code>
                <span>{schedule.tasks.length} task{schedule.tasks.length === 1 ? "" : "s"} · {schedule.timezone} · Next {schedule.next_run_at ? formatTimestamp(schedule.next_run_at) : "disabled"}</span>
              </div>
              <div className="schedule-actions">
                <button className="secondary-button compact" disabled={run.isPending} onClick={() => run.mutate(schedule)}><Play size={12} /> Run now</button>
                <button className="icon-button" onClick={() => toggle.mutate({ schedule, enabled: !schedule.enabled })} title={schedule.enabled ? "Disable" : "Enable"}>{schedule.enabled ? <CircleStop size={13} /> : <Play size={13} />}</button>
                <button className="icon-button danger" onClick={() => window.confirm(`Delete schedule “${schedule.name}”?`) && remove.mutate(schedule)}><Trash2 size={13} /></button>
              </div>
            </article>
          ))}
        </div>
        {actionError && <div className="form-error">{actionError.message}</div>}
      </section>
    </div>
  );
}

function scheduleTaskConfig(task: DraftScheduleTask) {
  switch (task.task_type) {
    case "backup": return {
      name: task.value.trim(),
      include_paths: parseRules(task.includes),
      exclude_globs: parseRules(task.excludes),
      retention_days: task.retention === "" ? null : Number(task.retention),
      discord_webhook_id: task.discordWebhookID || null,
      discord_format: task.discordWebhookID ? task.discordFormat : "",
    };
    case "power": return { action: task.value };
    case "command": return { command: task.value.trim() };
    case "delay": return { seconds: Number(task.value) };
    case "notify": return { message: task.value.trim() };
  }
}

type ConsoleFrame = {
  stream: "stdout" | "stderr" | "system";
  phase?: "provision" | "installer" | "runtime" | "system";
  severity?: "info" | "notice" | "warning" | "error" | "fatal";
  message: string;
  observed_at: string;
};

function ServerConsole({ server }: { server: ServerSummary }) {
  const [frames, setFrames] = useState<ConsoleFrame[]>([]);
  const [connected, setConnected] = useState(false);
  const [streamError, setStreamError] = useState("");
  const [command, setCommand] = useState("");
  const viewport = useRef<HTMLDivElement>(null);
  const terminalStream = server.status === "stopped" || server.status === "failed";
  const canStream = terminalStream ||
    ["installing", "starting", "running", "restarting", "stopping"].includes(server.status);
  const displayConnected = connected && canStream && !terminalStream;
  const sendCommand = useMutation({
    mutationFn: (value: string) =>
      api<{ transport?: string; response?: string } | void>(`/api/v1/servers/${server.id}/console`, {
        method: "POST",
        body: JSON.stringify({ command: value }),
      }),
    onSuccess: (result) => {
      setCommand("");
      if (result?.transport === "http_rest") {
        const frame: ConsoleFrame = {
          stream: "system", phase: "runtime", severity: "info",
          message: result.response ? `REST response: ${result.response}` : "REST command accepted",
          observed_at: new Date().toISOString(),
        };
        setFrames((current) => [...current, frame].slice(-2_000));
      }
    },
  });

  useEffect(() => {
    if (!canStream) {
      return;
    }
    const controller = new AbortController();
    let disposed = false;
    async function connect() {
      let retryDelay = 1_500;
      while (!disposed) {
        try {
          setStreamError("");
          const response = await fetch(`/api/v1/servers/${server.id}/console`, {
            credentials: "same-origin",
            headers: { Accept: "application/x-ndjson" },
            signal: controller.signal,
          });
          if (!response.ok || !response.body) {
            throw new Error(`Console stream unavailable (${response.status})`);
          }
          setConnected(true);
          retryDelay = 1_500;
          const reader = response.body.getReader();
          const decoder = new TextDecoder();
          let pending = "";
          while (!disposed) {
            const result = await reader.read();
            if (result.done) break;
            pending += decoder.decode(result.value, { stream: true });
            const lines = pending.split("\n");
            pending = lines.pop() ?? "";
            const incoming: ConsoleFrame[] = [];
            for (const line of lines) {
              if (!line.trim()) continue;
              try {
                incoming.push(JSON.parse(line) as ConsoleFrame);
              } catch {
                incoming.push({
                  stream: "system",
                  message: line,
                  observed_at: new Date().toISOString(),
                });
              }
            }
            if (incoming.length) {
              setFrames((current) => [...current, ...incoming].slice(-2_000));
            }
          }
          if (terminalStream) return;
        } catch (error) {
          if (!disposed && !controller.signal.aborted) {
            setStreamError(error instanceof Error ? error.message : "Console disconnected");
          }
        } finally {
          if (!disposed) setConnected(false);
        }
        if (!disposed) {
          await new Promise((resolve) => window.setTimeout(resolve, retryDelay));
          retryDelay = Math.min(retryDelay * 2, 15_000);
        }
      }
    }
    void connect();
    return () => {
      disposed = true;
      controller.abort();
    };
  }, [server.id, server.status, canStream, terminalStream]);

  useEffect(() => {
    const element = viewport.current;
    if (element) element.scrollTop = element.scrollHeight;
  }, [frames]);

  function submit(event: FormEvent) {
    event.preventDefault();
    const value = command.trim();
    if (value) sendCommand.mutate(value);
  }

  const cpuCapacity = server.resources.cpu_limit_millicores ? server.resources.cpu_limit_millicores / 10 : Math.max(server.runtime.cpu_percent ?? 0, 100);
  const memoryCapacity = server.resources.memory_limit_bytes ?? server.runtime.memory_limit_bytes;
  return (
    <>
    <section className="server-resource-meters compact">
      <ServerUsageMeter icon={Cpu} label="CPU" percent={cpuCapacity ? ((server.runtime.cpu_percent ?? 0) / cpuCapacity) * 100 : 0} detail={`${formatPercent(server.runtime.cpu_percent)} current`} />
      <ServerUsageMeter icon={MemoryStick} label="Memory" percent={memoryCapacity ? ((server.runtime.memory_bytes ?? 0) / memoryCapacity) * 100 : 0} detail={`${formatBytes(server.runtime.memory_bytes)} / ${memoryCapacity ? formatBytes(memoryCapacity) : "unlimited"}`} />
      <ServerUsageMeter icon={HardDrive} label="Disk" percent={server.resources.disk_limit_bytes ? ((server.runtime.disk_bytes ?? 0) / server.resources.disk_limit_bytes) * 100 : 0} available={Boolean(server.resources.disk_limit_bytes)} detail={`${formatBytes(server.runtime.disk_bytes)} used`} />
    </section>
    <section className="panel console-panel">
      <div className="panel-heading console-heading">
        <div>
          <span className="eyebrow">LIVE CONTAINER OUTPUT</span>
          <h2>Server console</h2>
        </div>
        <div className={`console-connection ${displayConnected ? "online" : ""}`}>
          <span />{" "}
          {terminalStream
            ? server.status === "failed"
              ? "Unavailable"
              : "Stopped"
            : displayConnected
              ? "Connected"
              : "Reconnecting"}
        </div>
      </div>
      <div className="console-viewport" ref={viewport} role="log" aria-live="polite">
        {frames.length === 0 && (
          <div className="console-empty">Waiting for container output...</div>
        )}
        {frames.map((frame, index) => (
          <div className={`console-line ${frame.stream} severity-${frame.severity ?? "info"}`} key={`${frame.observed_at}-${index}`}>
            <time>{new Date(frame.observed_at).toLocaleTimeString()}</time>
            <span className="console-stream">{frame.phase && frame.phase !== "runtime" ? frame.phase : frame.stream}</span>
            <span>{frame.message}</span>
          </div>
        ))}
      </div>
      {(streamError || sendCommand.isError) && (
        <div className="console-error">
          {sendCommand.isError ? sendCommand.error.message : streamError}
        </div>
      )}
      <form className="console-command" onSubmit={submit}>
        <span>&gt;</span>
        <input
          value={command}
          onChange={(event) => setCommand(event.target.value)}
          disabled={server.status !== "running" || sendCommand.isPending}
          maxLength={2_048}
          placeholder={
            server.status !== "running"
              ? "Start the server to send commands"
              : "Enter a game server command..."
          }
          aria-label="Game server console command"
        />
        <button
          className="button primary"
          type="submit"
          disabled={server.status !== "running" || sendCommand.isPending || !command.trim()}
        >
          Send
        </button>
      </form>
    </section>
    </>
  );
}

function ServerOverview({ server }: { server: ServerSummary }) {
  const cpuCapacity = server.resources.cpu_limit_millicores
    ? server.resources.cpu_limit_millicores / 10
    : Math.max(server.runtime.cpu_percent ?? 0, 100);
  const cpuPercent = cpuCapacity ? ((server.runtime.cpu_percent ?? 0) / cpuCapacity) * 100 : 0;
  const memoryCapacity = server.resources.memory_limit_bytes ?? server.runtime.memory_limit_bytes;
  const memoryPercent = memoryCapacity ? ((server.runtime.memory_bytes ?? 0) / memoryCapacity) * 100 : 0;
  const diskCapacity = server.resources.disk_limit_bytes;
  const diskPercent = diskCapacity ? ((server.runtime.disk_bytes ?? 0) / diskCapacity) * 100 : 0;
  return (
    <>
      <section className="server-resource-meters">
        <ServerUsageMeter icon={Cpu} label="CPU" percent={cpuPercent} detail={`${formatPercent(server.runtime.cpu_percent)} used${server.resources.cpu_limit_millicores ? ` / ${server.resources.cpu_limit_millicores} millicores` : " · unlimited"}`} />
        <ServerUsageMeter icon={MemoryStick} label="Memory" percent={memoryPercent} detail={`${formatBytes(server.runtime.memory_bytes)} / ${memoryCapacity ? formatBytes(memoryCapacity) : "unlimited"}`} />
        <ServerUsageMeter icon={HardDrive} label="Disk" percent={diskPercent} available={Boolean(diskCapacity)} detail={`${formatBytes(server.runtime.disk_bytes)}${diskCapacity ? ` / ${formatBytes(diskCapacity)} alert limit` : " · no alert limit"}`} />
        <ServerUsageMeter icon={Network} label="Network I/O" percent={0} available={false} detail={`${formatBytes((server.runtime.network_rx_bytes ?? 0) + (server.runtime.network_tx_bytes ?? 0))} transferred`} />
      </section>
      <div className="dashboard-columns">
        <section className="panel">
          <div className="panel-heading"><div><span className="eyebrow">RUNTIME</span><h2>Container configuration</h2></div></div>
          <dl className="detail-list">
            <div><dt>Container</dt><dd className="mono">{server.container_id?.slice(0, 16) || "Waiting for provisioning"}</dd></div>
            <div><dt>Image</dt><dd className="mono">{server.image_reference}</dd></div>
            <div><dt>CPU limit</dt><dd>{server.resources.cpu_limit_millicores ? `${server.resources.cpu_limit_millicores} millicores` : "Unlimited"}</dd></div>
            <div><dt>Memory limit</dt><dd>{server.resources.memory_limit_bytes ? formatBytes(server.resources.memory_limit_bytes) : "Unlimited"}</dd></div>
            <div><dt>PID limit</dt><dd>{server.resources.pids_limit ?? "Unlimited"}</dd></div>
          </dl>
        </section>
        <section className="panel">
          <div className="panel-heading"><div><span className="eyebrow">ALLOCATION</span><h2>Primary endpoint</h2></div></div>
          {server.primary_port ? (
            <div className="endpoint-card">
              <Network size={23} />
              <div><span>Local game address</span><strong>{server.primary_port.bind_address === "0.0.0.0" ? "127.0.0.1" : server.primary_port.bind_address}:{server.primary_port.host_port}</strong><small>Published {server.primary_port.protocol.toUpperCase()} · container {server.primary_port.container_port}/{server.primary_port.protocol}</small></div>
              <button className="icon-button" title="Copy port" onClick={() => void navigator.clipboard.writeText(String(server.primary_port?.host_port))}><Copy size={16} /></button>
            </div>
          ) : <EmptyState icon={Network} title="No endpoint" description="This server has no published primary port." />}
        </section>
      </div>
    </>
  );
}

function ServerUsageMeter({
  icon: Icon, label, percent, detail, available = true,
}: {
  icon: typeof Cpu;
  label: string;
  percent: number;
  detail: string;
  available?: boolean;
}) {
  const normalized = available ? Math.max(0, Math.min(100, percent)) : 0;
  const tone = normalized >= 90 ? "danger" : normalized >= 75 ? "warning" : "";
  return <article className={`server-usage-meter ${tone} ${available ? "" : "informational"}`}>
    <div><span><Icon size={17} /> {label}</span><strong>{available ? `${normalized.toFixed(1)}%` : "Live total"}</strong></div>
    <span className="server-usage-track" role={available ? "meter" : undefined} aria-valuenow={available ? normalized : undefined}><span style={{ width: `${normalized}%` }} /></span>
    <small>{detail}</small>
  </article>;
}

function formatPercent(value: number | null) {
  return value == null ? "—" : `${value.toFixed(1)}%`;
}

function formatBytes(value: number | null | undefined) {
  if (value == null) return "—";
  if (value === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  return `${(value / 1024 ** index).toFixed(index > 2 ? 1 : 0)} ${units[index]}`;
}

function formatTimestamp(value: string) {
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(new Date(value));
}
