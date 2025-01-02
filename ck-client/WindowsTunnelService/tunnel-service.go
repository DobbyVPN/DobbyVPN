package main

import (
	"log"
	"os"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"

	"github.com/amnezia-vpn/amneziawg-windows/tunnel"
)

func main() {
	if windows.SetDllDirectory("") != nil || windows.SetDefaultDllDirectories(windows.LOAD_LIBRARY_SEARCH_SYSTEM32) != nil {
		panic("failed to restrict dll search path")
	}
	
	inService, err := svc.IsWindowsService()
	if err != nil {
		log.Fatalf("Failed to determine if we are running in service: %v", err)
	}

	if inService {
		err := tunnel.Run(os.Args[1])
		if err != nil {
			log.Fatalf("Filed to run tunnel service: %s", err)
		}
	} else {
		log.Fatalf("Not in a service mode")
	}

}
