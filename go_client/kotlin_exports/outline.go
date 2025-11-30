package main

/*
#include <stdlib.h>
#include <string.h>
#include <android/log.h>

static void goLog(const char* tag, const char* msg) {
    __android_log_print(ANDROID_LOG_DEBUG, tag, "%s", msg);
}
*/
import "C"
import (
	"go_client/outline"
	"unsafe"
)

var client *outline.OutlineClient
var logTag = C.CString("OutlineGo")

//export Connect
func Connect() {
	if client == nil {
		return
	}
	client.Connect()
}

//export Disconnect
func Disconnect() {
	if client == nil {
		return
	}
	client.Disconnect()
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
	StopOutlineClient()
	goConfig := C.GoString(config)
	cl := outline.NewClient(goConfig)
	client = cl
}

//export StopOutlineClient
func StopOutlineClient() {
	if client != nil {
		client.Disconnect()
	} else {
	}
}
