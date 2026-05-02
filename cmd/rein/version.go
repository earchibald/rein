package main

import (
	"flag"
	"fmt"

	"github.com/earchibald/rein/internal/buildinfo"
)

func (a *app) runVersion(args []string) error {
	flagSet := flag.NewFlagSet("rein version", flag.ContinueOnError)
	flagSet.SetOutput(a.stderr)

	jsonOutput := flagSet.Bool("json", false, "emit structured JSON")
	if err := flagSet.Parse(args); err != nil {
		return err
	}
	if flagSet.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flagSet.Args())
	}

	info := buildinfo.Current()
	if *jsonOutput {
		return writeJSONObject(a.stdout, info)
	}

	if _, err := fmt.Fprintf(a.stdout, "rein %s\n", info.Version); err != nil {
		return err
	}
	if info.Commit != "" {
		if _, err := fmt.Fprintf(a.stdout, "commit: %s\n", info.Commit); err != nil {
			return err
		}
	}
	if info.BuildTime != "" {
		if _, err := fmt.Fprintf(a.stdout, "build_time: %s\n", info.BuildTime); err != nil {
			return err
		}
	}
	if info.BuiltBy != "" {
		if _, err := fmt.Fprintf(a.stdout, "built_by: %s\n", info.BuiltBy); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(a.stdout, "go_version: %s\n", info.GoVersion); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(a.stdout, "platform: %s\n", info.Platform); err != nil {
		return err
	}
	_, err := fmt.Fprintf(a.stdout, "modified: %t\n", info.Modified)
	return err
}
