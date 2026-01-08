package main

/*
#include <stdlib.h>
#include <string.h>
*/
import "C"
import (
	log "go_client/logger"
	"go_client/outline"
	"sync"
	"unsafe"
)

var client *outline.OutlineClient
var lastError string
var errorMu sync.Mutex

//export GetLastError
func GetLastError() *C.char {
	errorMu.Lock()
	defer errorMu.Unlock()
	if lastError == "" {
		return nil
	}
	return C.CString(lastError)
}

//export ClearLastError
func ClearLastError() {
	errorMu.Lock()
	defer errorMu.Unlock()
	lastError = ""
}

func setLastError(err string) {
	errorMu.Lock()
	defer errorMu.Unlock()
	lastError = err
	log.Infof("Error set: %s", err)
}

//export Connect
func Connect() C.int {
	log.Infof("Connect() called")
	ClearLastError()
	if client == nil {
		setLastError("client is nil")
		log.Infof("Connect() failed: client is nil")
		return -1
	}
	err := client.Connect()
	if err != nil {
		setLastError(err.Error())
		log.Infof("Connect() failed: %v", err)
		return -1
	}
	log.Infof("Connect() finished successfully")
	return 0
}

//export Disconnect
func Disconnect() {
	log.Infof("Disconnect() called")
	if client == nil {
		log.Infof("Disconnect(): client is nil")
		return
	}
	client.Disconnect()
	log.Infof("Disconnect() finished")
}

//export Read
func Read(buf *C.char, maxLen C.int) C.int {
	//log.Infof("Read() called")
	if client == nil {
		log.Infof("Read(): client is nil")
		return -1
	}
	data, err := client.Read()
	if err != nil {
		log.Infof("Read() error: " + err.Error())
		return -1
	}
	copyLen := len(data)
	if copyLen > int(maxLen) {
		copyLen = int(maxLen)
	}
	C.memcpy(
		unsafe.Pointer(buf),
		unsafe.Pointer(&data[0]),
		C.size_t(copyLen),
	)
	//log.Infof("Read() finished, bytesRead=" + string(rune(copyLen)))
	return C.int(copyLen)
}

//export Write
func Write(buf *C.char, length C.int) C.int {
	//log.Infof("Write() called, length=" + string(rune(length)))
	if client == nil {
		log.Infof("Write(): client is nil")
		return -1
	}
	data := C.GoBytes(unsafe.Pointer(buf), length)
	n, err := client.Write(data)
	if err != nil {
		log.Infof("Write() error: " + err.Error())
		return -1
	}
	//log.Infof("Write() finished, written=" + string(rune(n)))
	return C.int(n)
}

//export NewOutlineClient
func NewOutlineClient(config *C.char) {
	log.Infof("NewOutlineClient() called")
	StopOutlineClient()
	goConfig := C.GoString(config)
	log.Infof("Config length=%d, config: %s", len(goConfig), goConfig)
	cl := outline.NewClient(goConfig)
	client = cl
	log.Infof("NewOutlineClient() finished")
}

//export StopOutlineClient
func StopOutlineClient() {
	log.Infof("StopOutlineClient() called")
	if client != nil {
		client.Disconnect()
		log.Infof("StopOutlineClient(): disconnected")
	} else {
		log.Infof("StopOutlineClient(): client is nil")
	}
}
