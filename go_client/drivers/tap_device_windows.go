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
	tapInstallPath = "tap-windows6\\tapinstall.exe"
	oemVistaPath   = "tap-windows6\\OemVista.inf"
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
	var cmdOuput string
	var err error

	log.Infof("Configuring TAP device subnet...")

	cmdOuput, err = executeCommandForFind(fmt.Sprintf("netsh interface ip set address %s static 10.0.85.2 255.255.255.255", deviceName))
	log.Infof("TAP network device subnet set output: %s.", cmdOuput)
	if err != nil {
		log.Infof("Could not set TAP network device subnet.")
		return
	}

	log.Infof("Configuring primary DNS...")

	cmdOuput, err = executeCommandForFind(fmt.Sprintf("netsh interface ip set dnsservers %s static address=1.1.1.1", deviceName))
	log.Infof("TAP device primary DNS config output: %s.", cmdOuput)
	if err != nil {
		log.Infof("Could not configure TAP device primary DNS.")
		return
	}

	log.Infof("Configuring secondary DNS...")

	cmdOuput, err = executeCommandForFind(fmt.Sprintf("netsh interface ip add dnsservers %s 9.9.9.9 index=2", deviceName))
	log.Infof("TAP device secondary DNS config output: %s.", cmdOuput)
	if err != nil {
		log.Infof("Could not configure TAP device secondary DNS.")
		return
	}
}

func runAsAdmin(command string) error {
	ex, _ := os.Executable()
	exDir := filepath.Dir(ex)
	log.Infof("Executable directory: %s", exDir)
	log.Infof("Running command: %s", command)

	cmd := exec.Command("cmd.exe", "/D", exDir, "/C", command)
	output, err := cmd.CombinedOutput()

	sc := bufio.NewScanner(strings.NewReader(string(output)))
	for sc.Scan() {
		message := sc.Text()
		log.Infof("%s", message)
	}

	if err != nil {
		return fmt.Errorf("Admin command \"%s\" error: %s", command, err)
	} else {
		return nil
	}
}

var (
	netAdaptersClassGuid = "{4D36E972-E325-11CE-BFC1-08002BE10318}"
	netAdaptersKey       = fmt.Sprintf("HKEY_LOCAL_MACHINE\\SYSTEM\\CurrentControlSet\\Control\\Class\\%s", netAdaptersClassGuid)
	netConfigKey         = fmt.Sprintf("HKEY_LOCAL_MACHINE\\SYSTEM\\CurrentControlSet\\Control\\Network\\%s", netAdaptersClassGuid)
)

func findTapDeviceName() (*string, error) {
	findCommand := fmt.Sprintf(`reg query %s /s /f tap0901 /e /d`, netAdaptersKey)
	findCommandResult, err := executeCommandForFind(findCommand)
	if err != nil {
		return nil, fmt.Errorf("Cannot run REG QUERY: %v", err)
	}

	re := regexp.MustCompile(`HKEY.*\d{4}$`)
	adapters := []string{}
	sc := bufio.NewScanner(strings.NewReader(findCommandResult))
	for sc.Scan() {
		line := sc.Text()
		if re.MatchString(line) {
			adapters = append(adapters, line)
		}
	}

	if len(adapters) == 0 {
		return nil, fmt.Errorf("Can't find TAP device register")
	}

	var latestTimestamp string = "0"
	var adapterName *string = nil

	for _, adapterKey := range adapters {
		netConfigId, err := queryRegistryValue(adapterKey, "NetCfgInstanceId", false)
		if err != nil {
			return nil, fmt.Errorf("Cannot query NetCfgInstanceId reg value: %v", err)
		}
		if netConfigId == nil {
			log.Infof("Can't find NetCfgInstanceId for %s.", adapterKey)
			continue
		}

		installTimestamp, err := queryRegistryValue(adapterKey, "InstallTimeStamp", false)
		if err != nil {
			return nil, fmt.Errorf("Cannot query InstallTimeStamp reg value: %v", err)
		}
		if installTimestamp == nil {
			log.Infof("Can't find InstallTimeStamp for %s.", adapterKey)
			continue
		}

		nameKey := fmt.Sprintf("%s\\%s\\Connection", netConfigKey, *netConfigId)
		name, err := queryRegistryValue(nameKey, "Name", true)
		if err != nil {
			return nil, fmt.Errorf("Cannot query %s reg value: %v", nameKey, err)
		}
		if name == nil {
			log.Infof("Adapter hasn't got name: %s.", adapterKey)
			continue
		}

		if *installTimestamp > latestTimestamp {
			latestTimestamp = *installTimestamp
			adapterName = name
		}
	}

	return adapterName, nil
}

func queryRegistryValue(key string, valueName string, multipleTokens bool) (*string, error) {
	var result string

	command := fmt.Sprintf(`reg query %s /v %s`, key, valueName)
	output, err := executeCommandForFind(command)
	if err != nil {
		return nil, fmt.Errorf("Error running REG QUERY: %s", err)
	}

	if strings.TrimSpace(output) == "" {
		return nil, fmt.Errorf("Key \"%s\" isn't find or empty", key)
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

	re := regexp.MustCompile(`\s+`)
	tokens := re.Split(*line, -1)

	if multipleTokens {
		result = strings.Join(tokens[3:], " ")

		return &result, nil
	} else {
		if len(tokens) >= 4 {
			result = tokens[3]
			return &result, nil
		} else {
			return nil, nil
		}
	}
}

func executeCommandForFind(command string) (string, error) {
	log.Infof("Running command: %s", command)
	cmd := exec.Command("cmd.exe", "/c", command)
	output, err := cmd.CombinedOutput()

	sc := bufio.NewScanner(strings.NewReader(string(output)))
	for sc.Scan() {
		message := sc.Text()
		log.Infof("%s", message)
	}

	if err != nil {
		return "", fmt.Errorf("Command \"%s\" error: %s", command, err)
	}

	return string(output), nil
}

func AddTapDevice(appDir string) {
	updatePath()

	// Checking if a TAP device exists
	findTapDeviceOutput, err := executeCommandForFind(fmt.Sprintf("netsh interface show interface name=%s", deviceName))
	log.Infof("TAP device existance check ouptut: %s", findTapDeviceOutput)

	if err == nil {
		log.Infof("TAP network device already exists.")
		configureTapDevice(deviceName)
		return
	}

	log.Infof("Creating TAP network device...")

	err = runAsAdmin(fmt.Sprintf(`%s install %s %s`, tapInstallPath, oemVistaPath, deviceHwid))
	if err != nil {
		log.Infof("[ERROR] Error during adding TAP device: %s", err)
		return
	}

	// Find new TAP device name (we should change it to outline-tap0)
	tapName, err := findTapDeviceName()
	if err != nil {
		log.Infof("[ERROR] Could not find TAP device name: %s", err)
		return
	}
	if tapName == nil || *tapName == "" {
		log.Infof("Could not find TAP device name.")
		return
	}

	log.Infof("Found TAP device name: %s", *tapName)

	// Rename TAP device
	tapDeviceRename, err := executeCommandForFind(fmt.Sprintf("netsh interface set interface name=\"%s\" newname=\"%s\"", *tapName, deviceName))
	log.Infof("TAP device rename ouptut: %s", tapDeviceRename)
	if err != nil {
		log.Infof("Could not rename TAP device: %s", err)
		return
	}

	configureTapDevice(deviceName)
}
