package templates

import "encoding/json"

const BundleFormatVersion = 1

type Bundle struct {
	FormatVersion int             `json:"format_version"`
	GeneratedAt   string          `json:"generated_at"`
	Sources       []BundleSource  `json:"sources"`
	Templates     []TemplateEntry `json:"templates"`
}

type BundleSource struct {
	Kind   string `json:"kind"`
	Digest string `json:"digest"`
	Count  int    `json:"count"`
}

type TemplateEntry struct {
	Slug                string              `json:"slug"`
	Name                string              `json:"name"`
	Category            string              `json:"category"`
	SourceKind          string              `json:"source_kind"`
	UpstreamURL         string              `json:"upstream_url"`
	Author              string              `json:"author,omitempty"`
	Description         string              `json:"description,omitempty"`
	SourceDigest        string              `json:"source_digest"`
	SourceDocument      json.RawMessage     `json:"source_document"`
	CanonicalDocument   CanonicalTemplate   `json:"canonical_document"`
	CompatibilityReport CompatibilityReport `json:"compatibility_report"`
}

type CanonicalTemplate struct {
	APIVersion        string            `json:"api_version"`
	Name              string            `json:"name"`
	Description       string            `json:"description,omitempty"`
	SourceKind        string            `json:"source_kind"`
	Category          string            `json:"category"`
	Images            map[string]string `json:"images"`
	DefaultImage      string            `json:"default_image"`
	StartupCommand    string            `json:"startup_command"`
	StopCommand       string            `json:"stop_command,omitempty"`
	InstallContainer  string            `json:"install_container,omitempty"`
	InstallEntrypoint string            `json:"install_entrypoint,omitempty"`
	InstallScript     string            `json:"install_script,omitempty"`
	Variables         []Variable        `json:"variables"`
	NetworkPorts      []NetworkPort     `json:"network_ports"`
	CommandTransport  CommandTransport  `json:"command_transport"`
	BackupDefaults    BackupDefaults    `json:"backup_defaults"`
	ResourceDefaults  ResourceDefaults  `json:"resource_defaults"`
	FileDenylist      []string          `json:"file_denylist,omitempty"`
	Features          json.RawMessage   `json:"features,omitempty"`
}

// CommandTransport declares how a command entered in the Dockside console is
// delivered to the game server. Imported templates default to auto detection.
type CommandTransport struct {
	Type            string                `json:"type"`
	RCONPortEnv     string                `json:"rcon_port_env,omitempty"`
	RCONPasswordEnv string                `json:"rcon_password_env,omitempty"`
	REST            *RESTCommandTransport `json:"rest,omitempty"`
}

// RESTCommandTransport is limited to an HTTP service listening inside the game
// container network namespace. Templates cannot configure an arbitrary host.
type RESTCommandTransport struct {
	Method          string            `json:"method"`
	Port            int               `json:"port"`
	PortEnvironment string            `json:"port_environment,omitempty"`
	Path            string            `json:"path"`
	BodyTemplate    string            `json:"body_template,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	AcceptedStatus  []int             `json:"accepted_status,omitempty"`
	TimeoutSeconds  int               `json:"timeout_seconds,omitempty"`
}

type BackupDefaults struct {
	IncludePaths  []string `json:"include_paths,omitempty"`
	ExcludeGlobs  []string `json:"exclude_globs,omitempty"`
	RetentionDays *int     `json:"retention_days,omitempty"`
}

type ResourceDefaults struct {
	CPULimitMillicores *int   `json:"cpu_limit_millicores,omitempty"`
	MemoryLimitMB      *int64 `json:"memory_limit_mb,omitempty"`
	DiskAlertLimitMB   *int64 `json:"disk_alert_limit_mb,omitempty"`
}

// NetworkPort is the portable network contract shared by imported and custom
// templates. A zero container port or empty protocol means that the upstream
// definition did not contain enough information and the installer must ask.
type NetworkPort struct {
	Name          string `json:"name"`
	Purpose       string `json:"purpose"`
	ContainerPort int    `json:"container_port"`
	Protocol      string `json:"protocol"`
	Primary       bool   `json:"primary"`
	Required      bool   `json:"required"`
	Published     bool   `json:"published"`
	Environment   string `json:"environment,omitempty"`
}

type Variable struct {
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	Environment  string `json:"environment"`
	DefaultValue string `json:"default_value"`
	UserViewable bool   `json:"user_viewable"`
	UserEditable bool   `json:"user_editable"`
	Rules        string `json:"rules,omitempty"`
	FieldType    string `json:"field_type,omitempty"`
	Secret       bool   `json:"secret"`
}

type CompatibilityReport struct {
	Compatible bool     `json:"compatible"`
	Warnings   []string `json:"warnings"`
}

type CatalogIndex struct {
	PanelType string        `json:"panel_type"`
	Nests     []CatalogNest `json:"nests"`
}

type CatalogNest struct {
	Type string        `json:"nest_type"`
	Eggs []CatalogItem `json:"Eggs"`
}

type CatalogItem struct {
	Egg         CatalogEgg `json:"egg"`
	DownloadURL string     `json:"download_url"`
	Readme      string     `json:"readme"`
}

type CatalogEgg struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Meta        json.RawMessage `json:"meta"`
}
