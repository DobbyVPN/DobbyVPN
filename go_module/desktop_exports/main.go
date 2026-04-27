//go:build !(android || ios)

package main

import (
	"flag"
	executor "go_module/desktop_exports/executor"
)

func main() {
	var SERVER_PORT int
	var SERVER_MODE string
	flag.IntVar(&SERVER_PORT, "port", 50051, "The server port")
	flag.StringVar(&SERVER_MODE, "mode", "normal", "Run mode")
	flag.Parse()

	ex := &executor.Executor{}
	ex.Execute(SERVER_PORT, SERVER_MODE)
}
