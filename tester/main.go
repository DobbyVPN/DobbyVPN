package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	"tester/executor"
	"tester/util"
)

func runTest(tester util.Tester, testNumber int, testerConfig util.TestConfig) error {
	log.Printf("Running test: %v\n", testerConfig.Description)

	ex := &executor.Executor{}
	cancel, err := ex.Execute(tester.GrpcExecutablePath, testerConfig.Mode)
	if err != nil {
		return fmt.Errorf("Cannot execute vpn server: %v\n", err)
	}
	defer cancel()

	log.Printf("Started vpn server\n")

	for index, step := range testerConfig.Steps {
		log.SetPrefix(fmt.Sprintf("[LOG test:%d step:%d] ", testNumber, index))
		err := tester.RunTestStep(step)
		if err != nil {
			return fmt.Errorf("Error while running test step: %v\n", err)
		}
	}

	return nil
}

func prepare(path, address string) (util.Tester, error) {
	log.Printf("Preparing")

	ipData, err := util.GetIpData()
	if err != nil {
		return util.Tester{}, fmt.Errorf("Error geting localhost ip: %v", err)
	}
	log.Printf("Setting LOCALHOST_IP=%v\n", ipData.IP)
	var localhostIp = ipData.IP

	log.Printf("Preparing completed")

	return util.Tester{GrpcExecutablePath: path, GrpcAddress: address, LocalhostIp: localhostIp}, nil
}

func main() {
	var (
		grpcExecutable string
		grpcAddress    string
		config         string
	)

	flag.StringVar(&grpcExecutable, "path", "grpcvpnserver", "VPN service executable absolute path")
	flag.StringVar(&grpcAddress, "addr", "localhost:50051", "The address to connect to")
	flag.StringVar(&config, "conf", "./config.json", "Tester config path")

	flag.Parse()

	log.SetPrefix("[LOG] ")

	log.Printf("Reading config %v\n", config)

	data, err := os.ReadFile(config)
	if err != nil {
		log.Fatalf("Error reading file: %v", err)
	}

	var testerConfig util.TesterConfig
	if err := json.Unmarshal(data, &testerConfig); err != nil {
		log.Fatalf("Error parsing json file: %v", err)
	}

	tester, err := prepare(grpcExecutable, grpcAddress)
	if err != nil {
		log.Fatalf("Error while prepating: %v", err)
	}

	for testNumber, testerConfig := range testerConfig.Tests {
		log.SetPrefix(fmt.Sprintf("[LOG test:%d] ", testNumber))

		err := runTest(tester, testNumber, testerConfig)
		if err != nil {
			log.Fatalf("Error while running test: %v", err)
		}
	}

	log.SetPrefix("[LOG] ")

	log.Printf("✓ All tests passed")
}
