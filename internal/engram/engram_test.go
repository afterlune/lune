package engram

import (
	"errors"
	"strings"
	"testing"

	core "github.com/afterlune/luneCore"
)

type fakeMCPController struct {
	servers     []core.MCPServerStatus
	enabled     []string
	enableError error
}

func (f *fakeMCPController) MCPServers() []core.MCPServerStatus {
	return f.servers
}

func (f *fakeMCPController) EnableMCPServer(name string) error {
	f.enabled = append(f.enabled, name)
	return f.enableError
}

func TestInstructionsOnlyWhenConfigured(t *testing.T) {
	if got := Instructions(nil); got != "" {
		t.Fatalf("instructions without Engram config = %q, want empty", got)
	}
	if got := Instructions(map[string]core.MCPServerConfig{ServerName: {}}); !strings.Contains(got, "durable project memory") {
		t.Fatalf("instructions with Engram config = %q, want memory protocol", got)
	}
}

func TestEnsureEnabled(t *testing.T) {
	configured := map[string]core.MCPServerConfig{ServerName: {}}
	otherServer := map[string]core.MCPServerConfig{"search": {}}

	t.Run("does not enable when Engram is absent", func(t *testing.T) {
		client := &fakeMCPController{}
		if err := EnsureEnabled(client, otherServer); err != nil {
			t.Fatalf("EnsureEnabled() error = %v", err)
		}
		if len(client.enabled) != 0 {
			t.Fatalf("enabled servers = %v, want none", client.enabled)
		}
	})

	t.Run("enables configured Engram", func(t *testing.T) {
		client := &fakeMCPController{}
		if err := EnsureEnabled(client, configured); err != nil {
			t.Fatalf("EnsureEnabled() error = %v", err)
		}
		if len(client.enabled) != 1 || client.enabled[0] != ServerName {
			t.Fatalf("enabled servers = %v, want [%q]", client.enabled, ServerName)
		}
	})

	t.Run("does not reconnect a restored connection", func(t *testing.T) {
		client := &fakeMCPController{servers: []core.MCPServerStatus{{Name: ServerName, Enabled: true, Connected: true}}}
		if err := EnsureEnabled(client, configured); err != nil {
			t.Fatalf("EnsureEnabled() error = %v", err)
		}
		if len(client.enabled) != 0 {
			t.Fatalf("enabled servers = %v, want no reconnect", client.enabled)
		}
	})

	t.Run("retries a restored disconnected server", func(t *testing.T) {
		client := &fakeMCPController{servers: []core.MCPServerStatus{{Name: ServerName, Enabled: true}}}
		if err := EnsureEnabled(client, configured); err != nil {
			t.Fatalf("EnsureEnabled() error = %v", err)
		}
		if len(client.enabled) != 1 || client.enabled[0] != ServerName {
			t.Fatalf("enabled servers = %v, want [%q]", client.enabled, ServerName)
		}
	})

	t.Run("returns connection failure", func(t *testing.T) {
		wantErr := errors.New("connection failed")
		client := &fakeMCPController{enableError: wantErr}
		if err := EnsureEnabled(client, configured); !errors.Is(err, wantErr) {
			t.Fatalf("EnsureEnabled() error = %v, want %v", err, wantErr)
		}
	})
}

func TestStatusMessage(t *testing.T) {
	tests := []struct {
		name    string
		servers []core.MCPServerStatus
		want    string
	}{
		{name: "missing", want: "automatic memory is off"},
		{name: "connected", servers: []core.MCPServerStatus{{Name: ServerName, Enabled: true, Connected: true}}, want: "Routine exchanges are not saved"},
		{name: "disconnected", servers: []core.MCPServerStatus{{Name: ServerName, Enabled: true, LastError: "not found"}}, want: "check `/mcp list`"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := StatusMessage(test.servers); !strings.Contains(got, test.want) {
				t.Fatalf("StatusMessage() = %q, want substring %q", got, test.want)
			}
		})
	}
}
