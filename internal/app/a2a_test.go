package app

import (
	"io"
	"testing"

	core "github.com/afterlune/luneCore"
)

func TestA2AListenAuthenticationBoundary(t *testing.T) {
	for _, test := range []struct {
		address, token string
		allowed        bool
	}{
		{"127.0.0.1:18091", "", true}, {"[::1]:18091", "", true}, {":18091", "", false}, {"0.0.0.0:18091", "", false}, {"localhost:18091", "", false}, {"192.0.2.1:18091", "", false}, {":18091", "token", true},
	} {
		if got := validateA2AListen(test.address, test.token) == nil; got != test.allowed {
			t.Errorf("%s allowed=%v", test.address, got)
		}
	}
}
func TestA2AAppAppliesPermissionBeforeServing(t *testing.T) {
	writeUserProjectConfigForTest(t, `{"models":{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test"}},"profiles":{"primary":{"provider":"host","model":"test-model"}},"defaultProfile":"primary"},"runtime":{}}`)
	old := runA2AServer
	t.Cleanup(func() { runA2AServer = old })
	for _, mode := range []core.PermissionMode{core.PermissionReadOnly, core.PermissionWorkspace, core.PermissionAuto, core.PermissionFullAccess, ""} {
		called := false
		runA2AServer = func(c *core.Core, _, _ string) error {
			called = true
			want := mode
			if want == "" {
				want = core.PermissionWorkspace
			}
			if c.PermissionMode() != want {
				t.Errorf("got %s want %s", c.PermissionMode(), want)
			}
			return nil
		}
		args := []string{"--a2a-listen", "127.0.0.1:18091"}
		if mode != "" {
			args = append(args, "--permission", string(mode))
		}
		if err := RunWithStreams(args, nil, io.Discard, io.Discard); err != nil {
			t.Fatal(err)
		}
		if !called {
			t.Fatal("server not reached")
		}
	}
}
