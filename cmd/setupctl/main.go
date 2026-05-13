package main

import (
	"fmt"
	"os"
	"runtime"

	"git.justw.tf/LightZirconite/setup-win/internal/setup"
)

var version = "dev"

func main() {
	if err := setup.Run(os.Args[1:], version); err != nil {
		fmt.Fprintln(os.Stderr, "Initra:", err)
		pauseOnWindowsLaunchError()
		os.Exit(1)
	}
}

func pauseOnWindowsLaunchError() {
	if runtime.GOOS != "windows" {
		return
	}
	if len(os.Args) > 1 {
		return
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprint(os.Stderr, "Press Enter to close...")
	_, _ = fmt.Scanln()
}
