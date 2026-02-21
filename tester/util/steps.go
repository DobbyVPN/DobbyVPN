package util

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

type Tester struct {
	GrpcExecutablePath string
	GrpcAddress        string
	LocalhostIp        string
}

func (tester *Tester) parceIpMatch(ip string) string {
	switch ip {
	case "localhost":
		return tester.LocalhostIp
	default:
		return ip
	}
}

func (tester *Tester) RunTestStep(testStep TestStep) error {
	log.Printf("Running test step\n")

	switch testStep.Action {
	case "assert":
		return tester.AssertStep(testStep)
	case "timeout":
		return tester.TimeoutStep(testStep)
	case "StartAwg":
		return tester.StartAwgStep(testStep)
	case "StopAwg":
		return tester.StopAwgStep()
	case "StartOutline":
		return tester.StartOutlineStep(testStep)
	case "StopOutline":
		return tester.StopOutlineStep()
	case "StartCloak":
		return tester.StartCloakStep(testStep)
	case "StopCloak":
		return tester.StopCloakStep()
	case "InitLogger":
		return tester.InitLoggerStep(testStep)
	default:
		return fmt.Errorf("Unexpected action %v", testStep.Action)
	}
}

func (tester *Tester) AssertStep(testStep TestStep) error {
	if ip, ok := testStep.Args["ip"].(string); ok {
		ipMatch := tester.parceIpMatch(ip)

		return AssertIpExact(ipMatch)
	}

	if ipCountryCode, ok := testStep.Args["ip_cc"].(string); ok {
		return AssertIpCountryCode(ipCountryCode)
	}

	return fmt.Errorf("Invalid assertion arguments")
}

func (tester *Tester) TimeoutStep(testStep TestStep) error {
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

func (tester *Tester) StartAwgStep(testStep TestStep) error {
	if tunnel, ok := testStep.Args["tunnel"].(string); ok {
		if config, ok := testStep.Args["config"].(string); ok {
			log.Printf("Creating gRPC client\n")

			conn, err := grpc.NewClient(tester.GrpcAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
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

func (tester *Tester) StopAwgStep() error {
	log.Printf("Creating gRPC client\n")

	conn, err := grpc.NewClient(tester.GrpcAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
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

func (tester *Tester) StartOutlineStep(testStep TestStep) error {
	if config, ok := testStep.Args["key"].(string); ok {
		log.Printf("Creating gRPC client\n")

		conn, err := grpc.NewClient(tester.GrpcAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("Did not connect: %v", err)
		}
		defer conn.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		log.Printf("Starting tunnel\n")

		vpnclient := pb.NewVpnClient(conn)
		log.Printf("Created gRPC client")

		_, err = vpnclient.StartOutline(ctx, &pb.StartOutlineRequest{Config: config})
		if err != nil {
			return fmt.Errorf("Failed to StartOutline: %v", err)
		}

		log.Printf("Sent StartOutline")

		return nil
	}

	return fmt.Errorf("Invalid StartOutline arguments")
}

func (tester *Tester) StopOutlineStep() error {
	log.Printf("Creating gRPC client\n")

	conn, err := grpc.NewClient(tester.GrpcAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("Did not connect: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	log.Printf("Starting tunnel\n")

	vpnclient := pb.NewVpnClient(conn)
	log.Printf("Created gRPC client")

	_, err = vpnclient.StopOutline(ctx, &pb.Empty{})
	if err != nil {
		return fmt.Errorf("Failed to StopOutline: %v", err)
	}

	log.Printf("Sent StopOutline")

	return nil
}

func (tester *Tester) StartCloakStep(testStep TestStep) error {
	if localHost, ok := testStep.Args["localHost"].(string); ok {
		if localPort, ok := testStep.Args["localPort"].(string); ok {
			if config, ok := testStep.Args["config"].(string); ok {
				if udp, ok := testStep.Args["udp"].(string); ok {
					log.Printf("Creating gRPC client\n")

					conn, err := grpc.NewClient(tester.GrpcAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
					if err != nil {
						return fmt.Errorf("Did not connect: %v", err)
					}
					defer conn.Close()

					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					defer cancel()

					log.Printf("Starting tunnel\n")

					vpnclient := pb.NewVpnClient(conn)
					log.Printf("Created gRPC client")

					udpAsBoolean, err := strconv.ParseBool(udp)
					if err != nil {
						return fmt.Errorf("Cannot parse udp value: %v", err)
					}

					_, err = vpnclient.StartCloakClient(ctx, &pb.StartCloakClientRequest{
						LocalHost: localHost,
						LocalPort: localPort,
						Config:    config,
						Udp:       udpAsBoolean,
					})
					if err != nil {
						return fmt.Errorf("Failed to StartCloakClient: %v", err)
					}

					log.Printf("Sent StartCloakClient")

					return nil
				}
			}
		}
	}

	return fmt.Errorf("Invalid StartCloakClient arguments")
}

func (tester *Tester) StopCloakStep() error {
	log.Printf("Creating gRPC client\n")

	conn, err := grpc.NewClient(tester.GrpcAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("Did not connect: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	log.Printf("Starting tunnel\n")

	vpnclient := pb.NewVpnClient(conn)
	log.Printf("Created gRPC client")

	_, err = vpnclient.StopCloakClient(ctx, &pb.Empty{})
	if err != nil {
		return fmt.Errorf("Failed to StopCloakClient: %v", err)
	}

	log.Printf("Sent StopCloakClient")

	return nil
}

func (tester *Tester) InitLoggerStep(testStep TestStep) error {
	if path, ok := testStep.Args["path"].(string); ok {
		log.Printf("Creating gRPC client\n")

		conn, err := grpc.NewClient(tester.GrpcAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("Did not connect: %v", err)
		}
		defer conn.Close()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		log.Printf("Starting tunnel\n")

		vpnclient := pb.NewVpnClient(conn)
		log.Printf("Created gRPC client")

		_, err = vpnclient.InitLogger(ctx, &pb.InitLoggerRequest{Path: path})
		if err != nil {
			return fmt.Errorf("Failed to InitLogger: %v", err)
		}

		log.Printf("Sent InitLogger")

		return nil
	}

	return fmt.Errorf("Invalid InitLogger arguments")
}
