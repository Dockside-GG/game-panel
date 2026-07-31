package engineclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// APIError preserves a structured failure returned by the isolated engine so
// callers can present an actionable, sanitized explanation.
type APIError struct {
	Status    int
	Code      string
	Detail    string
	Retryable bool
}

func (e *APIError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("engine returned HTTP %d", e.Status)
	}
	return fmt.Sprintf("engine returned HTTP %d: %s", e.Status, e.Detail)
}

type HostStatus struct {
	Connected            bool             `json:"connected"`
	InstanceID           string           `json:"instance_id"`
	EngineVersion        string           `json:"engine_version"`
	APIVersion           string           `json:"api_version"`
	OperatingSystem      string           `json:"operating_system"`
	Architecture         string           `json:"architecture"`
	KernelVersion        string           `json:"kernel_version"`
	CPUs                 int              `json:"cpus"`
	MemoryBytes          int64            `json:"memory_bytes"`
	CPUUsagePercent      float64          `json:"cpu_usage_percent"`
	MemoryUsedBytes      int64            `json:"memory_used_bytes"`
	MemoryAvailableBytes int64            `json:"memory_available_bytes"`
	Load1                float64          `json:"load_1"`
	Load5                float64          `json:"load_5"`
	Load15               float64          `json:"load_15"`
	TelemetryAvailable   bool             `json:"telemetry_available"`
	TelemetryScope       string           `json:"telemetry_scope"`
	DataFilesystem       *FilesystemUsage `json:"data_filesystem,omitempty"`
	BackupFilesystem     *FilesystemUsage `json:"backup_filesystem,omitempty"`
	Containers           int              `json:"containers"`
	ContainersRunning    int              `json:"containers_running"`
	ContainersStopped    int              `json:"containers_stopped"`
	Images               int              `json:"images"`
	SecurityOptions      []string         `json:"security_options"`
	Warnings             []string         `json:"warnings"`
	ObservedAt           time.Time        `json:"observed_at"`
}

type FilesystemUsage struct {
	Path       string `json:"path"`
	TotalBytes int64  `json:"total_bytes"`
	UsedBytes  int64  `json:"used_bytes"`
	FreeBytes  int64  `json:"free_bytes"`
}

type SystemContainer struct {
	Component        string     `json:"component"`
	ContainerID      string     `json:"container_id"`
	Image            string     `json:"image"`
	State            string     `json:"state"`
	Health           string     `json:"health"`
	CPUPercent       float64    `json:"cpu_percent"`
	MemoryBytes      int64      `json:"memory_bytes"`
	MemoryLimitBytes int64      `json:"memory_limit_bytes"`
	NetworkRXBytes   int64      `json:"network_rx_bytes"`
	NetworkTXBytes   int64      `json:"network_tx_bytes"`
	BlockReadBytes   int64      `json:"block_read_bytes"`
	BlockWriteBytes  int64      `json:"block_write_bytes"`
	RestartCount     int        `json:"restart_count"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	ObservedAt       time.Time  `json:"observed_at"`
	Error            string     `json:"error,omitempty"`
}

type SystemContainerLogs struct {
	Component  string    `json:"component"`
	Stdout     string    `json:"stdout"`
	Stderr     string    `json:"stderr"`
	ObservedAt time.Time `json:"observed_at"`
}

type Port struct {
	HostIP        string `json:"host_ip"`
	HostPort      int    `json:"host_port"`
	ContainerPort int    `json:"container_port"`
	Protocol      string `json:"protocol"`
}

type Resources struct {
	CPUMillicores          *int   `json:"cpu_millicores,omitempty"`
	CPUSet                 string `json:"cpu_set,omitempty"`
	MemoryLimitBytes       *int64 `json:"memory_limit_bytes,omitempty"`
	MemoryReservationBytes *int64 `json:"memory_reservation_bytes,omitempty"`
	SwapLimitBytes         *int64 `json:"swap_limit_bytes,omitempty"`
	PidsLimit              *int64 `json:"pids_limit,omitempty"`
	IOWeight               *int   `json:"io_weight,omitempty"`
}

type InstallSpec struct {
	Image       string            `json:"image"`
	Entrypoint  string            `json:"entrypoint"`
	Script      string            `json:"script"`
	Environment map[string]string `json:"environment"`
}

type ProvisionRequest struct {
	Image       string            `json:"image"`
	Startup     string            `json:"startup"`
	Environment map[string]string `json:"environment"`
	Ports       []Port            `json:"ports"`
	Resources   Resources         `json:"resources"`
	Install     *InstallSpec      `json:"install,omitempty"`
	Start       bool              `json:"start"`
}

type ProvisionResult struct {
	ContainerID string `json:"container_id"`
	VolumeName  string `json:"volume_name"`
	NetworkName string `json:"network_name"`
	State       string `json:"state"`
}

type ReconfigureRequest struct {
	Image       string            `json:"image"`
	Startup     string            `json:"startup"`
	Environment map[string]string `json:"environment"`
	Ports       []Port            `json:"ports"`
	Resources   Resources         `json:"resources"`
}

type ServerStats struct {
	ServerID         string     `json:"server_id"`
	ContainerID      string     `json:"container_id"`
	State            string     `json:"state"`
	Health           string     `json:"health"`
	CPUPercent       float64    `json:"cpu_percent"`
	MemoryBytes      int64      `json:"memory_bytes"`
	MemoryLimitBytes int64      `json:"memory_limit_bytes"`
	NetworkRXBytes   int64      `json:"network_rx_bytes"`
	NetworkTXBytes   int64      `json:"network_tx_bytes"`
	BlockReadBytes   int64      `json:"block_read_bytes"`
	BlockWriteBytes  int64      `json:"block_write_bytes"`
	Pids             int64      `json:"pids"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	ExitCode         *int       `json:"exit_code,omitempty"`
	Error            string     `json:"error,omitempty"`
	ObservedAt       time.Time  `json:"observed_at"`
}

