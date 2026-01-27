package main

import (
	"fmt"
	"log"
)

func assertIpExact(ipMatch string) error {
	log.Printf("Checking ip exact match")

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

func assertIpCountryCode(ipCountryCode string) error {
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
