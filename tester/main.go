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
	"time"

	pb "tester/vpnserver"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	GRPC_EXECUTABLE string = ""
	GRPC_ADDRESS    string = ""
	IPINFO_URL      string = "https://api.myip.com/"
	TESTING_CONFIG  string = ""
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

func testing() error {
	log.Printf("Testing if everything's ok with network\n")

	ipData, err := getIpData()
	if err != nil {
		return fmt.Errorf("Error getting ip data: %v\n", err)
	}

	if ipData.CC != "RU" {
		return fmt.Errorf("Expected RU country, got %v", ipData.CC)
	}

	log.Printf("Creating gRPC client\n")

	conn, err := grpc.NewClient(GRPC_ADDRESS, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("Did not connect: %v", err)
	} else {
		log.Printf("Created gRPC client")
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	log.Printf("Starting tunnel\n")

	vpnclient := pb.NewVpnClient(conn)
	_, err = vpnclient.StartAwg(ctx, &pb.StartAwgRequest{Tunnel: "awg", Config: TESTING_CONFIG})
	if err != nil {
		return fmt.Errorf("Failed to StartAwg: %v", err)
	} else {
		log.Printf("Sent StartAwg")
	}

	log.Printf("Sleeping 2 seconds\n")

	time.Sleep(time.Second * 2)

	log.Printf("Testing if ip has changed\n")

	ipData, err = getIpData()
	if err != nil {
		return fmt.Errorf("Error getting ip data: %v\n", err)
	}

	if ipData.CC != "NL" {
		return fmt.Errorf("Expected NL country, got %v", ipData.CC)
	}

	return nil
}

func main() {
	flag.StringVar(&GRPC_EXECUTABLE, "path", "grpcvpnserver", "VPN service executable absolute path")
	flag.StringVar(&GRPC_ADDRESS, "addr", "localhost:50051", "The address to connect to")
	flag.Parse()

	log.SetPrefix("[LOG] ")
	log.Printf("Running %v\n", GRPC_EXECUTABLE)

	cmd := exec.Command("sudo", GRPC_EXECUTABLE)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		log.Fatalf("Failed to start: %v\n", err)
	}

	log.Printf("Subprocess PID: %d\n", cmd.Process.Pid)

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

	err := testing()
	if err != nil {
		log.Printf("Error while testing: %v\n", err)
	}
}
