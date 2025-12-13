package main

/*
#include <stdlib.h>
#include <string.h>
*/
import "C"
import (
	log "go_client/logger"
	"go_client/outline"
	"unsafe"
)

var client *outline.OutlineClient

//export Connect
func Connect() {
	log.Infof("Connect() called")
	if client == nil {
		log.Infof("Connect() failed: client is nil")
		return
	}
	err := client.Connect()
	if err != nil {
		log.Infof("Connect() error: " + err.Error())
		return
	}
	log.Infof("Connect() finished")
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
	log.Infof("Config length=" + string(rune(len(goConfig))))
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
