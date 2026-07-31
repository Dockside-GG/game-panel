package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/dockside-gg/game-panel/internal/store"
	"github.com/go-chi/chi/v5"
)

type scheduleTaskRequest struct {
	TaskType       string          `json:"task_type"`
	Config         json.RawMessage `json:"config"`
	TimeoutSeconds int             `json:"timeout_seconds"`
}

type createScheduleRequest struct {
	Name           string                `json:"name"`
	CronExpression string                `json:"cron_expression"`
	Timezone       string                `json:"timezone"`
	Enabled        bool                  `json:"enabled"`
	Tasks          []scheduleTaskRequest `json:"tasks"`
}

type scheduleEnabledRequest struct {
	Enabled bool `json:"enabled"`
}

func (s *Server) listSchedules(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverID")
	if _, err := s.store.ServerByID(r.Context(), serverID); err != nil {
		writeProblem(w, r, err)
		return
	}
	items, err := s.store.ListSchedules(r.Context(), serverID)
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schedules": items})
}

func (s *Server) createSchedule(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !canOperate(session.User.PanelRole) {
		writeProblem(w, r, errForbidden)
		return
	}
	serverID := chi.URLParam(r, "serverID")
	var input createScheduleRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.CronExpression = strings.TrimSpace(input.CronExpression)
	input.Timezone = strings.TrimSpace(input.Timezone)
	if input.Name == "" || len(input.Name) > 120 || len(input.Tasks) == 0 || len(input.Tasks) > 20 {
		writeProblem(w, r, errors.Join(errBadRequest, errors.New("invalid schedule name or task count")))
		return
	}
	if _, err := store.ValidateSchedule(input.CronExpression, input.Timezone); err != nil {
		writeProblem(w, r, errors.Join(errBadRequest, err))
		return
	}
	tasks := make([]store.ScheduleTaskInput, 0, len(input.Tasks))
	for _, task := range input.Tasks {
		task.TaskType = strings.ToLower(strings.TrimSpace(task.TaskType))
		if err := validateScheduleTask(task); err != nil {
			writeProblem(w, r, errors.Join(errBadRequest, err))
			return
		}
		if task.TimeoutSeconds == 0 {
			task.TimeoutSeconds = 300
		}
		tasks = append(tasks, store.ScheduleTaskInput{
			TaskType: task.TaskType, Config: task.Config, TimeoutSeconds: task.TimeoutSeconds,
		})
	}
	result, err := s.store.CreateSchedule(r.Context(), store.CreateScheduleParams{
		ServerID: serverID, Name: input.Name, CronExpression: input.CronExpression,
		Timezone: input.Timezone, Enabled: input.Enabled, CreatedBy: session.User.ID, Tasks: tasks,
	})
	if err != nil {
		writeProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) setScheduleEnabled(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !canOperate(session.User.PanelRole) {
		writeProblem(w, r, errForbidden)
		return
	}
	var input scheduleEnabledRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := s.store.SetScheduleEnabled(
		r.Context(), chi.URLParam(r, "serverID"), chi.URLParam(r, "scheduleID"), input.Enabled,
	); err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) runScheduleNow(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !canOperate(session.User.PanelRole) {
		writeProblem(w, r, errForbidden)
		return
	}
	if err := s.store.RunScheduleNow(
		r.Context(), chi.URLParam(r, "serverID"), chi.URLParam(r, "scheduleID"),
	); err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !canOperate(session.User.PanelRole) {
		writeProblem(w, r, errForbidden)
		return
	}
	if err := s.store.DeleteSchedule(
		r.Context(), chi.URLParam(r, "serverID"), chi.URLParam(r, "scheduleID"),
	); err != nil {
		writeProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func validateScheduleTask(task scheduleTaskRequest) error {
	if task.TimeoutSeconds < 0 || task.TimeoutSeconds > 3600 || len(task.Config) == 0 {
		return errors.New("invalid schedule task timeout or config")
	}
	switch task.TaskType {
	case "backup":
		var config struct {
			Name          string   `json:"name"`
			IncludePaths  []string `json:"include_paths"`
			ExcludeGlobs  []string `json:"exclude_globs"`
			RetentionDays *int     `json:"retention_days"`
		}
		if json.Unmarshal(task.Config, &config) != nil || strings.TrimSpace(config.Name) == "" ||
			!validBackupRules(config.IncludePaths) || !validBackupRules(config.ExcludeGlobs) ||
			(config.RetentionDays != nil && (*config.RetentionDays < 1 || *config.RetentionDays > 3650)) {
			return errors.New("invalid scheduled backup task")
		}
	case "power":
		var config struct {
			Action string `json:"action"`
		}
		if json.Unmarshal(task.Config, &config) != nil ||
			!map[string]bool{"start": true, "stop": true, "restart": true, "kill": true}[config.Action] {
			return errors.New("invalid scheduled power task")
		}
	case "command":
		var config struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(task.Config, &config) != nil || strings.TrimSpace(config.Command) == "" ||
			len(config.Command) > 2048 || strings.ContainsAny(config.Command, "\x00\r\n") {
			return errors.New("invalid scheduled command task")
		}
	case "delay":
		var config struct {
			Seconds int `json:"seconds"`
		}
		if json.Unmarshal(task.Config, &config) != nil || config.Seconds < 1 || config.Seconds > 3600 {
			return errors.New("invalid scheduled delay task")
		}
	case "notify":
		var config struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(task.Config, &config) != nil || strings.TrimSpace(config.Message) == "" ||
			len(config.Message) > 1000 {
			return errors.New("invalid scheduled notification task")
		}
	default:
		return errors.New("unknown schedule task type")
	}
	return nil
}
