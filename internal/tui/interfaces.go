package tui

import (
	"context"

	"github.com/afterlune/lune/internal/model"
)

// Responder is the model boundary needed by the TUI.
type Responder interface {
	Respond(context.Context, *model.ResponseInput) (*model.Response, error)
}

// ResumableResponder adds interrupted-task controls to a responder.
type ResumableResponder interface {
	Responder
	HasPending() (bool, error)
	Resume(context.Context) (*model.Response, error)
	Discard(context.Context) (bool, error)
}

// legacyRunID is the checkpoint name used by releases predating session IDs.
const legacyRunID = "lune-cli-active"
