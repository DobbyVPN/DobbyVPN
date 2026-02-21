package util

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

var (
	IPINFO_URL string = "https://ipinfo.io/json"
)

type IPData struct {
	IP      string `json:"ip"`
	Country string `json:"region"`
	CC      string `json:"country"`
}

func GetIpData() (*IPData, error) {
	client := http.Client{
		Timeout: 4 * time.Second,
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
