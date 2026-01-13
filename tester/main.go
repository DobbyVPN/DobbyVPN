package main

import "C"
import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	pb "tester/vpnserver"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	GRPC_EXECUTABLE string = ""
	GRPC_ADDRESS    string = ""
	TESTER_CONFIG   string = ""
	IPINFO_URL      string = "https://api.myip.com/"
	TESTING_CONFIG  string = ""
	LOCALHOST_IP    string = ""
)

type IPData struct {
	IP      string `json:"ip"`
	Country string `json:"country"`
	CC      string `json:"cc"`
}

func getIpData() (*IPData, error) {
	client := http.Client{
		Timeout: 2 * time.Second,
	}
	resp, err := client.Get(IPINFO_URL)
	if err != nil {
		return nil, fmt.Errorf("Error getting ip info: %v", err)
	}

	var ipData IPData
	err = json.NewDecoder(resp.Body).Decode(&ipData)
	if err != nil {
		return nil, fmt.Errorf("Error decoding JSON: %v", err)
	}

	resp.Body.Close()
	client.CloseIdleConnections()

	log.Printf("Got ip: ip=%v, country_code=%v", ipData.IP, ipData.CC)

	return &ipData, nil
}

type TesterConfig struct {
	Tests []TestConfig `json:"tests"`
}

type TestConfig struct {
	Description string     `json:"description"`
	Steps       []TestStep `json:"steps"`
}

type TestStep struct {
	Action string                 `json:"action"`
	Args   map[string]interface{} `json:"args"`
}

func parceIpMatch(ip string) string {
	switch ip {
	case "localhost":
		return LOCALHOST_IP
	default:
		return ip
	}
}

func runTestStep(testStep TestStep) error {
	log.Printf("Running test step\n")

	switch testStep.Action {
	case "assert":
		if ip, ok := testStep.Args["ip"].(string); ok {
			log.Printf("Checking ip exact match")

			ipMatch := parceIpMatch(ip)

			ipData, err := getIpData()
			if err != nil {
				return fmt.Errorf("Error loading current ip: %v", err)
			}

			if ipData.IP != ipMatch {
				return fmt.Errorf("Assertion error: current ip: %v, expected: %v", ipData.IP, ipMatch)
			} else {
				log.Printf("Assertion succeed")

				return nil
			}
		}

		if ipCountryCode, ok := testStep.Args["ip_cc"].(string); ok {
			log.Printf("Checking ip country code match")

			ipData, err := getIpData()
			if err != nil {
				return fmt.Errorf("Error loading current ip: %v", err)
			}

			if ipData.CC != ipCountryCode {
				return fmt.Errorf("Assertion error: current ip coutrye code: %v, expected: %v", ipData.CC, ipCountryCode)
			} else {
				log.Printf("Assertion succeed")

				return nil
			}
		}

		return fmt.Errorf("Invalid assertion arguments")
	case "timeout":
		if seconds, ok := testStep.Args["seconds"].(string); ok {
			log.Printf("Sleeping %v seconds\n", seconds)

			secondsAsInt, err := strconv.Atoi(seconds)
			if err == nil {
				time.Sleep(time.Second * time.Duration(secondsAsInt))

				log.Printf("Sleeping done\n")

				return nil
			}
		}

		return fmt.Errorf("Invalid timeout arguments")
	case "StartAwg":
		if tunnel, ok := testStep.Args["tunnel"].(string); ok {
			if config, ok := testStep.Args["config"].(string); ok {
				log.Printf("Creating gRPC client\n")

				conn, err := grpc.NewClient(GRPC_ADDRESS, grpc.WithTransportCredentials(insecure.NewCredentials()))
				if err != nil {
					return fmt.Errorf("Did not connect: %v", err)
				}
				defer conn.Close()

				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()

				log.Printf("Starting tunnel\n")

				vpnclient := pb.NewVpnClient(conn)
				log.Printf("Created gRPC client")

				_, err = vpnclient.StartAwg(ctx, &pb.StartAwgRequest{Tunnel: tunnel, Config: config})
				if err != nil {
					return fmt.Errorf("Failed to StartAwg: %v", err)
				}

				log.Printf("Sent StartAwg")

				return nil
			}
		}

		return fmt.Errorf("Invalid StartAwg arguments")
	case "StopAwg":
		log.Printf("Creating gRPC client\n")

		conn, err := grpc.NewClient(GRPC_ADDRESS, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("Did not connect: %v", err)
		}
		defer conn.Close()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		log.Printf("Starting tunnel\n")

		vpnclient := pb.NewVpnClient(conn)
		log.Printf("Created gRPC client")

		_, err = vpnclient.StopAwg(ctx, &pb.Empty{})
		if err != nil {
			return fmt.Errorf("Failed to StopAwg: %v", err)
		}

		log.Printf("Sent StopAwg")

		return nil
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
		log.Fatalf("Error reading file:", err)
	}

	var testerConfig TesterConfig
	if err := json.Unmarshal(data, &testerConfig); err != nil {
		log.Fatalf("Error parsing json file:", err)
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
}
