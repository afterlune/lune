//go:build !lune_web

package app

import (
	"errors"
	"io"
)

func runWeb(args []string, stdout, _ io.Writer) error {
	if _, run, err := parseWebOptions(args, stdout); err != nil || !run {
		return err
	}
	return errors.New("web assets are not embedded; rebuild with LUNE_EMBED_WEB=1")
}
