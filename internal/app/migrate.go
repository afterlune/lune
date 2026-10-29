package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	core "github.com/afterlune/luneCore"
)

func runMigration(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("lune migrate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	stateDir := flags.String("state-dir", "", "existing Lune state directory (all Lune instances must be stopped)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *stateDir == "" || flags.NArg() != 0 {
		return errors.New("usage: lune migrate --state-dir <directory>; unfinished tasks will be discarded after backup")
	}
	report, err := core.UpgradeState(context.Background(), *stateDir)
	if err != nil {
		return err
	}
	if report.AlreadyCurrent {
		_, err = fmt.Fprintln(stdout, "Execution storage is already current.")
		return err
	}
	_, err = fmt.Fprintf(stdout, "Execution storage upgraded. Discarded %d unfinished task(s); history, settings, and recorded usage were preserved.\nBackup: %s\nLegacy checkpoint files were left untouched.\n", report.DiscardedTasks, report.BackupDirectory)
	return err
}
