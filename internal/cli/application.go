package cli

import (
	"bufio"
	"os"

	core "github.com/afterlune/luneCore"
)

func Run(client *core.Core) error {
	return runCoreChatLoop(coreCLIApplication{Core: client}, bufio.NewReader(os.Stdin), os.Stdout, os.Stderr, startKeyboardInputWatcher)
}
