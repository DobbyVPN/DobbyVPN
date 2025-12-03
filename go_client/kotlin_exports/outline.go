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
    "unsafe"
    "go_client/outline"
)

var client *outline.OutlineClient
var logTag = C.CString("OutlineGo")

func log(msg string) {
    cmsg := C.CString(msg)
    C.goLog(logTag, cmsg)
    C.free(unsafe.Pointer(cmsg))
}

//export Connect
func Connect() {
    log("Connect() called")
    if client == nil {
        log("Connect() failed: client is nil")
        return
    }
    client.Connect()
    log("Connect() finished")
}

//export Disconnect
func Disconnect() {
    log("Disconnect() called")
    if client == nil {
        log("Disconnect(): client is nil")
        return
    }
    client.Disconnect()
    log("Disconnect() finished")
}

//export Read
func Read(buf *C.char, maxLen C.int) C.int {
    log("Read() called")
    if client == nil {
        log("Read(): client is nil")
        return -1
    }
    data, err := client.Read()
    if err != nil {
        log("Read() error: " + err.Error())
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
    log("Read() finished, bytesRead=" + string(rune(copyLen)))
    return C.int(copyLen)
}

//export Write
func Write(buf *C.char, length C.int) C.int {
    log("Write() called, length=" + string(rune(length)))
    if client == nil {
        log("Write(): client is nil")
        return -1
    }
    data := C.GoBytes(unsafe.Pointer(buf), length)
    n, err := client.Write(data)
    if err != nil {
        log("Write() error: " + err.Error())
        return -1
    }
    log("Write() finished, written=" + string(rune(n)))
    return C.int(n)
}

//export NewOutlineClient
func NewOutlineClient(config *C.char) {
    log("NewOutlineClient() called")
    StopOutlineClient()
    goConfig := C.GoString(config)
    log("Config length=" + string(rune(len(goConfig))))
    cl := outline.NewClient(goConfig)
    client = cl
    log("NewOutlineClient() finished")
}

//export StopOutlineClient
func StopOutlineClient() {
    log("StopOutlineClient() called")
    if client != nil {
        client.Disconnect()
        log("StopOutlineClient(): disconnected")
    } else {
        log("StopOutlineClient(): client is nil")
    }
}
