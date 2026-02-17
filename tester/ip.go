package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

type IPData struct {
	IP      string `json:"ip"`
	Country string `json:"region"`
	CC      string `json:"country"`
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
