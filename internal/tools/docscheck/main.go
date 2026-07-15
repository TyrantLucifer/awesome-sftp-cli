package main

import (
	"fmt"
	"os"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/docscheck"
)

func main() {
	root := "."
	if len(os.Args) > 2 {
		fmt.Fprintln(os.Stderr, "usage: docscheck [repository-root]")
		os.Exit(2)
	}
	if len(os.Args) == 2 {
		root = os.Args[1]
	}

	findings := docscheck.Check(root)
	for _, finding := range findings {
		fmt.Printf("%s:%d: %s: %s\n", finding.Path, finding.Line, finding.Rule, finding.Message)
	}
	if len(findings) > 0 {
		os.Exit(1)
	}
}
