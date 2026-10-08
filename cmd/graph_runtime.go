package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/afterlune/lune/internal/agentgraph"
	"github.com/afterlune/lune/internal/model"
	graph "github.com/afterlune/luneGraph"
	"github.com/afterlune/luneGraph/checkpoint"
)

const activeRunID = "lune-cli-active"

type resumableResponder interface {
	HasPending() (bool, error)
	Resume(context.Context) (*model.Response, error)
	Discard(context.Context) (bool, error)
}

type graphResponder struct {
	agent   *agentgraph.Agent
	store   graph.Store[agentgraph.State]
	history *model.ResponseInput
}

func (r *graphResponder) Respond(ctx context.Context, input *model.ResponseInput) (*model.Response, error) {
	if pending, err := r.HasPending(); err != nil {
		return nil, fmt.Errorf("inspect pending task: %w", err)
	} else if pending {
		return nil, errors.New("an interrupted task is pending; use /resume or /discard first")
	}
	result, err := r.agent.Start(ctx, activeRunID, r.history, input, agentgraph.TaskOptions{}, r.options())
	return r.finish(result, err)
}

func (r *graphResponder) Resume(ctx context.Context) (*model.Response, error) {
	result, err := r.agent.Recover(ctx, activeRunID, nil, r.options())
	return r.finish(result, err)
}

func (r *graphResponder) HasPending() (bool, error) {
	_, err := r.store.Load(context.Background(), activeRunID)
	if errors.Is(err, checkpoint.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (r *graphResponder) Discard(ctx context.Context) (bool, error) {
	err := r.store.Delete(ctx, activeRunID)
	if errors.Is(err, checkpoint.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	slog.Warn("interrupted agent task discarded", "run_id", activeRunID)
	return true, nil
}

func (r *graphResponder) options() graph.Options[agentgraph.State] {
	return graph.Options[agentgraph.State]{
		Store:    r.store,
		Observer: graph.ObserverFunc(logGraphEvent),
	}
}

func logGraphEvent(ctx context.Context, event graph.Event) {
	level := slog.LevelDebug
	if event.Operation == graph.OperationStart || event.Operation == graph.OperationResume || event.Operation == graph.OperationRecover {
		level = slog.LevelInfo
	}
	if event.Err != nil {
		level = slog.LevelError
	}
	attrs := []slog.Attr{
		slog.String("operation", string(event.Operation)),
		slog.String("phase", string(event.Phase)),
		slog.String("run_id", event.RunID),
		slog.String("node", event.Node),
		slog.String("status", string(event.Status)),
	}
	if event.CallID != "" {
		attrs = append(attrs, slog.String("call_id", event.CallID))
	}
	if event.Err != nil {
		attrs = append(attrs, slog.String("error_type", fmt.Sprintf("%T", event.Err)))
	}
	slog.LogAttrs(ctx, level, "graph execution", attrs...)
}

func (r *graphResponder) finish(result graph.Result[agentgraph.State], runErr error) (*model.Response, error) {
	if result.Status == graph.StatusCompleted {
		if result.Checkpoint.Final == nil {
			if err := r.deleteCompletedRun(); err != nil {
				runErr = errors.Join(runErr, err)
			}
			return nil, errors.Join(runErr, errors.New("agent graph completed without a final state"))
		}
		state := result.Checkpoint.Final
		if state.TaskError != "" && state.FinalResponse == nil {
			if err := r.deleteCompletedRun(); err != nil {
				runErr = errors.Join(runErr, err)
			}
			return nil, errors.Join(runErr, errors.New(state.TaskError))
		}
		r.history = model.NewResponseInputItems(state.History.Items...)
		response := state.FinalResponse
		if response == nil {
			if err := r.deleteCompletedRun(); err != nil {
				runErr = errors.Join(runErr, err)
			}
			return nil, errors.Join(runErr, errors.New("agent graph completed without a final response"))
		}
		if err := r.deleteCompletedRun(); err != nil {
			return response, errors.Join(runErr, err)
		}
		if state.TaskError != "" {
			return response, errors.Join(runErr, errors.New(state.TaskError))
		}
		return response, runErr
	}
	if result.Status == graph.StatusCompletedWithFailures {
		if err := r.deleteCompletedRun(); err != nil {
			runErr = errors.Join(runErr, err)
		}
		if runErr == nil {
			runErr = errors.New("agent graph completed with callback failures")
		}
		return nil, runErr
	}

	if result.Status == graph.StatusFailed {
		// Only remove a persisted graph failure. Store conflicts and I/O errors
		// can leave a valid checkpoint owned by another call or process.
		if result.Checkpoint.Failure != nil && !errors.Is(runErr, graph.ErrConflict) {
			if err := r.deleteCompletedRun(); err != nil {
				runErr = errors.Join(runErr, err)
			}
		}
		if runErr == nil {
			runErr = errors.New("agent graph failed")
		}
	}
	if result.Status == graph.StatusCancelled || result.Status == graph.StatusBudget || result.Status == graph.StatusWaiting {
		slog.Warn("agent task remains resumable", "run_id", activeRunID, "status", result.Status)
	}
	return nil, runErr
}

func (r *graphResponder) deleteCompletedRun() error {
	err := r.store.Delete(context.Background(), activeRunID)
	if errors.Is(err, checkpoint.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete completed task checkpoint: %w", err)
	}
	return nil
}
