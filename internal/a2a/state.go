package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type taskRecord struct {
	ID, ContextID, SessionID, ExecutionID, MessageID, InputHash string
	State, StatusText, OutputText                               string
	StatusData                                                  map[string]any
	FinishedAt                                                  time.Time
}

// OpenServer restores the A2A task index. Core remains the source of live
// execution and pending requests.
func OpenServer(ctx context.Context, backend Backend, token, dir string) (*Server, error) {
	s := NewServer(backend, token)
	s.stateDir = dir
	if err := s.restoreTasks(ctx); err != nil {
		s.cancel()
		s.stopAdmission()
		for _, task := range s.tasks {
			if task.runtime != nil {
				cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				_ = task.runtime.Close(cleanup)
				cancel()
			}
		}
		return nil, err
	}
	for _, task := range s.tasks {
		if task.runtime != nil {
			s.wg.Add(1)
			go s.observeTask(task)
		}
	}
	return s, nil
}

func (s *Server) restoreTasks(ctx context.Context) error {
	if err := os.MkdirAll(s.stateDir, 0700); err != nil {
		return err
	}
	entries, err := os.ReadDir(s.stateDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		encoded, err := os.ReadFile(filepath.Join(s.stateDir, entry.Name()))
		if err != nil {
			return err
		}
		var row taskRecord
		if err := json.Unmarshal(encoded, &row); err != nil {
			return fmt.Errorf("read A2A state %s: %w", entry.Name(), err)
		}
		if row.ID == "" || row.ID+".json" != entry.Name() || row.SessionID == "" {
			return fmt.Errorf("invalid A2A state %s", entry.Name())
		}
		task := &managedTask{
			id: row.ID, contextID: row.ContextID, sessionID: row.SessionID,
			executionID: row.ExecutionID, messageID: row.MessageID, inputHash: row.InputHash,
			state: row.State, statusText: row.StatusText, outputText: row.OutputText,
			statusData: row.StatusData, finishedAt: row.FinishedAt, changed: make(chan struct{}),
		}
		s.tasks[task.id] = task
		if terminal(task.state) {
			continue
		}
		if task.executionID == "" {
			task.state = "TASK_STATE_FAILED"
			task.statusText = "Core submission was not confirmed before restart; inspect the session before retrying"
			task.finishedAt = s.now()
			if err := s.saveTask(task); err != nil {
				return err
			}
			continue
		}
		rt, err := s.backend.OpenSessionRuntime(ctx, task.sessionID)
		if err != nil {
			return fmt.Errorf("reopen A2A task %s: %w", task.id, err)
		}
		rt.SetUserInteractionAvailable(true)
		task.runtime = rt
	}
	s.pruneLocked()
	return nil
}

// saveTask is called before registering a task or while holding its mutex.
func (s *Server) saveTask(t *managedTask) error {
	if s.stateDir == "" {
		return nil
	}
	row := taskRecord{
		ID: t.id, ContextID: t.contextID, SessionID: t.sessionID,
		ExecutionID: t.executionID, MessageID: t.messageID, InputHash: t.inputHash,
		State: t.state, StatusText: t.statusText, OutputText: t.outputText,
		StatusData: t.statusData, FinishedAt: t.finishedAt,
	}
	encoded, err := json.Marshal(row)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(s.stateDir, ".task-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(encoded)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), filepath.Join(s.stateDir, t.id+".json"))
}

func (s *Server) removeTaskLocked(id string) {
	if s.stateDir != "" {
		if err := os.Remove(filepath.Join(s.stateDir, id+".json")); err != nil && !os.IsNotExist(err) {
			slog.Warn("remove expired A2A task", "task_id", id, "error", err)
			return
		}
	}
	delete(s.tasks, id)
}
