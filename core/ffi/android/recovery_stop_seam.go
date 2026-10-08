//go:build android && dobbyvpn_test_seams

package dobbyvpn

/*
#include <jni.h>
*/
import "C"

import testseam "core/sessionapi/runtime"

// These JNI entry points are present only in Android builds carrying the
// build-local test seam tag. The declarations live in androidTest, so the
// untagged app has no Java API for controlling recovery.
//
//export Java_com_dobby_nativebridge_NativeRecoveryStopTestSeam_enableNative
func Java_com_dobby_nativebridge_NativeRecoveryStopTestSeam_enableNative(
	_ *C.JNIEnv,
	_ C.jclass,
) C.jboolean {
	if testseam.EnableTestRecoveryStop() {
		return C.jboolean(1)
	}
	return C.jboolean(0)
}

//export Java_com_dobby_nativebridge_NativeRecoveryStopTestSeam_armNative
func Java_com_dobby_nativebridge_NativeRecoveryStopTestSeam_armNative(
	_ *C.JNIEnv,
	_ C.jclass,
) C.jboolean {
	if testseam.ArmTestRecoveryStop() {
		return C.jboolean(1)
	}
	return C.jboolean(0)
}
