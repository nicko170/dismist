package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func run(args []string, output io.Writer) error {
	if len(args) == 0 || args[0] != "init" {
		return errors.New("usage: dismist init --data-dir DIR")
	}
	flags := flag.NewFlagSet("dismist init", flag.ContinueOnError)
	flags.SetOutput(output)
	dataDir := flags.String("data-dir", "", "private data directory")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *dataDir == "" || flags.NArg() != 0 {
		return errors.New("usage: dismist init --data-dir DIR")
	}
	if err := initialize(*dataDir); err != nil {
		return err
	}
	fmt.Fprintf(output, "Dismist credentials ready in %s\n", *dataDir)
	fmt.Fprintf(output, "Read the administrator token locally from %s\n", filepath.Join(*dataDir, "admin.token"))
	fmt.Fprintf(output, "Install %s in the AP trust store before connecting an AP.\n", filepath.Join(*dataDir, "ca.crt"))
	return nil
}
