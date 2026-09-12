// Command fmtcheck checks Go source formatting without modifying files.
package main

import (
	"fmt"
	"os"
)

func main() {
	files, err := unformattedGoFiles([]string{"cmd", "internal"})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, path := range files {
		fmt.Fprintln(os.Stderr, path)
	}
	if len(files) != 0 {
		os.Exit(1)
	}
}
