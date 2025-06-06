package main

/*
#include <stdlib.h>
#include <string.h>
*/
import "C"
import (
	"go_client/kotlin_exports"
	"unsafe"
)

// хранит текущее устройство
var dev *kotlin_exports.OutlineDevice

//export NewOutlineDevice
func NewOutlineDevice(config *C.char) {
	goConfig := C.GoString(config)
	d, err := kotlin_exports.NewOutlineDevice(goConfig)
	if err != nil {
		// можно логировать ошибку, но возвращаем просто nil
		dev = nil
		return
	}
	dev = d
}

//export Write
func Write(buf *C.char, length C.int) C.int {
	if dev == nil {
		return -1
	}
	data := C.GoBytes(unsafe.Pointer(buf), length)
	n, err := dev.Write(data)
	if err != nil {
		return -1
	}
	return C.int(n)
}

//export Read
func Read(buf *C.char, maxLen C.int) C.int {
	if dev == nil {
		return -1
	}
	data, err := dev.Read()
	if err != nil {
		return -1
	}
	// сколько реально скопируем
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

func main() {}
