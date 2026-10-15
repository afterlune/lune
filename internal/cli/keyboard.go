package cli

import (
	"fmt"
	"os"
	"sync"

	"github.com/eiannone/keyboard"
)

func startKeyboardInputWatcher() (<-chan cliInputEvent, func(), error) {
	info, err := os.Stdin.Stat()
	if err != nil {
		return nil, func() {}, fmt.Errorf("inspect stdin: %w", err)
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		return nil, func() {}, nil
	}
	keys, err := keyboard.GetKeys(10)
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
			_ = keyboard.Close()
			<-watcherDone
		})
	}
	return inputEvents, stop, nil
}
