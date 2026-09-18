//go:build android

package dobbyvpn

/*
#include <jni.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

static JavaVM *dobby_vm;
static jobject dobby_context;
static jclass dobby_bridge;

static JNIEnv *dobby_env(bool *attached) {
	if (dobby_vm == NULL) return NULL;
	JNIEnv *env = NULL;
	jint status = (*dobby_vm)->GetEnv(dobby_vm, (void **)&env, JNI_VERSION_1_6);
	if (status == JNI_OK) return env;
	if ((*dobby_vm)->AttachCurrentThread(dobby_vm, &env, NULL) != JNI_OK) return NULL;
	*attached = true;
	return env;
}

static void dobby_detach(bool attached) {
	if (attached && dobby_vm != NULL) (*dobby_vm)->DetachCurrentThread(dobby_vm);
}

static void dobby_clear_exception(JNIEnv *env) {
	if (env != NULL && (*env)->ExceptionCheck(env)) (*env)->ExceptionClear(env);
}

static int dobby_call_prepare(void) {
	bool attached = false;
	JNIEnv *env = dobby_env(&attached);
	if (env == NULL || dobby_bridge == NULL || dobby_context == NULL) { dobby_detach(attached); return -1; }
	jmethodID method = (*env)->GetStaticMethodID(env, dobby_bridge, "prepare", "(Landroid/content/Context;)I");
	if (method == NULL) { dobby_clear_exception(env); dobby_detach(attached); return -1; }
	jint result = (*env)->CallStaticIntMethod(env, dobby_bridge, method, dobby_context);
	if ((*env)->ExceptionCheck(env)) { (*env)->ExceptionDescribe(env); (*env)->ExceptionClear(env); result = -1; }
	dobby_detach(attached);
	return (int)result;
}

static int32_t dobby_call_acquire(const char *session, int64_t generation) {
	bool attached = false; JNIEnv *env = dobby_env(&attached);
	if (env == NULL || dobby_bridge == NULL) { dobby_detach(attached); return -1; }
	jmethodID method = (*env)->GetStaticMethodID(env, dobby_bridge, "acquireTunnel", "(Ljava/lang/String;J)I");
	if (method == NULL) { dobby_clear_exception(env); dobby_detach(attached); return -1; }
	jstring id = (*env)->NewStringUTF(env, session == NULL ? "" : session);
	jint result = (*env)->CallStaticIntMethod(env, dobby_bridge, method, id, (jlong)generation);
	(*env)->DeleteLocalRef(env, id); dobby_clear_exception(env); dobby_detach(attached); return (int32_t)result;
}

static bool dobby_call_release(const char *session, int64_t generation, int32_t fd) {
	bool attached = false; JNIEnv *env = dobby_env(&attached);
	if (env == NULL || dobby_bridge == NULL) { dobby_detach(attached); return false; }
	jmethodID method = (*env)->GetStaticMethodID(env, dobby_bridge, "releaseTunnel", "(Ljava/lang/String;JI)Z");
	if (method == NULL) { dobby_clear_exception(env); dobby_detach(attached); return false; }
	jstring id = (*env)->NewStringUTF(env, session == NULL ? "" : session);
	jboolean result = (*env)->CallStaticBooleanMethod(env, dobby_bridge, method, id, (jlong)generation, (jint)fd);
	(*env)->DeleteLocalRef(env, id); dobby_clear_exception(env); dobby_detach(attached); return result == JNI_TRUE;
}

static bool dobby_call_protect(const char *session, int64_t generation, int32_t fd) {
	bool attached = false; JNIEnv *env = dobby_env(&attached);
	if (env == NULL || dobby_bridge == NULL) { dobby_detach(attached); return false; }
	jmethodID method = (*env)->GetStaticMethodID(env, dobby_bridge, "protectSocket", "(Ljava/lang/String;JI)Z");
	if (method == NULL) { dobby_clear_exception(env); dobby_detach(attached); return false; }
	jstring id = (*env)->NewStringUTF(env, session == NULL ? "" : session);
	jboolean result = (*env)->CallStaticBooleanMethod(env, dobby_bridge, method, id, (jlong)generation, (jint)fd);
	(*env)->DeleteLocalRef(env, id); dobby_clear_exception(env); dobby_detach(attached); return result == JNI_TRUE;
}

static void dobby_call_publish(const char *session, int64_t generation, const char *state, const char *failure) {
	bool attached = false; JNIEnv *env = dobby_env(&attached);
	if (env == NULL || dobby_bridge == NULL) { dobby_detach(attached); return; }
	jmethodID method = (*env)->GetStaticMethodID(env, dobby_bridge, "publishState", "(Ljava/lang/String;JLjava/lang/String;Ljava/lang/String;)V");
	if (method == NULL) { dobby_clear_exception(env); dobby_detach(attached); return; }
	jstring id = (*env)->NewStringUTF(env, session == NULL ? "" : session);
	jstring stateValue = (*env)->NewStringUTF(env, state == NULL ? "" : state);
	jstring failureValue = (*env)->NewStringUTF(env, failure == NULL ? "" : failure);
	(*env)->CallStaticVoidMethod(env, dobby_bridge, method, id, (jlong)generation, stateValue, failureValue);
	(*env)->DeleteLocalRef(env, id); (*env)->DeleteLocalRef(env, stateValue); (*env)->DeleteLocalRef(env, failureValue);
	dobby_clear_exception(env); dobby_detach(attached);
}

static void dobby_set_android_context(uintptr_t vm, uintptr_t envPointer, uintptr_t context) {
	dobby_vm = (JavaVM *)vm;
	JNIEnv *env = (JNIEnv *)envPointer;
	if (env == NULL || context == 0) return;
	if (dobby_context != NULL) (*env)->DeleteGlobalRef(env, dobby_context);
	dobby_context = (*env)->NewGlobalRef(env, (jobject)context);
	if (dobby_bridge == NULL) {
		jclass local = (*env)->FindClass(env, "com/dobby/nativebridge/NativeVpnBridge");
		if (local != NULL) dobby_bridge = (*env)->NewGlobalRef(env, local);
		dobby_clear_exception(env);
	}
}

static uintptr_t dobby_vm_for_env(JNIEnv *env) {
	JavaVM *vm = NULL;
	if (env == NULL || (*env)->GetJavaVM(env, &vm) != JNI_OK || vm == NULL) return 0;
	return (uintptr_t)vm;
}

static char *dobby_jstring_utf8(JNIEnv *env, jstring value) {
	if (env == NULL || value == NULL) return NULL;
	const char *raw = (*env)->GetStringUTFChars(env, value, NULL);
	if (raw == NULL) return NULL;
	size_t length = strlen(raw);
	char *copy = (char *)malloc(length + 1);
	if (copy != NULL) memcpy(copy, raw, length + 1);
	(*env)->ReleaseStringUTFChars(env, value, raw);
	return copy;
}

static unsigned char *dobby_jbytearray_copy(JNIEnv *env, jbyteArray value, jint *length) {
	if (length != NULL) *length = 0;
	if (env == NULL || value == NULL) return NULL;
	jsize size = (*env)->GetArrayLength(env, value);
	if (size < 0) return NULL;
	unsigned char *copy = NULL;
	if (size > 0) {
		copy = (unsigned char *)malloc((size_t)size);
		if (copy == NULL) return NULL;
		(*env)->GetByteArrayRegion(env, value, 0, size, (jbyte *)copy);
		if ((*env)->ExceptionCheck(env)) {
			(*env)->ExceptionClear(env);
			free(copy);
			return NULL;
		}
	}
	if (length != NULL) *length = (jint)size;
	return copy;
}

static jstring dobby_new_jstring(JNIEnv *env, const char *value) {
	if (env == NULL || value == NULL) return NULL;
	return (*env)->NewStringUTF(env, value);
}
*/
import "C"

