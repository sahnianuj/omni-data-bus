// Command covergate fails when any package's coverage is below its threshold.
//
//	go run ./tools/covergate/cmd/covergate coverage/cover.out
package main

import (
	"fmt"
	"os"

	"omnidatabus/tools/covergate"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	th := covergate.DefaultThresholds()
	if len(args) == 2 && args[0] == "--integration" {
		th, args = covergate.IntegrationThresholds(), args[1:]
	}
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: covergate [--integration] <cover.out>")
		return 2
	}
	f, err := os.Open(args[0]) //nolint:gosec // CLI tool: the profile path is the operator's argument
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	defer func() { _ = f.Close() }()
	results, err := covergate.Evaluate(f, th)
	for _, r := range results {
		fmt.Printf("%-60s %5.1f%% (min %.0f%%)\n", r.Package, r.Percent, r.Threshold)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
