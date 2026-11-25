package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/afterlune/lune/internal/ptyterm"
)

type outboundMsg struct {
	Type    string `json:"type"`
	Data    string `json:"data,omitempty"`
	Code    *int   `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type inboundMsg struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
}

func main() {
	cwd := flag.String("cwd", "", "Initial working directory")
	shell := flag.String("shell", "", "Shell binary override")
	cols := flag.Uint("cols", 80, "Initial columns")
	rows := flag.Uint("rows", 24, "Initial rows")
	flag.Parse()

	var writeMu sync.Mutex
	send := func(msg outboundMsg) {
		raw, err := json.Marshal(msg)
		if err != nil {
			return
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		_, _ = os.Stdout.Write(append(raw, '\n'))
	}

	sess, err := ptyterm.Open(ptyterm.Options{
		Dir:   *cwd,
		Shell: *shell,
		Cols:  uint16(*cols),
		Rows:  uint16(*rows),
	})
	if err != nil {
		send(outboundMsg{Type: "error", Message: err.Error()})
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer sess.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, rerr := sess.Read(buf)
			if n > 0 {
				send(outboundMsg{
					Type: "data",
					Data: base64.StdEncoding.EncodeToString(buf[:n]),
				})
			}
			if rerr != nil {
				code, _ := sess.Wait()
				send(outboundMsg{Type: "exit", Code: &code})
				return
			}
		}
	}()

	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			var msg inboundMsg
			if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
				continue
			}
			switch msg.Type {
			case "data":
				if msg.Data != "" {
					raw, derr := base64.StdEncoding.DecodeString(msg.Data)
					if derr == nil && len(raw) > 0 {
						if _, werr := sess.Write(raw); werr != nil {
							_ = sess.Close()
							return
						}
					}
				}
			case "resize":
				_ = sess.Resize(msg.Cols, msg.Rows)
			case "close":
				_ = sess.Close()
				return
			}
		}
		// Parent closed stdin pipe: close PTY so no orphan shell survives.
		_ = sess.Close()
		if err := scanner.Err(); err != nil && err != io.EOF {
			send(outboundMsg{Type: "error", Message: err.Error()})
		}
	}()

	<-done
}
