//go:build !windows

package main

import (
	"fmt"
	"os"
)

func showStartupError(err error) { fmt.Fprintln(os.Stderr, err) }
