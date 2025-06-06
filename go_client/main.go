package main

import "C"
import (
	"go_client/kotlin_exports"
	"unsafe"
)

var dev *kotlin_exports.OutlineDevice

//export NewOutlineDevice
func NewOutlineDevice(config *C.char) {
	goConfig := C.GoString(config)
	_dev, err := kotlin_exports.NewOutlineDevice(goConfig)
	if err != nil {
		return
	}
	dev = _dev
	return
}

//export Write
func Write(buf *C.char, length C.int) C.int {
	data := C.GoBytes(unsafe.Pointer(buf), length)
	n, err := dev.Write(data)
	if err != nil {
		return -1
	}
	return C.int(n)
}

//export Read
func Read(buf *C.char, maxLen C.int) C.int {
	data, err := dev.Read()
	if err != nil {
		return -1
	}

	copyLen := len(data)
	if copyLen > int(maxLen) {
		copyLen = int(maxLen)
	}
	C.memcpy(unsafe.Pointer(buf),
		unsafe.Pointer(&data[0]),
		C.size_t(copyLen))
	return C.int(copyLen)
}

func main() {}
