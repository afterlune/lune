package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
)

type webOptions struct {
	workspace string
	noOpen    bool
}

func parseWebOptions(args []string, stdout io.Writer) (webOptions, bool, error) {
	var options webOptions
	flags := flag.NewFlagSet("lune web", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.workspace, "workspace", "", "workspace directory")
	flags.BoolVar(&options.noOpen, "no-open", false, "print the launch link instead of opening the browser")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stdout, "Usage: lune web [--workspace DIRECTORY] [--no-open]")
			return options, false, nil
		}
		return options, false, err
	}
	if flags.NArg() != 0 {
		return options, false, errors.New("usage: lune web [--workspace DIRECTORY] [--no-open]")
	}
	return options, true, nil
}