import (
	"unsafe"

	"go_module/sessionapi/mobilebinding"
)

type jniPlatformCallbacks struct{}

func (jniPlatformCallbacks) AcquireTunnel(sessionID string, generation int64) int32 {
	value := C.CString(sessionID)
	defer C.free(unsafe.Pointer(value))
	return int32(C.dobby_call_acquire(value, C.int64_t(generation)))
}

func (jniPlatformCallbacks) ReleaseTunnel(sessionID string, generation int64, fd int32) bool {
	value := C.CString(sessionID)
	defer C.free(unsafe.Pointer(value))
	return bool(C.dobby_call_release(value, C.int64_t(generation), C.int32_t(fd)))
}

func (jniPlatformCallbacks) ProtectSocket(sessionID string, generation int64, fd int32) bool {
	value := C.CString(sessionID)
	defer C.free(unsafe.Pointer(value))
	return bool(C.dobby_call_protect(value, C.int64_t(generation), C.int32_t(fd)))
}

func (jniPlatformCallbacks) PublishState(sessionID string, generation int64, state string, failureCode string) {
	id := C.CString(sessionID)
	stateValue := C.CString(state)
	failure := C.CString(failureCode)
	defer C.free(unsafe.Pointer(id))
	defer C.free(unsafe.Pointer(stateValue))
	defer C.free(unsafe.Pointer(failure))
	C.dobby_call_publish(id, C.int64_t(generation), stateValue, failure)
}

