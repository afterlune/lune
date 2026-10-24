package cli

import (
	"bufio"
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"testing"

	core "github.com/afterlune/luneCore"
	publicmodel "github.com/afterlune/luneCore/model"
)

type goalCLIProvider struct{ calls atomic.Int32 }

func (*goalCLIProvider) Name() string { return "host" }
func (p *goalCLIProvider) CreateResponse(context.Context, publicmodel.ModelRequest, publicmodel.ModelProfile) (*publicmodel.ModelResponse, error) {
	call := p.calls.Add(1)
	if call == 2 {
		return &publicmodel.ModelResponse{Status: "completed", Output: []publicmodel.ResponseOutputItem{publicmodel.OutputFunctionCall{CallID: "done", Name: "update_goal", Arguments: `{"status":"complete","reason":"Everything verified"}`}}}, nil
	}
	text := "First round progress"
	if call > 2 {
		text = "Final goal answer"
	}
	return &publicmodel.ModelResponse{Status: "completed", Output: []publicmodel.ResponseOutputItem{publicmodel.OutputMessage{Role: publicmodel.RoleAssistant, Content: []publicmodel.OutputContentPart{publicmodel.OutputText{Text: text}}}}}, nil
}
func (p *goalCLIProvider) StreamResponse(ctx context.Context, request publicmodel.ModelRequest, profile publicmodel.ModelProfile, _ func(publicmodel.ModelStreamEvent) error) (*publicmodel.ModelResponse, error) {
	return p.CreateResponse(ctx, request, profile)
}

func TestCLIGoalCreatesDraftAndPrintsAllRounds(t *testing.T) {
	p := &goalCLIProvider{}
	client, err := core.New(core.Config{Workspace: t.TempDir(), Memory: core.MemoryConfig{Disabled: true}, ModelConfig: &publicmodel.Config{
		Providers:      map[string]publicmodel.ProviderConfig{"host": {Protocol: publicmodel.ProtocolResponses}},
		Profiles:       map[string]publicmodel.ModelProfile{"main": {Provider: "host", Model: "test"}},
		DefaultProfile: "main",
	}, ModelProviders: map[string]publicmodel.Provider{"host": p}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	var stdout, stderr bytes.Buffer
	input := "/goal\n/goal Finish all requirements\n/goal\n/status\n/goal clear\n/exit\n"
	if err := runCoreChatLoop(coreCLIApplication{client}, bufio.NewReader(strings.NewReader(input)), &stdout, &stderr, nil); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"No goal is set", "First round progress", "Final goal answer", "Goal: complete", "Goal cleared"} {
		if !strings.Contains(stdout.String(), text) {
			t.Fatalf("missing %q in %s; stderr=%s", text, stdout.String(), stderr.String())
		}
	}
	if p.calls.Load() != 3 {
		t.Fatalf("calls=%d, want 3", p.calls.Load())
	}
	sessions, err := client.ListSessions(t.Context(), 10, false)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions=%v err=%v", sessions, err)
	}
	if strings.Contains(string(sessions[0].History), "Continue making concrete progress") {
		t.Fatal("internal continuation leaked into user transcript")
	}
}

func TestCLIGoalPauseAndContinueAdvice(t *testing.T) {
	p := &goalCLIProvider{}
	client, err := core.New(core.Config{Workspace: t.TempDir(), Memory: core.MemoryConfig{Disabled: true}, ModelConfig: &publicmodel.Config{
		Providers:      map[string]publicmodel.ProviderConfig{"host": {Protocol: publicmodel.ProtocolResponses}},
		Profiles:       map[string]publicmodel.ModelProfile{"main": {Provider: "host", Model: "test"}},
		DefaultProfile: "main",
	}, ModelProviders: map[string]publicmodel.Provider{"host": p}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	var stdout, stderr bytes.Buffer
	input := "/goal Initial task\n/goal pause\n/continue\n/goal resume\n/exit\n"
	if err := runCoreChatLoop(coreCLIApplication{client}, bufio.NewReader(strings.NewReader(input)), &stdout, &stderr, nil); err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	if !strings.Contains(output, "Use /goal resume to continue this goal and its checkpoint.") {
		t.Fatalf("expected /continue to guide to /goal resume; got stdout=%s", output)
	}
}
