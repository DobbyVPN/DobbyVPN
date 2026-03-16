//go:build windows

package drivers

import (
	"bufio"
	"fmt"
	log "go_client/logger"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	deviceName     = "outline-tap0"
	deviceHwid     = "tap0901"
	tapInstallPath = "tap-windows6/tapinstall.exe"
	oemVistaPath   = "tap-windows6/OemVista.inf"
)

func updatePath() {
	currentPath := os.Getenv("PATH")

	newPath := fmt.Sprintf(
		"%s;%s\\system32;%s\\system32\\wbem;%s\\system32\\WindowsPowerShell\\v1.0",
		currentPath,
		filepath.Join(os.Getenv("SystemRoot"), "/system32"),
		filepath.Join(os.Getenv("SystemRoot"), "/system32/wbem"),
		filepath.Join(os.Getenv("SystemRoot"), "/system32/WindowsPowerShell/v1.02"),
	)

	os.Setenv("PATH", newPath)
	log.Infof("Updated PATH: %s", newPath)
}

func configureTapDevice(deviceName string) {
	var cmd *exec.Cmd

	log.Infof("Configuring TAP device subnet...")

	cmd = exec.Command(fmt.Sprintf("netsh interface ip set address %s static 10.0.85.2 255.255.255.255", deviceName))
	if cmd.Run() != nil {
		log.Infof("Could not set TAP network device subnet.")
		return
	}

	log.Infof("Configuring primary DNS...")

	cmd = exec.Command(fmt.Sprintf("netsh interface ip set dnsservers %s static address=1.1.1.1", deviceName))
	if cmd.Run() != nil {
		log.Infof("Could not configure TAP device primary DNS.")
		return
	}

	log.Infof("Configuring secondary DNS...")

	cmd = exec.Command(fmt.Sprintf("netsh interface ip add dnsservers %s 9.9.9.9 index=2", deviceName))
	if cmd.Run() != nil {
		log.Infof("Could not configure TAP device secondary DNS.")
		return
	}
}

func runAsAdmin(appDir string, command string) {
	cmd := exec.Command(
		fmt.Sprintf(
			"powershell -Command %s",
			fmt.Sprintf("Start-Process powershell -WindowStyle Hidden -ArgumentList \"-NoProfile;  %s\" -Verb RunAs", command),
		),
	)
	cmd.Dir = appDir

	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Infof("[ERROR] Error running admin command: %s", err)
	} else {
		sc := bufio.NewScanner(strings.NewReader(string(output)))
		for sc.Scan() {
			message := sc.Text()
			log.Infof("%s", message)
		}
	}
}

var (
	netAdaptersClassGuid = "{4D36E972-E325-11CE-BFC1-08002BE10318}"
	netAdaptersKey       = fmt.Sprintf("HKEY_LOCAL_MACHINE\\SYSTEM\\CurrentControlSet\\Control\\Class\\%s", netAdaptersClassGuid)
	netConfigKey         = fmt.Sprintf("HKEY_LOCAL_MACHINE\\SYSTEM\\CurrentControlSet\\Control\\Network\\%s", netAdaptersClassGuid)
)

func findTapDeviceName() *string {
	findCommand := fmt.Sprintf("reg query %s /s /f \"tap0901\" /e /d", netAdaptersKey)
	findCommandResult := executeCommandForFind(findCommand)

	re := regexp.MustCompile(`HKEY.*\\\\\\d{4}\$`)
	adapters := []string{}
	sc := bufio.NewScanner(strings.NewReader(findCommandResult))
	for sc.Scan() {
		line := sc.Text()
		if re.MatchString(line) {
			adapters = append(adapters, line)
		}
	}

	if len(adapters) == 0 {
		log.Infof("Can't find TAP device register")

		return nil
	}

	var latestTimestamp string = "0"
	var adapterName *string = nil

	for _, adapterKey := range adapters {
		netConfigId := queryRegistryValue(adapterKey, "NetCfgInstanceId", false)
		if netConfigId == nil {
			log.Infof("Can't find NetCfgInstanceId for %s.", adapterKey)
			continue
		}

		installTimestamp := queryRegistryValue(adapterKey, "InstallTimeStamp", false)
		if installTimestamp == nil {
			log.Infof("Can't find InstallTimeStamp for %s.", adapterKey)
			continue
		}

		nameKey := fmt.Sprintf("%s\\$netConfigId\\Connection", netConfigKey)
		name := queryRegistryValue(nameKey, "Name", true)
		if name == nil {
			log.Infof("Adapter hasn't got name: %s.", adapterKey)
			continue
		}

		if *installTimestamp > latestTimestamp {
			latestTimestamp = *installTimestamp
			adapterName = name
		}
	}

	return adapterName
}

func queryRegistryValue(key string, valueName string, multipleTokens bool) *string {
	var result string

	command := fmt.Sprintf("reg query \"%s\" /v \"%s\"", key, valueName)
	output := executeCommandForFind(command)

	if strings.TrimSpace(output) == "" {
		log.Infof("Key \"%s\" isn't find or empty", key)
		return nil
	}

	var line *string = nil
	sc := bufio.NewScanner(strings.NewReader(output))
	for sc.Scan() {
		l := sc.Text()
		if strings.Contains(l, valueName) {
			line = &l
			break
		}
	}

	re := regexp.MustCompile(`\\s+`)
	tokens := re.Split(*line, -1)

	if multipleTokens {
		result = strings.Join(tokens[3:], " ")

		return &result
	} else {
		if len(tokens) >= 4 {
			result = tokens[3]
			return &result
		} else {
			return nil
		}
	}
}

func executeCommandForFind(command string) string {
	// Use cmd.exe /c to properly handle paths with spaces
	cmd := exec.Command("cmd.exe", "/c", command)

	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Infof("[ERROR] Error running command %s: %s", command, err)

		return ""
	} else {
		return string(output)
	}
}

func AddTapDevice(appDir string) {
	var cmd *exec.Cmd

	updatePath()

	// Checking if a TAP device exists
	cmd = exec.Command(fmt.Sprintf("netsh interface show interface name=%s", deviceName))

	if cmd.Run() == nil {
		log.Infof("TAP network device already exists.")

		configureTapDevice(deviceName)
		return
	}

	log.Infof("Creating TAP network device...")

	runAsAdmin(appDir, fmt.Sprintf("%s install %s %s", tapInstallPath, oemVistaPath, deviceHwid))

	// Find new TAP device name (we should change it to outline-tap0)
	tapName := findTapDeviceName()
	if tapName == nil || *tapName == "" {
		log.Infof("Could not find TAP device name.")
		return
	}

	log.Infof("Found TAP device name: %s", *tapName)

	// Rename TAP device
	cmd = exec.Command(fmt.Sprintf("netsh interface set interface name=\"%s\" newname=\"%s\"", *tapName, deviceName))
	if cmd.Run() != nil {
		log.Infof("Could not rename TAP device.")
		return
	}

	configureTapDevice(deviceName)
}