func setAndroidContext(vm, env, context uintptr) {
	C.dobby_set_android_context(C.uintptr_t(vm), C.uintptr_t(env), C.uintptr_t(context))
}

func prepareAndroidService() int { return int(C.dobby_call_prepare()) }

// installJNIPlatform is called once from the package initializer. The Java
// bridge is only a callback transport; it never receives configuration bytes.
func installJNIPlatform(binding *mobilebinding.Binding) {
	binding.SetPlatformCallbacks(jniPlatformCallbacks{})
}

// The instrumentation APK uses the same narrow binding as the production
// Fyne activity. These JNI entry points are intentionally data-only wrappers;
// they do not expose the manager or native descriptors to Java.

//export Java_com_dobby_nativebridge_NativeGoSession_attach
func Java_com_dobby_nativebridge_NativeGoSession_attach(env *C.JNIEnv, _ C.jclass, context C.jobject) {
	if env == nil || unsafe.Pointer(context) == nil {
		return
	}
	setAndroidContext(uintptr(C.dobby_vm_for_env(env)), uintptr(unsafe.Pointer(env)), uintptr(unsafe.Pointer(context)))
}

func jniString(env *C.JNIEnv, value C.jstring) string {
	copy := C.dobby_jstring_utf8(env, value)
	if copy == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(copy))
	return C.GoString(copy)
}

func jniBytes(env *C.JNIEnv, value C.jbyteArray) []byte {
	var length C.jint
	copy := C.dobby_jbytearray_copy(env, value, &length)
	if copy == nil || length <= 0 {
		if copy != nil {
			C.free(unsafe.Pointer(copy))
		}
		return nil
	}
	defer C.free(unsafe.Pointer(copy))
	return C.GoBytes(unsafe.Pointer(copy), C.int(length))
}

func jniResult(env *C.JNIEnv, value string) C.jstring {
	copy := C.CString(value)
	defer C.free(unsafe.Pointer(copy))
	return C.dobby_new_jstring(env, copy)
}

//export Java_com_dobby_nativebridge_NativeGoSession_configure
func Java_com_dobby_nativebridge_NativeGoSession_configure(
	env *C.JNIEnv, _ C.jclass, session C.jstring, sequence C.jlong, raw C.jbyteArray,
) C.jstring {
	return jniResult(env, mobileSessions.Configure(jniString(env, session), int64(sequence), jniBytes(env, raw)))
}

//export Java_com_dobby_nativebridge_NativeGoSession_start
func Java_com_dobby_nativebridge_NativeGoSession_start(
	env *C.JNIEnv, _ C.jclass, session C.jstring, sequence C.jlong, mode C.jstring, index C.jint,
) C.jstring {
	return jniResult(env, mobileSessions.Start(jniString(env, session), int64(sequence), jniString(env, mode), int32(index)))
}

//export Java_com_dobby_nativebridge_NativeGoSession_stop
func Java_com_dobby_nativebridge_NativeGoSession_stop(
	env *C.JNIEnv, _ C.jclass, session C.jstring, generation C.jlong,
) C.jstring {
	return jniResult(env, mobileSessions.Stop(jniString(env, session), int64(generation)))
}

//export Java_com_dobby_nativebridge_NativeGoSession_snapshot
func Java_com_dobby_nativebridge_NativeGoSession_snapshot(
	env *C.JNIEnv, _ C.jclass, session C.jstring,
) C.jstring {
	return jniResult(env, mobileSessions.Snapshot(jniString(env, session)))
}

//export Java_com_dobby_nativebridge_NativeGoSession_reset
func Java_com_dobby_nativebridge_NativeGoSession_reset(
	env *C.JNIEnv, _ C.jclass, session C.jstring, sequence C.jlong,
) C.jstring {
	return jniResult(env, mobileSessions.Reset(jniString(env, session), int64(sequence)))
}
