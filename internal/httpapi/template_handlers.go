package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/dockside-gg/game-panel/internal/templates"
	"github.com/go-chi/chi/v5"
)

type importTemplateRequest struct {
	Category string          `json:"category"`
	Document json.RawMessage `json:"document"`
}

type archiveTemplateRequest struct {
	ConfirmName string `json:"confirm_name"`
}

type forkTemplateRequest struct {
	Category string          `json:"category"`
	Document json.RawMessage `json:"document"`
}

type serverTemplateRequest struct {
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description"`
}

func (s *Server) listTemplates(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	items, total, err := s.store.ListTemplates(
		r.Context(),
		strings.TrimSpace(r.URL.Query().Get("search")),
		strings.TrimSpace(r.URL.Query().Get("category")),
		strings.TrimSpace(r.URL.Query().Get("source")),
		limit,
		offset,
	)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"templates": items,
		"total":     total,
		"limit":     limit,
		"offset":    offset,
	})
}

func (s *Server) templateDetail(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.TemplateByVersion(r.Context(), chi.URLParam(r, "versionID"))
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) templateFacets(w http.ResponseWriter, r *http.Request) {
	categories, sources, err := s.store.TemplateFacets(r.Context())
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"categories": categories,
		"sources":    sources,
	})
}

func (s *Server) importTemplate(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !canAdminister(session.User.PanelRole) {
		writeProblem(w, r, errForbidden)
		return
	}
	var input importTemplateRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Category = strings.TrimSpace(input.Category)
	if input.Category == "" || len(input.Category) > 80 ||
		len(input.Document) == 0 || len(input.Document) > 2<<20 ||
		!json.Valid(input.Document) {
		writeProblem(w, r, errors.Join(errBadRequest, errors.New("invalid template document or category")))
		return
	}
	entry, err := templates.Normalize("custom", input.Category, "", input.Document)
	if err != nil {
		writeProblem(w, r, errors.Join(errBadRequest, err))
		return
	}
	item, err := s.store.ImportCustomTemplate(
		r.Context(), session.User.ID, entry,
	)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) forkTemplate(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !canAdminister(session.User.PanelRole) {
		writeProblem(w, r, errForbidden)
		return
	}
	parent, err := s.store.TemplateByVersion(r.Context(), chi.URLParam(r, "versionID"))
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	var input forkTemplateRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Category = strings.TrimSpace(input.Category)
	if input.Category == "" {
		input.Category = parent.Category
	}
	if len(input.Document) == 0 {
		input.Document = parent.SourceDocument
	}
	if len(input.Category) > 80 || len(input.Document) > 2<<20 || !json.Valid(input.Document) {
		writeProblem(w, r, errors.Join(errBadRequest, errors.New("invalid template document or category")))
		return
	}
	entry, err := templates.Normalize("custom", input.Category, "", input.Document)
	if err != nil {
		writeProblem(w, r, errors.Join(errBadRequest, err))
		return
	}
	if parent.SourceKind == "custom" {
		entry.Slug = parent.Slug
	} else {
		entry.Slug = "custom-" + parent.Slug
	}
	item, err := s.store.ForkTemplate(
		r.Context(), session.User.ID, parent.VersionID, entry,
	)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) archiveTemplate(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !canAdminister(session.User.PanelRole) {
		writeProblem(w, r, errForbidden)
		return
	}
	var input archiveTemplateRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := s.store.ArchiveCustomTemplate(
		r.Context(), session.User.ID, chi.URLParam(r, "templateID"), input.ConfirmName,
	); err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createTemplateFromServer(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !canAdminister(session.User.PanelRole) {
		writeProblem(w, r, errForbidden)
		return
	}
	serverID := chi.URLParam(r, "serverID")
	server, err := s.store.ServerByID(r.Context(), serverID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	var input serverTemplateRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Category = strings.TrimSpace(input.Category)
	input.Description = strings.TrimSpace(input.Description)
	if input.Name == "" || len(input.Name) > 120 || input.Category == "" ||
		len(input.Category) > 80 || len(input.Description) > 2000 {
		writeProblem(w, r, errors.Join(errBadRequest, errors.New("invalid template name, category, or description")))
		return
	}
	configuration, err := s.store.ServerConfiguration(r.Context(), serverID, s.box)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	parent, err := s.store.TemplateByVersion(r.Context(), server.TemplateVersionID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	var canonical templates.CanonicalTemplate
	if err := json.Unmarshal(parent.CanonicalDocument, &canonical); err != nil {
		writeProblem(w, r, err)
		return
	}
	variables := make([]map[string]any, 0, len(configuration.Variables))
	for _, variable := range configuration.Variables {
		defaultValue := variable.DefaultValue
		if !variable.Secret && variable.Value != nil {
			defaultValue = *variable.Value
		}
		if variable.Secret {
			defaultValue = ""
		}
		variables = append(variables, map[string]any{
			"name": variable.DisplayName, "description": variable.Description,
			"env_variable": variable.Name, "default_value": defaultValue,
			"user_viewable": variable.UserViewable,
			"user_editable": variable.UserEditable,
			"rules":         variable.Rules, "field_type": variable.FieldType,
		})
	}
	networkPorts := make([]templates.NetworkPort, 0, len(configuration.Ports))
	for _, port := range configuration.Ports {
		name := strings.TrimSpace(port.Purpose)
		if name == "" {
			name = "Game port"
		}
		networkPorts = append(networkPorts, templates.NetworkPort{
			Name: name, Purpose: name,
			ContainerPort: port.ContainerPort, Protocol: port.Protocol,
			Primary: port.IsPrimary, Required: port.IsPrimary, Published: true,
			Environment: port.Environment,
		})
	}
	resourceDefaults := templates.ResourceDefaults{
		CPULimitMillicores: configuration.Resources.CPULimitMillicores,
	}
	if configuration.Resources.MemoryLimitBytes != nil {
		value := *configuration.Resources.MemoryLimitBytes / (1024 * 1024)
		resourceDefaults.MemoryLimitMB = &value
	}
	if configuration.Resources.DiskLimitBytes != nil {
		value := *configuration.Resources.DiskLimitBytes / (1024 * 1024)
		resourceDefaults.DiskAlertLimitMB = &value
	}
	document := map[string]any{
		"name": input.Name, "author": "Dockside panel owner",
		"description":   input.Description,
		"docker_images": configuration.Images,
		"startup":       configuration.EffectiveStartup,
		"config":        map[string]any{"stop": canonical.StopCommand},
		"dockside": map[string]any{
			"network_ports":     networkPorts,
			"command_transport": configuration.CommandTransport,
			"backup_defaults":   configuration.BackupDefaults,
			"resource_defaults": resourceDefaults,
		},
		"scripts": map[string]any{"installation": map[string]any{
			"script": canonical.InstallScript, "container": canonical.InstallContainer,
			"entrypoint": canonical.InstallEntrypoint,
		}},
		"variables":     variables,
		"file_denylist": canonical.FileDenylist,
		"features":      canonical.Features,
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	entry, err := templates.Normalize("custom", input.Category, "", encoded)
	if err != nil {
		writeProblem(w, r, errors.Join(errBadRequest, err))
		return
	}
	item, err := s.store.ForkTemplate(
		r.Context(), session.User.ID, parent.VersionID, entry,
	)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}
