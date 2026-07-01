package http

import (
	"encoding/json"
	"net/http"

	"github.com/nextlevelbuilder/goclaw/internal/media"
	"github.com/nextlevelbuilder/goclaw/internal/permissions"
)

// availableBackends lists the backend variants exposed by the storage backend API.
// Currently only "local" (filesystem) is supported.
var availableBackends = []backendInfo{
	{Name: "local", Label: "Local filesystem"},
}

type backendInfo struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

// storageConfigResponse is the payload for GET /v1/storage/config.
type storageConfigResponse struct {
	CurrentBackend string `json:"currentBackend"`
	BasePath       string `json:"basePath"`
}

// storageBackendsResponse is the payload for GET /v1/storage/backends.
type storageBackendsResponse struct {
	Backends []backendInfo `json:"backends"`
}

// migrationStartRequest is the body for POST /v1/storage/migrate.
type migrationStartRequest struct {
	TargetBackend string `json:"targetBackend"`
}

// migrationStartResponse is the body returned by POST /v1/storage/migrate.
type migrationStartResponse struct {
	MigrationID string `json:"migrationId"`
	Status      string `json:"status"`
}

// migrationStatusProgress holds counters for GET /v1/storage/migrate/status/{id}.
type migrationStatusProgress struct {
	Total    int `json:"total"`
	Migrated int `json:"migrated"`
}

// migrationStatusResponse is the payload for GET /v1/storage/migrate/status/{id}.
type migrationStatusResponse struct {
	Status       string                  `json:"status"`
	Progress     migrationStatusProgress `json:"progress"`
	ErrorMessage string                  `json:"errorMessage,omitempty"`
}

// StorageBackendHandler provides API endpoints for querying and switching
// the media storage backend.
type StorageBackendHandler struct {
	store *media.Store
	drain media.DrainChecker
}

// NewStorageBackendHandler creates a StorageBackendHandler.
// drain may be nil for environments that do not run a scheduler.
func NewStorageBackendHandler(store *media.Store, drain media.DrainChecker) *StorageBackendHandler {
	return &StorageBackendHandler{store: store, drain: drain}
}

// RegisterRoutes wires the four storage backend endpoints onto mux.
func (h *StorageBackendHandler) RegisterRoutes(mux *http.ServeMux) {
	adminOnly := func(next http.HandlerFunc) http.HandlerFunc {
		return requireAuth(permissions.RoleAdmin, next)
	}
	mux.HandleFunc("GET /v1/storage/config", adminOnly(h.handleConfig))
	mux.HandleFunc("GET /v1/storage/backends", adminOnly(h.handleBackends))
	mux.HandleFunc("POST /v1/storage/migrate", adminOnly(h.handleMigrate))
	mux.HandleFunc("GET /v1/storage/migrate/status/{migrationId}", adminOnly(h.handleMigrateStatus))
}

// handleConfig returns the current backend name and base path.
func (h *StorageBackendHandler) handleConfig(w http.ResponseWriter, r *http.Request) {
	if !requireMasterScope(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, storageConfigResponse{
		CurrentBackend: h.store.BackendName(),
		BasePath:       h.store.BaseDir(),
	})
}

// handleBackends returns the list of available storage backends.
func (h *StorageBackendHandler) handleBackends(w http.ResponseWriter, r *http.Request) {
	if !requireMasterScope(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, storageBackendsResponse{Backends: availableBackends})
}

// handleMigrate starts an async migration to the specified backend.
// Body: {"targetBackend": "<name>"}
// The target base path is derived from the backend name (only "local" is
// supported for now, which requires an explicit targetPath in the body;
// future backends may use configured paths).
func (h *StorageBackendHandler) handleMigrate(w http.ResponseWriter, r *http.Request) {
	if !requireMasterScope(w, r) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxMigrateBodyBytes)
	var req migrationStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.TargetBackend == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "targetBackend is required"})
		return
	}

	// Validate that the requested backend is known.
	known := false
	for _, be := range availableBackends {
		if be.Name == req.TargetBackend {
			known = true
			break
		}
	}
	if !known {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown backend: " + req.TargetBackend})
		return
	}

	// For now, the target path is the same root directory with the backend name
	// appended as a sub-directory. This allows testing the full migration path
	// without external infrastructure.
	targetDir := h.store.BaseDir() + "-" + req.TargetBackend

	migrationID, err := h.store.MigrateBackend(req.TargetBackend, targetDir, h.drain)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusAccepted, migrationStartResponse{
		MigrationID: migrationID,
		Status:      "pending",
	})
}

// handleMigrateStatus returns the current status of a migration by ID.
func (h *StorageBackendHandler) handleMigrateStatus(w http.ResponseWriter, r *http.Request) {
	if !requireMasterScope(w, r) {
		return
	}

	migrationID := r.PathValue("migrationId")
	if migrationID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "migrationId is required"})
		return
	}

	state, ok := h.store.MigrationState(migrationID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "migration not found"})
		return
	}

	writeJSON(w, http.StatusOK, migrationStatusResponse{
		Status: string(state.Status),
		Progress: migrationStatusProgress{
			Total:    state.Total,
			Migrated: state.Migrated,
		},
		ErrorMessage: state.ErrorMessage,
	})
}

// maxMigrateBodyBytes is the request body size limit for migration start.
const maxMigrateBodyBytes = 4096
