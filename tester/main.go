package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

var (
	GRPC_EXECUTABLE string = ""
	GRPC_ADDRESS    string = ""
	TESTER_CONFIG   string = ""
	IPINFO_URL      string = "https://api.myip.com/"
	TESTING_CONFIG  string = ""
)

func runTestStep(testStep TestStep) error {
	log.Printf("Running test step\n")

	switch testStep.Action {
	case "assert":
		return assertStep(testStep)
	case "timeout":
		return timeoutStep(testStep)
	case "StartAwg":
		return startAwgStep(testStep)
	case "StopAwg":
		return stopAwgStep()
	default:
		return fmt.Errorf("Unexpected action %v", testStep.Action)
	}
}

func runTest(testNumber int, testerConfig TestConfig) error {
	log.Printf("Running test: %v\n", testerConfig.Description)

	tmpFile, err := os.CreateTemp("./", "vpnserver-output-*.log")
	if err != nil {
		return fmt.Errorf("Error creating vpn subprocess temporal log file: %v", err)
	}
	defer tmpFile.Close()

	path, err := filepath.Abs(tmpFile.Name())
	if err != nil {
		return fmt.Errorf("Error printing temporal file absolute path: %v", err)
	}
	log.Printf("Created temp log file: %v\n", path)

	cmd := exec.Command(GRPC_EXECUTABLE)
	cmd.Stdout = tmpFile
	cmd.Stderr = tmpFile

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("Failed to start vpn subprocess: %v\n", err)
	}
	defer func() {
		log.Println("Interrupting subprocess...")
		cmd.Process.Kill()

		err := cmd.Wait()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				log.Printf("Subprocess exited with code: %d\n", exitErr.ExitCode())
			} else {
				log.Printf("Wait error: %v\n", err)
			}
		}
	}()

	log.Printf("VPN subprocess run, PID: %d\n", cmd.Process.Pid)

	for index, step := range testerConfig.Steps {
		log.SetPrefix(fmt.Sprintf("[LOG test:%d step:%d] ", testNumber, index))
		err := runTestStep(step)
		if err != nil {
			return fmt.Errorf("Error while running test step: %v\n", err)
		}
	}

	return nil
}

func prepare() error {
	log.Printf("Preparing")

	ipData, err := getIpData()
	if err != nil {
		return fmt.Errorf("Error geting localhost ip: %v", err)
	}
	log.Printf("Setting LOCALHOST_IP=%v\n", ipData.IP)
	LOCALHOST_IP = ipData.IP

	log.Printf("Preparing completed")

	return nil
}

func main() {
	flag.StringVar(&GRPC_EXECUTABLE, "path", "grpcvpnserver", "VPN service executable absolute path")
	flag.StringVar(&GRPC_ADDRESS, "addr", "localhost:50051", "The address to connect to")
	flag.StringVar(&TESTER_CONFIG, "conf", "./config.json", "Tester config path")
	flag.Parse()

	log.SetPrefix("[LOG] ")

	log.Printf("Reading config %v\n", TESTER_CONFIG)

	data, err := os.ReadFile(TESTER_CONFIG)
	if err != nil {
		log.Fatalf("Error reading file: %v", err)
	}

	var testerConfig TesterConfig
	if err := json.Unmarshal(data, &testerConfig); err != nil {
		log.Fatalf("Error parsing json file: %v", err)
	}

	err = prepare()
	if err != nil {
		log.Fatalf("Error while prepating: %v", err)
	}

	for testNumber, testerConfig := range testerConfig.Tests {
		log.SetPrefix(fmt.Sprintf("[LOG test:%d] ", testNumber))

		err := runTest(testNumber, testerConfig)
		if err != nil {
			log.Fatalf("Error while running test: %v", err)
		}
	}

	log.SetPrefix("[LOG] ")

	log.Printf("✓ All tests passed")
}
