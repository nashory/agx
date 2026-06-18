package runtime

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/nashory/agx/internal/db"
)

func (s *Service) handleListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.store.ListProjects()
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]Project, 0, len(projects))
	for _, project := range projects {
		out = append(out, s.projectDTO(project))
	}
	writeJSON(w, out)
}

func (s *Service) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var req createProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorStatus(w, http.StatusBadRequest, fmt.Errorf("decode project request: %w", err))
		return
	}
	if strings.TrimSpace(req.Path) == "" {
		writeErrorStatus(w, http.StatusBadRequest, fmt.Errorf("project path is required"))
		return
	}
	project, err := s.store.EnsureProjectDetails(req.Path, req.Name, req.Description, req.DefaultAgent)
	if err != nil {
		writeError(w, err)
		return
	}
	dto := s.projectDTO(project)
	s.bus.Publish("project.changed", dto)
	logRuntimeOperation("project_create",
		"project", shortDiagnosticID(project.ID),
		"path", project.Path,
		"default_agent", valueOrEmptyString(project.DefaultAgent),
	)
	writeJSON(w, dto)
}

func (s *Service) handleGetProject(w http.ResponseWriter, r *http.Request) {
	project, err := s.store.GetProject(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, s.projectDTO(project))
}

func (s *Service) handlePatchProject(w http.ResponseWriter, r *http.Request) {
	project, err := s.store.GetProject(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	var req patchProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorStatus(w, http.StatusBadRequest, fmt.Errorf("decode project patch request: %w", err))
		return
	}
	if req.Name != nil || req.Description != nil {
		name := project.Name
		if req.Name != nil {
			name = *req.Name
		}
		description := project.Description
		if req.Description != nil {
			description = req.Description
		}
		if err := s.store.UpdateProjectDetails(project.ID, name, description); err != nil {
			writeError(w, err)
			return
		}
	}
	if req.DefaultAgent != nil {
		if err := s.store.UpdateProjectDefaultAgent(project.ID, req.DefaultAgent); err != nil {
			writeError(w, err)
			return
		}
	}
	refreshed, err := s.store.GetProject(project.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	dto := s.projectDTO(refreshed)
	s.bus.Publish("project.changed", dto)
	logRuntimeOperation("project_update",
		"project", shortDiagnosticID(refreshed.ID),
		"default_agent", valueOrEmptyString(refreshed.DefaultAgent),
		"name_updated", req.Name != nil,
		"description_updated", req.Description != nil,
	)
	writeJSON(w, dto)
}

func (s *Service) handleGrantProjectAccess(w http.ResponseWriter, r *http.Request) {
	project, err := s.store.GetProject(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if err := validateOrRepairProjectAccess(project.Path); err != nil {
		writeError(w, err)
		return
	}
	if err := s.store.MarkProjectAccessGranted(project.Path); err != nil {
		writeError(w, err)
		return
	}
	dto := s.projectDTO(project)
	s.bus.Publish("project.changed", dto)
	logRuntimeOperation("project_grant_access",
		"project", shortDiagnosticID(project.ID),
		"path", project.Path,
	)
	writeJSON(w, dto)
}

func (s *Service) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	project, err := s.store.GetProject(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	tasks, err := s.store.ListTasks(project.ID, nil)
	if err != nil {
		writeError(w, err)
		return
	}
	for _, task := range tasks {
		if err := s.stopStructuredTaskForDelete(r.Context(), task); err != nil {
			writeError(w, err)
			return
		}
		if err := s.removeTaskAttachmentFiles(task.ID); err != nil {
			writeError(w, err)
			return
		}
		s.deleteDiscordChannelForTaskAsync(task, "")
	}
	if err := s.managerForProject(project).StopProject(project); err != nil {
		writeError(w, err)
		return
	}
	if err := s.store.DeleteProject(project.ID); err != nil {
		writeError(w, err)
		return
	}
	s.bus.Publish("project.deleted", map[string]string{"id": project.ID})
	logRuntimeOperation("project_delete",
		"project", shortDiagnosticID(project.ID),
		"path", project.Path,
		"tasks", len(tasks),
	)
	writeJSON(w, map[string]bool{"deleted": true})
}

func valueOrEmptyString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (s *Service) projectDTO(project db.Project) Project {
	dto := Project{
		ID:           project.ID,
		Name:         project.Name,
		Path:         project.Path,
		Description:  project.Description,
		DefaultAgent: project.DefaultAgent,
		LastOpened:   project.LastOpened,
		CreatedAt:    project.CreatedAt,
	}
	granted, err := s.store.HasProjectAccessGrant(project.Path)
	if err != nil {
		message := err.Error()
		dto.AccessError = &message
		return dto
	}
	if !granted {
		message := "Grant access before creating tasks so AGX can create Git worktrees."
		dto.AccessError = &message
		return dto
	}
	dto.AccessGranted = true
	return dto
}
