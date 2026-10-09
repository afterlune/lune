package tui

import (
	"context"

	"github.com/afterlune/lune/internal/model"
)

type fakeResponder struct {
	inputs    []*model.ResponseInput
	firstErr  error
	responses int
}

type blockingResponder struct {
	started chan struct{}
}

type fakeResumableResponder struct {
	fakeResponder
	pending   bool
	resumes   int
	discarded int
}

func (f *blockingResponder) Respond(ctx context.Context, _ *model.ResponseInput) (*model.Response, error) {
	close(f.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func (f *fakeResponder) Respond(_ context.Context, input *model.ResponseInput) (*model.Response, error) {
	f.inputs = append(f.inputs, input)
	f.responses++
	if f.responses == 1 && f.firstErr != nil {
		return nil, f.firstErr
	}
	return &model.Response{Output: []model.ResponseOutputItem{
		model.OutputMessage{
			Role:    model.RoleAssistant,
			Content: []model.OutputContentPart{model.OutputText{Text: "reply"}},
		},
	}}, nil
}

func (f *fakeResumableResponder) HasPending() (bool, error) { return f.pending, nil }

func (f *fakeResumableResponder) Resume(context.Context) (*model.Response, error) {
	f.resumes++
	f.pending = false
	return &model.Response{Output: []model.ResponseOutputItem{
		model.OutputMessage{Role: model.RoleAssistant, Content: []model.OutputContentPart{model.OutputText{Text: "resumed"}}},
	}}, nil
}

func (f *fakeResumableResponder) Discard(context.Context) (bool, error) {
	if !f.pending {
		return false, nil
	}
	f.pending = false
	f.discarded++
	return true, nil
}
