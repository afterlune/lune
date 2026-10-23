package settings

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/afterlune/luneCore/capability"
)

func TestTaskCapabilityStoreLifecycle(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	inputSchema := json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}`)
	workflow := capability.GeneratedTool{
		Name: "Search project", Description: "Search the workspace for a query.", Purpose: capability.PurposeResearch,
		Tags: []string{"search"}, InputSchema: inputSchema, Script: `function run(input, tools) { return tools.call("grep", {pattern: input.query}); }`,
		Dependencies: []string{"grep"}, SourceSessionID: "session-1", SourceExecutionID: "execution-1",
	}
	saved, created, err := store.Save(ctx, workflow)
	if err != nil || !created {
		t.Fatalf("Save() = %#v, %v, created=%v", saved, err, created)
	}
	duplicate, created, err := store.Save(ctx, capability.GeneratedTool{
		Name: workflow.Name, Description: "replacement", Purpose: workflow.Purpose, InputSchema: inputSchema,
		Script: "replacement", Dependencies: []string{"grep"},
	})
	if err != nil || created || duplicate.ID != saved.ID || duplicate.Description != workflow.Description {
		t.Fatalf("duplicate Save() = %#v, %v, created=%v", duplicate, err, created)
	}

	found, err := store.List(ctx, 10, false)
	if err != nil || len(found) != 1 || found[0].ID != saved.ID {
		t.Fatalf("List(enabled) = %#v, %v", found, err)
	}
	if err := store.MarkUsed(ctx, saved.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.SetEnabled(ctx, saved.ID, false); err != nil {
		t.Fatal(err)
	}
	found, err = store.List(ctx, 10, false)
	if err != nil || len(found) != 0 {
		t.Fatalf("List() returned disabled generated tool: %#v, %v", found, err)
	}
	all, err := store.List(ctx, 20, true)
	if err != nil || len(all) != 1 || all[0].Enabled || all[0].UseCount != 1 || all[0].LastUsedAt.IsZero() {
		t.Fatalf("List(includeDisabled=true) = %#v, %v", all, err)
	}
	if err := store.Delete(ctx, saved.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, saved.ID); err == nil {
		t.Fatal("Get() succeeded after deletion")
	}
}
