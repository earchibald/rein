package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run())
}

func run() int {
	app := newApp(os.Stdout, os.Stderr, os.LookupEnv, os.UserHomeDir)
	if err := app.run(os.Args[1:]); err != nil {
		return parseErrorExitCode(err, os.Stderr)
	}
	return 0
}

func parseErrorExitCode(err error, stderr io.Writer) int {
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}

	_, _ = fmt.Fprintln(stderr, err)
	return 2
}
