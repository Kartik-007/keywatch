package main

import (
	"fmt"
	"os"

	"github.com/Kartik-007/keywatch/internal/cli"
)

var version = "dev"

func main() {
	cli.Root.Version = version
	if err := cli.Root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "keywatch:", err)
		os.Exit(1)
	}
}
