package main

/*
#include <stdlib.h>
#include <string.h>
*/
import "C"
import (
    "go_client/outline"
	"unsafe"
)

var client *outline.OutlineClient

//export Connect
func Connect() error {
	return client.Connect()
}

//export Disconnect
func Disconnect() error {
	return client.Disconnect()
}

//export Read
func Read(buf *C.char, maxLen C.int) C.int {
    if client == nil {
		return -1
	}
	data, err := client.Read()
	if err != nil {
		return -1
	}
	copyLen := len(data)
	if copyLen > int(maxLen) {
		copyLen = int(maxLen)
	}
	// memcpy из <string.h>
	C.memcpy(
		unsafe.Pointer(buf),
		unsafe.Pointer(&data[0]),
		C.size_t(copyLen),
	)
	return C.int(copyLen)
}

//export Write
func Write(buf *C.char, length C.int) C.int {
    if client == nil {
		return -1
	}
	data := C.GoBytes(unsafe.Pointer(buf), length)
	n, err := client.Write(data)
	if err != nil {
		return -1
	}
	return C.int(n)
}

//export NewOutlineClient
func NewOutlineClient(config *C.char) {
	goConfig := C.GoString(config)
	cl := outline.NewClient(goConfig)
	client = cl
}
