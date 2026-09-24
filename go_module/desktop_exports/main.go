//go:build !(android || ios)

package main

import (
	"flag"
	executor "go_module/desktop_exports/executor"
)

func main() {
	var mode string
	flag.StringVar(&mode, "mode", "normal", "Run mode")
	flag.Parse()

	ex := &executor.Executor{}
	ex.Execute(mode)
}
