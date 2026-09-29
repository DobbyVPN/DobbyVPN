//go:build !(android || ios)

package main

import (
	executor "core/clientserver/executor"
	"flag"
)

func main() {
	var mode string
	flag.StringVar(&mode, "mode", "normal", "Run mode")
	flag.Parse()

	ex := &executor.Executor{}
	ex.Execute(mode)
}
