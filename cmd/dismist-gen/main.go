// dismist-gen generates the files derived from the option registry and the
// firmware schema (design §4.2):
//
//	dismist-gen options [-ts FILE] [-md FILE]   ui/src/gen/options.gen.ts and docs/design/options.gen.md
//	dismist-gen paths -walk FILE [-root TYPE]  ap33-0.15.paths from a 0.15 type walk (run once by hand)
//
// `go generate ./internal/options` runs the options subcommand.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "options":
		flags := flag.NewFlagSet("options", flag.ExitOnError)
		ts := flags.String("ts", "ui/src/gen/options.gen.ts", "TypeScript output")
		md := flags.String("md", "docs/design/options.gen.md", "Markdown reference output")
		_ = flags.Parse(os.Args[2:])
		if err := writeOptions(*ts, *md); err != nil {
			fmt.Fprintln(os.Stderr, "dismist-gen options:", err)
			os.Exit(1)
		}
	case "paths":
		flags := flag.NewFlagSet("paths", flag.ExitOnError)
		walk := flags.String("walk", "", "gorewalk dump of the EPConfig type (private research file)")
		root := flags.String("root", "config.EPConfig", "root type")
		_ = flags.Parse(os.Args[2:])
		if *walk == "" {
			usage()
		}
		in, err := os.Open(*walk)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer in.Close()
		if err := generatePaths(in, os.Stdout, *root); err != nil {
			fmt.Fprintln(os.Stderr, "dismist-gen paths:", err)
			os.Exit(1)
		}
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: dismist-gen options [-ts FILE] [-md FILE] | dismist-gen paths -walk FILE [-root TYPE]")
	os.Exit(2)
}
