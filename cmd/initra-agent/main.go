package main

import (
	"fmt"
	"os"

	"git.justw.tf/LightZirconite/setup-win/internal/agent"
)

var version = "dev"

func main() {
	if err := agent.Main(os.Args[1:], version); err != nil {
		fmt.Fprintln(os.Stderr, "Initra Agent:", err)
		os.Exit(1)
	}
}
