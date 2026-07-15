package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(arguments []string, stderr io.Writer) int {
	if len(arguments) == 0 {
		printUsage(stderr)
		return 2
	}
	switch arguments[0] {
	case "scan":
		if len(arguments) != 2 || arguments[1] != "Makefile" {
			printUsage(stderr)
			return 2
		}
		content, err := os.ReadFile("Makefile")
		if err != nil {
			fmt.Fprintf(stderr, "make-contract: read %s: %v\n", arguments[1], err)
			return 1
		}
		findings := scanSource(arguments[1], string(content))
		if len(findings) != 0 {
			fmt.Fprint(stderr, formatFindings(findings))
			return 1
		}
		return 0
	case "fmt":
		if len(arguments) != 3 || arguments[1] != "cmd" || arguments[2] != "internal" {
			printUsage(stderr)
			return 2
		}
		files, err := unformattedGoFiles([]string{"cmd", "internal"})
		if err != nil {
			fmt.Fprintf(stderr, "make-contract: %v\n", err)
			return 1
		}
		if len(files) != 0 {
			for _, path := range files {
				fmt.Fprintln(stderr, path)
			}
			return 1
		}
		return 0
	case "verify":
		flags := flag.NewFlagSet("verify", flag.ContinueOnError)
		flags.SetOutput(stderr)
		goExecutable := flags.String("go", "go", "Go executable")
		if err := flags.Parse(arguments[1:]); err != nil || flags.NArg() != 0 {
			printUsage(stderr)
			return 2
		}
		err := verifyContract(*goExecutable)
		if err != nil {
			fmt.Fprintf(stderr, "make-contract: %v\n", err)
			return 1
		}
		return 0
	default:
		printUsage(stderr)
		return 2
	}
}

func printUsage(output io.Writer) {
	fmt.Fprintln(output, "usage: makecontract scan <Makefile>")
	fmt.Fprintln(output, "       makecontract fmt cmd internal")
	fmt.Fprintln(output, "       makecontract verify [--go <path>]")
}
