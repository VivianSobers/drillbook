// Command drillbook runs fire drills on Prometheus alerts and their runbooks.
package main

import (
	"fmt"
	"os"

	"github.com/VivianSobers/drillbook/internal/cli"
)

func main() {
	if err := cli.New(os.Stdout).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "drillbook:", err)
		os.Exit(1)
	}
}
