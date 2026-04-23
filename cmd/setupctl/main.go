package main

import (
	"fmt"
	"os"

	"git.justw.tf/LightZirconite/setup-win/internal/setup"
)

var version = "dev"

func main() {
	if err := setup.Run(os.Args[1:], version); err != nil {
		fmt.Fprintln(os.Stderr, "Initra:", err)
		os.Exit(1)
	}
}
