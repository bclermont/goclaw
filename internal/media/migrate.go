package media

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

// migrationStatus represents the lifecycle state of a backend migration.
type migrationStatus string

const (
	migrationStatusPending   migrationStatus = "pending"
	migrationStatusRunning   migrationStatus = "running"
	migrationStatusCompleted migrationStatus = "completed"
	migrationStatusFailed    migrationStatus = "failed"
)

// drainTimeout is the maximum time to wait for active sessions to finish before
// starting the migration.
const drainTimeout = 30 * time.Second


// MigrationState holds the current state of a single backend migration run.
type MigrationState struct {
	MigrationID   string
	Status        migrationStatus
	SourceBackend string
	TargetBackend string
	Total         int
	Migrated      int
	Errors        int
	ErrorMessage  string
}

// DrainChecker is implemented by scheduler.Scheduler. A nil value is accepted
// for environments (e.g. desktop SQLite edition) that do not run a scheduler.
type DrainChecker interface {
	// MarkDraining signals the scheduler to stop accepting new agent runs.
	MarkDraining()
}

// noopDrainChecker is used when no scheduler is wired (e.g. desktop edition).
type noopDrainChecker struct{}

func (noopDrainChecker) MarkDraining() {}

// MigrationManager tracks in-progress and completed migrations.
// A single migration may run at a time; concurrent requests are rejected.
type MigrationManager struct {
	mu         sync.RWMutex
	migrations map[string]*MigrationState
	active     bool // true while a migration goroutine is running
}

// NewMigrationManager creates an empty migration manager.
func NewMigrationManager() *MigrationManager {
	return &MigrationManager{migrations: make(map[string]*MigrationState)}
}

// GetMigrationStatus returns a copy of the migration state for the given ID.
// Returns false if the ID is unknown.
func (m *MigrationManager) GetMigrationStatus(migrationID string) (MigrationState, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	st, ok := m.migrations[migrationID]
	if !ok {
		return MigrationState{}, false
	}
	return *st, true
}

// MigrateBackend copies all files from src to dst backend directory, then
// updates s.baseDir to dst. It returns immediately; the work runs in a
// background goroutine. Returns the migration ID that callers can poll via
// GetMigrationStatus.
//
// The scheduler (drain) is signalled to stop accepting new runs, and the
// goroutine waits up to drainTimeout for active sessions to finish before
// starting the copy.
// MigrateBackend copies all files from src to dst backend directory, then
// updates s.baseDir to dst. It returns immediately; the work runs in a
// background goroutine. Returns the migration ID that callers can poll via
// GetMigrationStatus.
func (s *Store) MigrateBackend(
	targetBackendName string,
	targetBaseDir string,
	drain DrainChecker,
) (migrationID string, err error) {
	s.mu.Lock()
	if s.migration != nil && s.migration.active {
		s.mu.Unlock()
		return "", fmt.Errorf("media: migration already in progress")
	}
	if s.baseDir == targetBaseDir {
		s.mu.Unlock()
		return "", fmt.Errorf("media: target backend path is the same as current")
	}
	migrationID = uuid.New().String()
	state := &MigrationState{
		MigrationID:   migrationID,
		Status:        migrationStatusPending,
		SourceBackend: s.backendName,
		TargetBackend: targetBackendName,
	}
	if s.migration == nil {
		s.migration = NewMigrationManager()
	}
	s.migration.mu.Lock()
	s.migration.migrations[migrationID] = state
	s.migration.active = true
	s.migration.mu.Unlock()
	srcDir := s.baseDir
	s.mu.Unlock()

	if drain == nil {
		drain = noopDrainChecker{}
	}

	go func() {
		runMigration(s, state, srcDir, targetBaseDir, targetBackendName, drain)
	}()

	return migrationID, nil
}

// runMigration is the migration goroutine body.
func runMigration(
	s *Store,
	state *MigrationState,
	srcDir, dstDir, targetBackendName string,
	drain DrainChecker,
) {
	defer func() {
		s.migration.mu.Lock()
		s.migration.active = false
		s.migration.mu.Unlock()
	}()

	setState := func(fn func(*MigrationState)) {
		s.migration.mu.Lock()
		fn(state)
		s.migration.mu.Unlock()
	}

	setState(func(st *MigrationState) { st.Status = migrationStatusRunning })

	// Signal scheduler to reject new runs. We then wait for the drain timeout
	// to let any in-flight agent runs complete. The scheduler does not expose an
	// "all agents idle" predicate without an explicit agent key, so we rely on
	// MarkDraining() + a fixed wait period.
	drain.MarkDraining()
	time.Sleep(drainTimeout)

	// Ensure destination exists.
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		setState(func(st *MigrationState) {
			st.Status = migrationStatusFailed
			st.ErrorMessage = fmt.Sprintf("create target dir: %v", err)
		})
		slog.Error("media.migrate.mkdir_failed", "dst", dstDir, "error", err)
		return
	}

	// Count files first so progress reporting is accurate.
	var total int
	if err := filepath.WalkDir(srcDir, func(_ string, d fs.DirEntry, _ error) error {
		if !d.IsDir() {
			total++
		}
		return nil
	}); err != nil {
		slog.Warn("media.migrate.count_walk_failed", "src", srcDir, "error", err)
	}
	setState(func(st *MigrationState) { st.Total = total })

	// Copy every file, preserving relative sub-directory structure.
	var migrated, errors int
	if err := filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			errors++
			slog.Warn("media.migrate.walk_error", "path", path, "error", walkErr)
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			errors++
			return nil
		}
		dst := filepath.Join(dstDir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			errors++
			slog.Warn("media.migrate.mkdir_rel_failed", "dir", filepath.Dir(dst), "error", err)
			return nil
		}
		if err := copyFile(path, dst); err != nil {
			errors++
			slog.Warn("media.migrate.copy_failed", "src", path, "dst", dst, "error", err)
			return nil
		}
		migrated++
		setState(func(st *MigrationState) {
			st.Migrated = migrated
			st.Errors = errors
		})
		return nil
	}); err != nil {
		setState(func(st *MigrationState) {
			st.Status = migrationStatusFailed
			st.ErrorMessage = fmt.Sprintf("walk: %v", err)
		})
		return
	}

	// Update the store to point at the new backend.
	s.mu.Lock()
	s.baseDir = dstDir
	s.backendName = targetBackendName
	s.mu.Unlock()

	setState(func(st *MigrationState) {
		st.Migrated = migrated
		st.Errors = errors
		st.Status = migrationStatusCompleted
	})
	slog.Info("media.migrate.completed",
		"src", srcDir, "dst", dstDir,
		"migrated", migrated, "errors", errors)
}

