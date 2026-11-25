//go:build windows

package ptyterm

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsSession struct {
	hpc     windows.Handle
	inFile  *os.File
	outFile *os.File
	proc    windows.Handle
	thread  windows.Handle
	mu      sync.Mutex
	closed  bool

	waitOnce sync.Once
	waitCode int
	waitErr  error

	closeOnce sync.Once
	closeErr  error
}

func defaultShell(explicit string) (string, []string) {
	if explicit != "" {
		if p, err := exec.LookPath(explicit); err == nil {
			return p, nil
		}
		return explicit, nil
	}
	for _, candidate := range []string{"pwsh.exe", "powershell.exe", os.Getenv("COMSPEC"), "cmd.exe"} {
		if candidate == "" {
			continue
		}
		if p, err := exec.LookPath(candidate); err == nil {
			return p, nil
		}
	}
	return "cmd.exe", nil
}

func buildUTF16EnvBlock(env []string) (*uint16, error) {
	joined := strings.Join(env, "\x00") + "\x00\x00"
	slice := windows.StringToUTF16(joined)
	if len(slice) == 0 {
		return nil, nil
	}
	return &slice[0], nil
}

func openPTY(dir, explicitShell string, cols, rows uint16) (Session, error) {
	var inRead, inWrite windows.Handle
	if err := windows.CreatePipe(&inRead, &inWrite, nil, 0); err != nil {
		return nil, fmt.Errorf("create input pipe: %w", err)
	}
	var outRead, outWrite windows.Handle
	if err := windows.CreatePipe(&outRead, &outWrite, nil, 0); err != nil {
		_ = windows.CloseHandle(inRead)
		_ = windows.CloseHandle(inWrite)
		return nil, fmt.Errorf("create output pipe: %w", err)
	}

	var hpc windows.Handle
	size := windows.Coord{X: int16(cols), Y: int16(rows)}
	if err := windows.CreatePseudoConsole(size, inRead, outWrite, 0, &hpc); err != nil {
		_ = windows.CloseHandle(inRead)
		_ = windows.CloseHandle(inWrite)
		_ = windows.CloseHandle(outRead)
		_ = windows.CloseHandle(outWrite)
		return nil, fmt.Errorf("create pseudoconsole: %w", err)
	}
	_ = windows.CloseHandle(inRead)
	_ = windows.CloseHandle(outWrite)

	attrList, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		windows.ClosePseudoConsole(hpc)
		_ = windows.CloseHandle(inWrite)
		_ = windows.CloseHandle(outRead)
		return nil, fmt.Errorf("allocate attribute list: %w", err)
	}
	defer attrList.Delete()

	var hpcAttr unsafe.Pointer
	*(*windows.Handle)(unsafe.Pointer(&hpcAttr)) = hpc
	if err := attrList.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, hpcAttr, unsafe.Sizeof(hpc)); err != nil {
		windows.ClosePseudoConsole(hpc)
		_ = windows.CloseHandle(inWrite)
		_ = windows.CloseHandle(outRead)
		return nil, fmt.Errorf("attach pseudoconsole attribute: %w", err)
	}

	shell, _ := defaultShell(explicitShell)
	cmdLinePtr, err := windows.UTF16PtrFromString(`"` + shell + `"`)
	if err != nil {
		windows.ClosePseudoConsole(hpc)
		_ = windows.CloseHandle(inWrite)
		_ = windows.CloseHandle(outRead)
		return nil, err
	}
	var dirPtr *uint16
	if dir != "" {
		dirPtr, err = windows.UTF16PtrFromString(dir)
		if err != nil {
			windows.ClosePseudoConsole(hpc)
			_ = windows.CloseHandle(inWrite)
			_ = windows.CloseHandle(outRead)
			return nil, err
		}
	}
	envBlock, err := buildUTF16EnvBlock(buildEnv())
	if err != nil {
		windows.ClosePseudoConsole(hpc)
		_ = windows.CloseHandle(inWrite)
		_ = windows.CloseHandle(outRead)
		return nil, err
	}

	var si windows.StartupInfoEx
	si.Cb = uint32(unsafe.Sizeof(si))
	si.ProcThreadAttributeList = attrList.List()
	var pi windows.ProcessInformation

	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT)
	if err := windows.CreateProcess(nil, cmdLinePtr, nil, nil, false, flags, envBlock, dirPtr, &si.StartupInfo, &pi); err != nil {
		windows.ClosePseudoConsole(hpc)
		_ = windows.CloseHandle(inWrite)
		_ = windows.CloseHandle(outRead)
		return nil, fmt.Errorf("spawn shell %q: %w", shell, err)
	}

	return &windowsSession{
		hpc:     hpc,
		inFile:  os.NewFile(uintptr(inWrite), "conpty-in"),
		outFile: os.NewFile(uintptr(outRead), "conpty-out"),
		proc:    pi.Process,
		thread:  pi.Thread,
	}, nil
}

func (s *windowsSession) Read(p []byte) (int, error) {
	return s.outFile.Read(p)
}

func (s *windowsSession) Write(p []byte) (int, error) {
	return s.inFile.Write(p)
}

func (s *windowsSession) Resize(cols, rows uint16) error {
	cols, rows = normalizeSize(cols, rows)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("terminal session is closed")
	}
	return windows.ResizePseudoConsole(s.hpc, windows.Coord{X: int16(cols), Y: int16(rows)})
}

func (s *windowsSession) Wait() (int, error) {
	s.waitOnce.Do(func() {
		if _, err := windows.WaitForSingleObject(s.proc, windows.INFINITE); err != nil {
			s.waitCode = -1
			s.waitErr = err
			return
		}
		var code uint32
		if err := windows.GetExitCodeProcess(s.proc, &code); err != nil {
			s.waitCode = -1
			s.waitErr = err
			return
		}
		s.waitCode = int(code)
	})
	return s.waitCode, s.waitErr
}

func (s *windowsSession) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()

		windows.ClosePseudoConsole(s.hpc)
		_ = s.inFile.Close()
		_ = s.outFile.Close()
		_ = windows.TerminateProcess(s.proc, 1)
		_, _ = windows.WaitForSingleObject(s.proc, 500)
		_ = windows.CloseHandle(s.thread)
		s.closeErr = windows.CloseHandle(s.proc)
	})
	return s.closeErr
}
