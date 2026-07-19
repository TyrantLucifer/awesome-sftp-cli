package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/docscheck"
)

func main() {
	root, release, err := parseRequest(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	var findings []docscheck.Finding
	if release {
		findings = docscheck.CheckRelease(root)
	} else {
		findings = docscheck.Check(root)
	}
	for _, finding := range findings {
		fmt.Printf("%s:%d: %s: %s\n", finding.Path, finding.Line, finding.Rule, finding.Message)
	}
	if len(findings) > 0 {
		os.Exit(1)
	}
}

func parseRequest(args []string) (string, bool, error) {
	const usage = "usage: docscheck [--release] [repository-root]"
	switch len(args) {
	case 0:
		return ".", false, nil
	case 1:
		if args[0] == "--release" {
			return ".", true, nil
		}
		if strings.HasPrefix(args[0], "-") {
			return "", false, errors.New(usage)
		}
		return args[0], false, nil
	case 2:
		if args[0] != "--release" || strings.HasPrefix(args[1], "-") {
			return "", false, errors.New(usage)
		}
		return args[1], true, nil
	default:
		return "", false, errors.New(usage)
	}
}
