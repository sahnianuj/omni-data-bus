// Command checkgen fails when generated files differ from the committed versions.
//
//	go run ./tools/checkgen/cmd/checkgen <repo-root> <pathspec>...
package main

import (
	"context"
	"fmt"
	"os"

	"omnidatabus/tools/checkgen"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: checkgen <repo-root> <pathspec>...")
		os.Exit(2)
	}
	stale, err := checkgen.Stale(context.Background(), os.Args[1], os.Args[2:]...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if len(stale) > 0 {
		fmt.Fprintln(os.Stderr, "generated files are stale; run `make gen` and commit:")
		for _, f := range stale {
			fmt.Fprintln(os.Stderr, "  "+f)
		}
		os.Exit(1)
	}
	fmt.Println("generated files are up to date")
}
