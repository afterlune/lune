package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/afterlune/luneCore/capability"
)

type capabilityTestCLICore struct {
	*fakeCLICore
	items []capability.GeneratedTool
}

func (f *capabilityTestCLICore) ListGeneratedTools(context.Context, int, bool) ([]capability.GeneratedTool, error) {
	return append([]capability.GeneratedTool(nil), f.items...), nil
}
func (f *capabilityTestCLICore) SetGeneratedToolEnabled(_ context.Context, id string, enabled bool) error {
	for i := range f.items {
		if f.items[i].ID == id {
			f.items[i].Enabled = enabled
			return nil
		}
	}
	return context.Canceled
}
func (f *capabilityTestCLICore) DeleteGeneratedTool(_ context.Context, id string) error {
	for i := range f.items {
		if f.items[i].ID == id {
			f.items = append(f.items[:i], f.items[i+1:]...)
			return nil
		}
	}
	return context.Canceled
}

func TestCLICapabilityManagement(t *testing.T) {
	client := &capabilityTestCLICore{fakeCLICore: &fakeCLICore{}, items: []capability.GeneratedTool{
		{ID: "exp-12345678", Name: "Search issue", Description: "Find relevant source references.", Purpose: capability.PurposeResearch, Dependencies: []string{"grep"}, Enabled: true},
		{ID: "exp-87654321", Name: "Write summary", Description: "Summarize gathered findings.", Purpose: capability.PurposeWriting, Dependencies: []string{"write_file"}, Enabled: false},
	}}
	var stdout, stderr bytes.Buffer
	handleCoreCLICapability(client, "/capability list search", &stdout, &stderr)
	if !strings.Contains(stdout.String(), "Search issue") || strings.Contains(stdout.String(), "Write summary") || stderr.Len() != 0 {
		t.Fatalf("filtered list stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	stdout.Reset()
	handleCoreCLICapability(client, "/capability disable exp-123", &stdout, &stderr)
	if !strings.Contains(stdout.String(), "disabled") || client.items[0].Enabled {
		t.Fatalf("disable stdout=%q enabled=%v", stdout.String(), client.items[0].Enabled)
	}
	stdout.Reset()
	handleCoreCLICapability(client, "/capability delete exp-876", &stdout, &stderr)
	if !strings.Contains(stdout.String(), "Deleted generated tool") || len(client.items) != 1 {
		t.Fatalf("delete stdout=%q remaining=%d", stdout.String(), len(client.items))
	}
}