type FileEntry struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Type       string    `json:"type"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at"`
}

type FileList struct {
	Path    string      `json:"path"`
	Entries []FileEntry `json:"entries"`
}

type FileContent struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type FileDownload struct {
	Body               io.ReadCloser
	ContentType        string
	ContentDisposition string
	ContentLength      int64
}

type BackupResult struct {
	ObjectKey string `json:"object_key"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

type DatabaseRequest struct {
	Name          string `json:"name"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	AdminPassword string `json:"admin_password"`
}

type DatabaseResult struct {
	ContainerID string `json:"container_id"`
	VolumeName  string `json:"volume_name"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
}

func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Minute},
	}
}

func (c *Client) Host(ctx context.Context) (HostStatus, error) {
	var status HostStatus
	if err := c.do(ctx, http.MethodGet, "/v1/host", nil, &status); err != nil {
		return status, err
	}
	return status, nil
}

func (c *Client) SystemContainers(ctx context.Context) ([]SystemContainer, error) {
	var result struct {
		Containers []SystemContainer `json:"containers"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/system/containers", nil, &result); err != nil {
		return nil, err
	}
	return result.Containers, nil
}

func (c *Client) SystemContainerLogs(
	ctx context.Context,
	component string,
	tail int,
) (SystemContainerLogs, error) {
	var result SystemContainerLogs
	endpoint := fmt.Sprintf(
		"/v1/system/containers/%s/logs?tail=%d",
		url.PathEscape(component), tail,
	)
	if err := c.do(ctx, http.MethodGet, endpoint, nil, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (c *Client) RestartWorker(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/v1/system/containers/worker/restart", struct{}{}, nil)
}

func (c *Client) Power(ctx context.Context, serverID, action string) error {
	return c.do(ctx, http.MethodPost, "/v1/servers/"+serverID+"/power", map[string]string{"action": action}, nil)
}

func (c *Client) Provision(ctx context.Context, serverID string, input ProvisionRequest) (ProvisionResult, error) {
	var result ProvisionResult
	if err := c.do(ctx, http.MethodPost, "/v1/servers/"+serverID+"/provision", input, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (c *Client) InstallExisting(
	ctx context.Context,
	serverID string,
	input InstallSpec,
) error {
	return c.do(
		ctx, http.MethodPost, "/v1/servers/"+serverID+"/install", input, nil,
	)
}

func (c *Client) Reconfigure(ctx context.Context, serverID string, input ReconfigureRequest) (ProvisionResult, error) {
	var result ProvisionResult
	if err := c.do(ctx, http.MethodPut, "/v1/servers/"+serverID+"/configuration", input, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (c *Client) Delete(ctx context.Context, serverID string) error {
	return c.do(ctx, http.MethodDelete, "/v1/servers/"+serverID, nil, nil)
}

func (c *Client) Stats(ctx context.Context) ([]ServerStats, error) {
	var result struct {
		Servers []ServerStats `json:"servers"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/servers/stats", nil, &result); err != nil {
		return nil, err
	}
	return result.Servers, nil
}

func (c *Client) OpenConsole(ctx context.Context, serverID string, tail int) (io.ReadCloser, error) {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		fmt.Sprintf("%s/v1/servers/%s/console?tail=%d", c.baseURL, serverID, tail),
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create engine console request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "application/x-ndjson")
	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("open engine console: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return nil, decodeAPIError(response.StatusCode, message)
	}
	return response.Body, nil
}

type CommandResult struct {
	Transport string `json:"transport"`
	Response  string `json:"response,omitempty"`
}

func (c *Client) Command(ctx context.Context, serverID, command string) (CommandResult, error) {
	var result CommandResult
	err := c.do(ctx, http.MethodPost, "/v1/servers/"+serverID+"/console", map[string]string{
		"command": command,
	}, &result)
	return result, err
}

func (c *Client) ListFiles(ctx context.Context, serverID, path string) (FileList, error) {
	var result FileList
	endpoint := "/v1/servers/" + serverID + "/files?path=" + url.QueryEscape(path)
	if err := c.do(ctx, http.MethodGet, endpoint, nil, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (c *Client) UploadFile(
	ctx context.Context,
	serverID, path string,
	body io.Reader,
	size int64,
) error {
	endpoint := c.baseURL + "/v1/servers/" + serverID +
		"/files/upload?path=" + url.QueryEscape(path)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return fmt.Errorf("create engine upload request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Content-Type", "application/octet-stream")
	request.ContentLength = size
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("upload server file: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return decodeAPIError(response.StatusCode, message)
	}
	return nil
}

func (c *Client) ReadFile(ctx context.Context, serverID, path string) (FileContent, error) {
	var result FileContent
	endpoint := "/v1/servers/" + serverID + "/files/content?path=" + url.QueryEscape(path)
	if err := c.do(ctx, http.MethodGet, endpoint, nil, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (c *Client) OpenFileDownload(ctx context.Context, serverID, path string) (FileDownload, error) {
	endpoint := c.baseURL + "/v1/servers/" + serverID + "/files/download?path=" + url.QueryEscape(path)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return FileDownload{}, fmt.Errorf("create engine file download request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.http.Do(request)
	if err != nil {
		return FileDownload{}, fmt.Errorf("open engine file download: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return FileDownload{}, decodeAPIError(response.StatusCode, message)
	}
	return FileDownload{
		Body:               response.Body,
		ContentType:        response.Header.Get("Content-Type"),
		ContentDisposition: response.Header.Get("Content-Disposition"),
		ContentLength:      response.ContentLength,
	}, nil
}

func (c *Client) WriteFile(ctx context.Context, serverID, path, content string) (FileContent, error) {
	var result FileContent
	if err := c.do(ctx, http.MethodPut, "/v1/servers/"+serverID+"/files/content", map[string]string{
		"path": path, "content": content,
	}, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (c *Client) CreateDirectory(ctx context.Context, serverID, path string) error {
	return c.do(ctx, http.MethodPost, "/v1/servers/"+serverID+"/files/directories", map[string]string{
		"path": path,
	}, nil)
}

func (c *Client) DeleteFile(ctx context.Context, serverID, path string) error {
	return c.do(ctx, http.MethodDelete, "/v1/servers/"+serverID+"/files", map[string]string{
		"path": path,
	}, nil)
}

func (c *Client) RenameFile(ctx context.Context, serverID, path, newName string) (string, error) {
	var result map[string]string
	if err := c.do(ctx, http.MethodPatch, "/v1/servers/"+serverID+"/files", map[string]string{
		"path": path, "new_name": newName,
	}, &result); err != nil {
		return "", err
	}
	return result["path"], nil
}

func (c *Client) CreateBackup(
	ctx context.Context,
	serverID, backupID string,
	includePaths, excludeGlobs []string,
) (BackupResult, error) {
	var result BackupResult
	if err := c.do(ctx, http.MethodPost, "/v1/servers/"+serverID+"/backups/"+backupID, map[string]any{
		"include_paths": includePaths,
		"exclude_globs": excludeGlobs,
	}, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (c *Client) RestoreBackup(ctx context.Context, serverID, backupID, sha256 string) error {
	return c.do(ctx, http.MethodPost, "/v1/servers/"+serverID+"/backups/"+backupID+"/restore", map[string]string{
		"sha256": sha256,
	}, nil)
}

func (c *Client) DeleteBackup(ctx context.Context, serverID, backupID string) error {
	return c.do(ctx, http.MethodDelete, "/v1/servers/"+serverID+"/backups/"+backupID, nil, nil)
}

func (c *Client) OpenBackup(ctx context.Context, serverID, backupID string) (FileDownload, error) {
	endpoint := c.baseURL + "/v1/servers/" + serverID + "/backups/" + backupID + "/download"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return FileDownload{}, fmt.Errorf("create engine backup request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.http.Do(request)
	if err != nil {
		return FileDownload{}, fmt.Errorf("open engine backup: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return FileDownload{}, decodeAPIError(response.StatusCode, message)
	}
	return FileDownload{
		Body: response.Body, ContentType: response.Header.Get("Content-Type"),
		ContentDisposition: response.Header.Get("Content-Disposition"),
		ContentLength:      response.ContentLength,
	}, nil
}

func (c *Client) CreateDatabase(
	ctx context.Context,
	serverID, databaseID string,
	input DatabaseRequest,
) (DatabaseResult, error) {
	var result DatabaseResult
	if err := c.do(
		ctx,
		http.MethodPost,
		"/v1/servers/"+serverID+"/databases/"+databaseID,
		input,
		&result,
	); err != nil {
		return result, err
	}
	return result, nil
}

func (c *Client) DeleteDatabase(
	ctx context.Context,
	serverID, databaseID string,
	input DatabaseRequest,
	removeHost bool,
) error {
	return c.do(
		ctx,
		http.MethodDelete,
		fmt.Sprintf(
			"/v1/servers/%s/databases/%s?remove_host=%t",
			serverID,
			databaseID,
			removeHost,
		),
		input,
		nil,
	)
}

func (c *Client) RotateDatabasePassword(
	ctx context.Context,
	serverID, databaseID string,
	input DatabaseRequest,
) error {
	return c.do(
		ctx,
		http.MethodPost,
		"/v1/servers/"+serverID+"/databases/"+databaseID+"/password",
		input,
		nil,
	)
}

func (c *Client) do(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode engine request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("create engine request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "application/json")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("call engine: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return decodeAPIError(response.StatusCode, message)
	}
	if output != nil {
		if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(output); err != nil {
			return fmt.Errorf("decode engine response: %w", err)
		}
	}
	return nil
}

func decodeAPIError(status int, document []byte) error {
	var payload struct {
		Code   string `json:"code"`
		Error  string `json:"error"`
		Detail string `json:"detail"`
	}
	_ = json.Unmarshal(document, &payload)
	detail := strings.TrimSpace(payload.Detail)
	if detail == "" {
		detail = strings.TrimSpace(payload.Error)
	}
	if detail == "" {
		detail = strings.TrimSpace(string(document))
	}
	if len(detail) > 2000 {
		detail = detail[:2000]
	}
	code := strings.TrimSpace(payload.Code)
	if code == "" {
		code = "engine_request_failed"
	}
	return &APIError{
		Status: status, Code: code, Detail: detail,
		Retryable: status == http.StatusTooManyRequests || status >= 500,
	}
}
