//go:build !windows

package executor

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

type Executor struct{}

func empty() error {
	return nil
}

func run(executable string) (func() error, error) {
	tmpFile, err := os.CreateTemp("./", "vpnserver-output-*.log")
	if err != nil {
		return empty, fmt.Errorf("Error creating vpn subprocess temporal log file: %v", err)
	}
	defer tmpFile.Close()

	path, err := filepath.Abs(tmpFile.Name())
	if err != nil {
		return empty, fmt.Errorf("Error printing temporal file absolute path: %v", err)
	}
	log.Printf("Created temp log file: %v\n", path)

	cmd := exec.Command(executable)
	cmd.Stdout = tmpFile
	cmd.Stderr = tmpFile

	if err := cmd.Start(); err != nil {
		return empty, fmt.Errorf("Failed to start vpn subprocess: %v\n", err)
	}

	stop := func() error {
		log.Println("Interrupting subprocess...")
		cmd.Process.Kill()

		err := cmd.Wait()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				log.Printf("Subprocess exited with code: %d\n", exitErr.ExitCode())
			} else {
				log.Printf("Wait error: %v\n", err)

				return err
			}
		}

		return nil
	}

	return stop, nil
}

func (e *Executor) Execute(executable, mode string) (func() error, error) {
	switch mode {
	case "normal":
		return run(executable)
	default:
		return empty, fmt.Errorf("Unexpected mode: %v", mode)
	}
}
