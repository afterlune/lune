package cli

import (
	"fmt"
	"os"
	"sync"

	"github.com/eiannone/keyboard"
)

// The keyboard library keeps process-wide state and needs a real terminal, so the
// watcher reaches the outside world through these seams. Tests replace them
// instead of driving stdin or the shared keyboard state.
var (
	stdinIsCharacterDevice = func() (bool, error) {
		info, err := os.Stdin.Stat()
		if err != nil {
			return false, err
		}
		return info.Mode()&os.ModeCharDevice != 0, nil
	}
	openCLIKeys = func(buffer int) (<-chan keyboard.KeyEvent, error) {
		keys, err := keyboard.GetKeys(buffer)
		if err != nil {
			return nil, err
		}
		return keys, nil
	}
	closeCLIKeys = keyboard.Close
)

func startKeyboardInputWatcher() (<-chan cliInputEvent, func(), error) {
	interactive, err := stdinIsCharacterDevice()
	if err != nil {
		return nil, func() {}, fmt.Errorf("inspect stdin: %w", err)
	}
	if !interactive {
		return nil, func() {}, nil
	}
	keys, err := openCLIKeys(10)
	if err != nil {
		return nil, func() {}, err
	}

	inputEvents := make(chan cliInputEvent, 16)
	stopRequested := make(chan struct{})
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		defer close(inputEvents)
		for event := range keys {
			if event.Err == nil && event.Key != keyboard.KeyEsc && event.Rune == 0 {
				continue
			}
			input := cliInputEvent{
				rune:   event.Rune,
				escape: event.Key == keyboard.KeyEsc,
				err:    event.Err,
			}
			select {
			case inputEvents <- input:
			case <-stopRequested:
				return
			}
		}
	}()

	var once sync.Once
	stop := func() {
		once.Do(func() {
			close(stopRequested)
			_ = closeCLIKeys()
			<-watcherDone
		})
	}
	return inputEvents, stop, nil
}
