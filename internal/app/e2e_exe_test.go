//go:build windows

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/charmbracelet/x/ansi"
	"golang.org/x/sys/windows"
)

func buildLuneExeForTest(t *testing.T) string {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	exePath := filepath.Join(repoRoot, "bin", "lune.exe")
	if err := os.MkdirAll(filepath.Dir(exePath), 0755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	cmd := exec.Command("go", "build", "-o", exePath, "./cmd")
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build bin/lune.exe failed: %v\n%s", err, string(out))
	}
	return exePath
}

func startMockResponsesServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody map[string]any
		_ = json.NewDecoder(r.Body).Decode(&reqBody)
		isStream, _ := reqBody["stream"].(bool)

		replyText := "Verified `internal/tui/view.go:100` and `__init__.py` — rendering OK."
		if !isStream {
			w.Header().Set("Content-Type", "application/json")
			resp := map[string]any{
				"id":     "resp-sync-1",
				"status": "completed",
				"output": []any{
					map[string]any{
						"type": "message",
						"role": "assistant",
						"content": []any{
							map[string]any{
								"type": "output_text",
								"text": replyText,
							},
						},
					},
				},
				"usage": map[string]any{
					"input_tokens":  64,
					"output_tokens": 24,
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, _ := w.(http.Flusher)

		sendEvent := func(payload map[string]any) {
			b, _ := json.Marshal(payload)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", string(b))
			if flusher != nil {
				flusher.Flush()
			}
		}

		sendEvent(map[string]any{
			"type":  "response.reasoning_text.delta",
			"delta": "Inspecting TUI rendering pipeline... ",
		})
		sendEvent(map[string]any{
			"type":  "response.reasoning_text.delta",
			"delta": "Checking inline code and card surfaces.",
		})
		sendEvent(map[string]any{
			"type":  "response.output_text.delta",
			"delta": "Verified `internal/tui/view.go:100` ",
		})
		sendEvent(map[string]any{
			"type":  "response.output_text.delta",
			"delta": "and `__init__.py` — rendering OK.",
		})
		sendEvent(map[string]any{
			"type": "response.completed",
			"response": map[string]any{
				"id":     "resp-stream-1",
				"status": "completed",
				"output": []any{
					map[string]any{
						"type": "message",
						"role": "assistant",
						"content": []any{
							map[string]any{
								"type": "output_text",
								"text": replyText,
							},
						},
					},
				},
				"usage": map[string]any{
					"input_tokens":  128,
					"output_tokens": 32,
				},
			},
		})
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
}

func createMockWorkspaceWithServer(t *testing.T, serverURL string) string {
	t.Helper()
	tmpDir := t.TempDir()
	cfg := fmt.Sprintf(`{
  "providers": {
    "mock": {
      "protocol": "responses",
      "baseURL": %q,
      "apiKey": "test-api-key"
    }
  },
  "models": {
    "mock-fast": {
      "provider": "mock",
      "model": "gpt-test"
    }
  },
  "defaultModel": "mock-fast"
}`, serverURL)
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(cfg), 0600); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
	return tmpDir
}

func TestLuneExeOneShotStream(t *testing.T) {
	exePath := buildLuneExeForTest(t)
	srv := startMockResponsesServer(t)
	defer srv.Close()
	ws := createMockWorkspaceWithServer(t, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, exePath, "-C", ws, "Run one-shot verification")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("lune.exe one-shot failed: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}

	out := stdout.String()
	if !strings.Contains(out, "internal/tui/view.go:100") || !strings.Contains(out, "rendering OK") {
		t.Errorf("unexpected one-shot stdout: %q", out)
	}
}

func TestLuneExeCLIInteractive(t *testing.T) {
	exePath := buildLuneExeForTest(t)
	srv := startMockResponsesServer(t)
	defer srv.Close()
	ws := createMockWorkspaceWithServer(t, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, exePath, "--cli", "-C", ws)
	cmd.Stdin = strings.NewReader("/status\n/permissions ask\n/plan\n/plan\nHello from CLI\n/exit\n")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("lune.exe --cli failed: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}

	out := stdout.String()
	for _, expected := range []string{
		"gpt-test",
		"Permission mode set to ask.",
		"Switched to Plan mode",
		"Exited Plan mode",
		"internal/tui/view.go:100",
	} {
		if !strings.Contains(out, expected) {
			t.Errorf("expected %q in CLI stdout, got:\n%s", expected, out)
		}
	}
}

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func waitForOutputSubstring(t *testing.T, out *safeBuffer, substr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		raw := out.String()
		stripped := ansi.Strip(raw)
		if strings.Contains(raw, substr) || strings.Contains(stripped, substr) {
			return
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q in TUI output.\nStripped output:\n%s", substr, ansi.Strip(out.String()))
}

func TestLuneExeTUIWithConPTY(t *testing.T) {
	exePath := buildLuneExeForTest(t)
	srv := startMockResponsesServer(t)
	defer srv.Close()
	ws := createMockWorkspaceWithServer(t, srv.URL)

	// Create pipes for Windows PseudoConsole (ConPTY)
	var ptyInRead, ptyInWrite windows.Handle
	if err := windows.CreatePipe(&ptyInRead, &ptyInWrite, nil, 0); err != nil {
		t.Fatalf("CreatePipe in: %v", err)
	}
	defer windows.CloseHandle(ptyInRead)

	var ptyOutRead, ptyOutWrite windows.Handle
	if err := windows.CreatePipe(&ptyOutRead, &ptyOutWrite, nil, 0); err != nil {
		windows.CloseHandle(ptyInWrite)
		t.Fatalf("CreatePipe out: %v", err)
	}
	defer windows.CloseHandle(ptyOutWrite)

	var hpc windows.Handle
	size := windows.Coord{X: 120, Y: 36}
	if err := windows.CreatePseudoConsole(size, ptyInRead, ptyOutWrite, 0, &hpc); err != nil {
		windows.CloseHandle(ptyInWrite)
		windows.CloseHandle(ptyOutRead)
		t.Fatalf("CreatePseudoConsole: %v", err)
	}
	defer windows.ClosePseudoConsole(hpc)

	inFile := os.NewFile(uintptr(ptyInWrite), "pty-in")
	defer inFile.Close()
	outFile := os.NewFile(uintptr(ptyOutRead), "pty-out")
	defer outFile.Close()

	var output safeBuffer
	doneRead := make(chan struct{})
	go func() {
		defer close(doneRead)
		_, _ = io.Copy(&output, outFile)
	}()

	attrList, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		t.Fatalf("NewProcThreadAttributeList: %v", err)
	}
	defer attrList.Delete()

	if err := attrList.Update(
		windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE,
		unsafe.Pointer(hpc),
		unsafe.Sizeof(hpc),
	); err != nil {
		t.Fatalf("attrList.Update PSEUDOCONSOLE: %v", err)
	}

	var siEx windows.StartupInfoEx
	siEx.Cb = uint32(unsafe.Sizeof(siEx))
	siEx.StartupInfo.Flags = windows.STARTF_USESTDHANDLES
	siEx.ProcThreadAttributeList = attrList.List()

	cmdLineStr := fmt.Sprintf(`"%s" --tui -C "%s"`, exePath, ws)
	cmdLine, err := windows.UTF16PtrFromString(cmdLineStr)
	if err != nil {
		t.Fatalf("UTF16PtrFromString: %v", err)
	}
	cwdPtr, err := windows.UTF16PtrFromString(ws)
	if err != nil {
		t.Fatalf("UTF16PtrFromString cwd: %v", err)
	}

	var pi windows.ProcessInformation
	err = windows.CreateProcess(
		nil,
		cmdLine,
		nil,
		nil,
		false,
		windows.EXTENDED_STARTUPINFO_PRESENT,
		nil,
		cwdPtr,
		&siEx.StartupInfo,
		&pi,
	)
	if err != nil {
		t.Fatalf("CreateProcess in ConPTY failed: %v", err)
	}
	defer windows.CloseHandle(pi.Thread)
	defer windows.CloseHandle(pi.Process)

	// 1. Wait for initial TUI render in the 120x36 ConPTY
	waitForOutputSubstring(t, &output, "LuneAgent", 8*time.Second)
	waitForOutputSubstring(t, &output, "Ask Lune to do anything", 5*time.Second)

	// 2. Open '?' keyboard shortcuts modal and close with Esc
	_, _ = inFile.WriteString("?")
	waitForOutputSubstring(t, &output, "Keyboard Shortcuts", 5*time.Second)
	_, _ = inFile.WriteString("\x1b")
	time.Sleep(150 * time.Millisecond)

	// 3. Run /status command in TUI (waits for the status table "Permissions:" row)
	_, _ = inFile.WriteString("/status\r")
	waitForOutputSubstring(t, &output, "Permissions:", 5*time.Second)

	// 4. Submit a real turn to the mock Responses SSE server and verify streamed markdown & inline code
	_, _ = inFile.WriteString("Check TUI rendering\r")
	waitForOutputSubstring(t, &output, "internal/tui/view.go:100", 8*time.Second)
	waitForOutputSubstring(t, &output, "__init__.py", 5*time.Second)
	waitForOutputSubstring(t, &output, "rendering OK", 5*time.Second)

	// Verify no UTF-8 replacement character corruption occurred anywhere in the ConPTY stream
	if strings.ContainsRune(ansi.Strip(output.String()), '\ufffd') {
		t.Errorf("detected UTF-8 replacement character (\\ufffd) in ConPTY TUI output")
	}

	// 5. Send Ctrl+C to exit TUI cleanly. If the mock turn is still
	// finishing, the first Ctrl+C interrupts it instead of quitting; retry so
	// the test is not sensitive to that race.
	var waitRes uint32
	for attempt := 0; attempt < 3; attempt++ {
		_, _ = inFile.WriteString("\x03")
		waitRes, err = windows.WaitForSingleObject(pi.Process, 2500)
		if err == nil && waitRes == windows.WAIT_OBJECT_0 {
			break
		}
		if err != nil {
			break
		}
	}
	if err != nil || waitRes != windows.WAIT_OBJECT_0 {
		_ = windows.TerminateProcess(pi.Process, 1)
		t.Fatalf("lune.exe did not exit cleanly after Ctrl+C retries (waitRes=%v, err=%v)", waitRes, err)
	}

	var exitCode uint32
	if err := windows.GetExitCodeProcess(pi.Process, &exitCode); err != nil {
		t.Fatalf("GetExitCodeProcess: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("expected lune.exe exit code 0, got %d", exitCode)
	}
}
