package main

import (
	"context"
	"fmt"
	"log"
	"strconv"
	pb "tester/vpnserver"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	LOCALHOST_IP string = "127.0.0.1"
)

func parceIpMatch(ip string) string {
	switch ip {
	case "localhost":
		return LOCALHOST_IP
	default:
		return ip
	}
}

func assertStep(testStep TestStep) error {
	if ip, ok := testStep.Args["ip"].(string); ok {
		ipMatch := parceIpMatch(ip)

		return assertIpExact(ipMatch)
	}

	if ipCountryCode, ok := testStep.Args["ip_cc"].(string); ok {
		return assertIpCountryCode(ipCountryCode)
	}

	return fmt.Errorf("Invalid assertion arguments")
}

func timeoutStep(testStep TestStep) error {
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
}

func startAwgStep(testStep TestStep) error {
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
}

func stopAwgStep() error {
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
}
