//go:build android

package dobbyvpn

/*
#include <jni.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <pthread.h>

static JavaVM *dobby_vm;
static jobject dobby_context;
static jclass dobby_bridge;
static char *dobby_jstring_utf8(JNIEnv *, jstring);
// The native activity can refresh its context while the snapshot poller is
// making a JNI call. Keep global-reference replacement and use under one
// lock; a deleted global reference must never be passed to Java.
static pthread_mutex_t dobby_bridge_lock = PTHREAD_MUTEX_INITIALIZER;

static JNIEnv *dobby_env(bool *attached) {
	pthread_mutex_lock(&dobby_bridge_lock);
	JavaVM *vm = dobby_vm;
	pthread_mutex_unlock(&dobby_bridge_lock);
	if (vm == NULL) return NULL;
	JNIEnv *env = NULL;
	jint status = (*vm)->GetEnv(vm, (void **)&env, JNI_VERSION_1_6);
	if (status == JNI_OK) return env;
	if (status != JNI_EDETACHED) return NULL;
	if ((*vm)->AttachCurrentThread(vm, (void **)&env, NULL) != JNI_OK) return NULL;
	if (attached != NULL) *attached = true;
	return env;
}

static void dobby_detach(bool attached) {
	if (!attached) return;
	pthread_mutex_lock(&dobby_bridge_lock);
	JavaVM *vm = dobby_vm;
	pthread_mutex_unlock(&dobby_bridge_lock);
	if (vm != NULL) (*vm)->DetachCurrentThread(vm);
}

static void dobby_clear_exception(JNIEnv *env) {
	if (env != NULL && (*env)->ExceptionCheck(env)) (*env)->ExceptionClear(env);
}

// FindClass on a thread attached from Go uses the system class loader on
// Android and may not see application classes. Resolve the bridge through the
// Context's loader instead, then retain only the global reference.
static jclass dobby_load_bridge(JNIEnv *env, jobject context) {
	if (env == NULL || context == NULL) return NULL;
	jclass contextClass = (*env)->GetObjectClass(env, context);
	if (contextClass == NULL) { dobby_clear_exception(env); return NULL; }
	jmethodID getClass = (*env)->GetMethodID(env, contextClass, "getClass", "()Ljava/lang/Class;");
	jobject runtimeClass = getClass == NULL ? NULL : (*env)->CallObjectMethod(env, context, getClass);
	if (runtimeClass == NULL) {
		dobby_clear_exception(env);
		(*env)->DeleteLocalRef(env, contextClass);
		return NULL;
	}
	jclass classClass = (*env)->GetObjectClass(env, runtimeClass);
	jmethodID getLoader = classClass == NULL ? NULL : (*env)->GetMethodID(
		env, classClass, "getClassLoader", "()Ljava/lang/ClassLoader;"
	);
	jobject loader = getLoader == NULL ? NULL : (*env)->CallObjectMethod(env, runtimeClass, getLoader);
	jclass bridge = NULL;
	if (loader != NULL) {
		jclass loaderClass = (*env)->GetObjectClass(env, loader);
		jmethodID loadClass = loaderClass == NULL ? NULL : (*env)->GetMethodID(
			env, loaderClass, "loadClass", "(Ljava/lang/String;)Ljava/lang/Class;"
		);
		jstring name = (*env)->NewStringUTF(env, "com.dobby.nativebridge.NativeVpnBridge");
		if (loadClass != NULL && name != NULL) {
			bridge = (jclass)(*env)->CallObjectMethod(env, loader, loadClass, name);
		}
		if (name != NULL) (*env)->DeleteLocalRef(env, name);
		if (loaderClass != NULL) (*env)->DeleteLocalRef(env, loaderClass);
	}
	if (bridge == NULL) {
		// The direct lookup still works when this function is reached through a
		// Java native method, and is a useful fallback for unusual class loaders.
		dobby_clear_exception(env);
		bridge = (*env)->FindClass(env, "com/dobby/nativebridge/NativeVpnBridge");
	}
	jclass global = NULL;
	if (bridge != NULL) {
		global = (jclass)(*env)->NewGlobalRef(env, bridge);
		(*env)->DeleteLocalRef(env, bridge);
	}
	if (loader != NULL) (*env)->DeleteLocalRef(env, loader);
	if (classClass != NULL) (*env)->DeleteLocalRef(env, classClass);
	(*env)->DeleteLocalRef(env, runtimeClass);
	(*env)->DeleteLocalRef(env, contextClass);
	dobby_clear_exception(env);
	return global;
}

static int dobby_call_prepare(void) {
	bool attached = false;
	JNIEnv *env = dobby_env(&attached);
	if (env == NULL) { dobby_detach(attached); return -1; }
	pthread_mutex_lock(&dobby_bridge_lock);
	if (dobby_bridge == NULL || dobby_context == NULL) {
		pthread_mutex_unlock(&dobby_bridge_lock);
		dobby_detach(attached);
		return -1;
	}
	jmethodID method = (*env)->GetStaticMethodID(env, dobby_bridge, "prepare", "(Landroid/content/Context;)I");
	if (method == NULL) {
		dobby_clear_exception(env);
		pthread_mutex_unlock(&dobby_bridge_lock);
		dobby_detach(attached);
		return -1;
	}
	jint result = (*env)->CallStaticIntMethod(env, dobby_bridge, method, dobby_context);
	if ((*env)->ExceptionCheck(env)) { (*env)->ExceptionDescribe(env); (*env)->ExceptionClear(env); result = -1; }
	pthread_mutex_unlock(&dobby_bridge_lock);
	dobby_detach(attached);
	return (int)result;
}

static int32_t dobby_call_acquire(const char *session, int64_t generation) {
	bool attached = false; JNIEnv *env = dobby_env(&attached);
	if (env == NULL) { dobby_detach(attached); return -1; }
	pthread_mutex_lock(&dobby_bridge_lock);
	if (dobby_bridge == NULL) {
		pthread_mutex_unlock(&dobby_bridge_lock);
		dobby_detach(attached);
		return -1;
	}
	jmethodID method = (*env)->GetStaticMethodID(env, dobby_bridge, "acquireTunnel", "(Ljava/lang/String;J)I");
	if (method == NULL) {
		dobby_clear_exception(env);
		pthread_mutex_unlock(&dobby_bridge_lock);
		dobby_detach(attached);
		return -1;
	}
	jstring id = (*env)->NewStringUTF(env, session == NULL ? "" : session);
	if (id == NULL) {
		dobby_clear_exception(env);
		pthread_mutex_unlock(&dobby_bridge_lock);
		dobby_detach(attached);
		return -1;
	}
	jint result = (*env)->CallStaticIntMethod(env, dobby_bridge, method, id, (jlong)generation);
	(*env)->DeleteLocalRef(env, id);
	dobby_clear_exception(env);
	pthread_mutex_unlock(&dobby_bridge_lock);
	dobby_detach(attached);
	return (int32_t)result;
}

static bool dobby_call_release(const char *session, int64_t generation, int32_t fd) {
	bool attached = false; JNIEnv *env = dobby_env(&attached);
	if (env == NULL) { dobby_detach(attached); return false; }
	pthread_mutex_lock(&dobby_bridge_lock);
	if (dobby_bridge == NULL) {
		pthread_mutex_unlock(&dobby_bridge_lock);
		dobby_detach(attached);
		return false;
	}
	jmethodID method = (*env)->GetStaticMethodID(env, dobby_bridge, "releaseTunnel", "(Ljava/lang/String;JI)Z");
	if (method == NULL) {
		dobby_clear_exception(env);
		pthread_mutex_unlock(&dobby_bridge_lock);
		dobby_detach(attached);
		return false;
	}
	jstring id = (*env)->NewStringUTF(env, session == NULL ? "" : session);
	if (id == NULL) {
		dobby_clear_exception(env);
		pthread_mutex_unlock(&dobby_bridge_lock);
		dobby_detach(attached);
		return false;
	}
	jboolean result = (*env)->CallStaticBooleanMethod(env, dobby_bridge, method, id, (jlong)generation, (jint)fd);
	(*env)->DeleteLocalRef(env, id);
	dobby_clear_exception(env);
	pthread_mutex_unlock(&dobby_bridge_lock);
	dobby_detach(attached);
	return result == JNI_TRUE;
}

static bool dobby_call_protect(const char *session, int64_t generation, int32_t fd) {
	bool attached = false; JNIEnv *env = dobby_env(&attached);
	if (env == NULL) { dobby_detach(attached); return false; }
	pthread_mutex_lock(&dobby_bridge_lock);
	if (dobby_bridge == NULL) {
		pthread_mutex_unlock(&dobby_bridge_lock);
		dobby_detach(attached);
		return false;
	}
	jmethodID method = (*env)->GetStaticMethodID(env, dobby_bridge, "protectSocket", "(Ljava/lang/String;JI)Z");
	if (method == NULL) {
		dobby_clear_exception(env);
		pthread_mutex_unlock(&dobby_bridge_lock);
		dobby_detach(attached);
		return false;
	}
	jstring id = (*env)->NewStringUTF(env, session == NULL ? "" : session);
	if (id == NULL) {
		dobby_clear_exception(env);
		pthread_mutex_unlock(&dobby_bridge_lock);
		dobby_detach(attached);
		return false;
	}
	jboolean result = (*env)->CallStaticBooleanMethod(env, dobby_bridge, method, id, (jlong)generation, (jint)fd);
	(*env)->DeleteLocalRef(env, id);
	dobby_clear_exception(env);
	pthread_mutex_unlock(&dobby_bridge_lock);
	dobby_detach(attached);
	return result == JNI_TRUE;
}

static void dobby_call_publish(const char *session, int64_t generation, const char *state, const char *failure) {
	bool attached = false; JNIEnv *env = dobby_env(&attached);
	if (env == NULL) { dobby_detach(attached); return; }
	pthread_mutex_lock(&dobby_bridge_lock);
	if (dobby_bridge == NULL) {
		pthread_mutex_unlock(&dobby_bridge_lock);
		dobby_detach(attached);
		return;
	}
	jmethodID method = (*env)->GetStaticMethodID(env, dobby_bridge, "publishState", "(Ljava/lang/String;JLjava/lang/String;Ljava/lang/String;)V");
	if (method == NULL) {
		dobby_clear_exception(env);
		pthread_mutex_unlock(&dobby_bridge_lock);
		dobby_detach(attached);
		return;
	}
	jstring id = (*env)->NewStringUTF(env, session == NULL ? "" : session);
	jstring stateValue = (*env)->NewStringUTF(env, state == NULL ? "" : state);
	jstring failureValue = (*env)->NewStringUTF(env, failure == NULL ? "" : failure);
	if (id == NULL || stateValue == NULL || failureValue == NULL) {
		if (id != NULL) (*env)->DeleteLocalRef(env, id);
		if (stateValue != NULL) (*env)->DeleteLocalRef(env, stateValue);
		if (failureValue != NULL) (*env)->DeleteLocalRef(env, failureValue);
		dobby_clear_exception(env);
		pthread_mutex_unlock(&dobby_bridge_lock);
		dobby_detach(attached);
		return;
	}
	(*env)->CallStaticVoidMethod(env, dobby_bridge, method, id, (jlong)generation, stateValue, failureValue);
	(*env)->DeleteLocalRef(env, id);
	(*env)->DeleteLocalRef(env, stateValue);
	(*env)->DeleteLocalRef(env, failureValue);
	dobby_clear_exception(env);
	pthread_mutex_unlock(&dobby_bridge_lock);
	dobby_detach(attached);
}

static bool dobby_call_export_logs(const unsigned char *logs, int length) {
	bool attached = false; JNIEnv *env = dobby_env(&attached);
	if (env == NULL || length < 0 || (length > 0 && logs == NULL)) {
		dobby_detach(attached); return false;
	}
	pthread_mutex_lock(&dobby_bridge_lock);
	if (dobby_bridge == NULL || dobby_context == NULL) {
		pthread_mutex_unlock(&dobby_bridge_lock);
		dobby_detach(attached);
		return false;
	}
	jmethodID method = (*env)->GetStaticMethodID(
		env, dobby_bridge, "exportLogs", "(Landroid/content/Context;[B)Z"
	);
	if (method == NULL) {
		dobby_clear_exception(env);
		pthread_mutex_unlock(&dobby_bridge_lock);
		dobby_detach(attached);
		return false;
	}
	jbyteArray payload = (*env)->NewByteArray(env, (jsize)length);
	if (payload == NULL) {
		dobby_clear_exception(env);
		pthread_mutex_unlock(&dobby_bridge_lock);
		dobby_detach(attached);
		return false;
	}
	if (length > 0) {
		(*env)->SetByteArrayRegion(env, payload, 0, (jsize)length, (const jbyte *)logs);
		if ((*env)->ExceptionCheck(env)) {
			(*env)->ExceptionClear(env); (*env)->DeleteLocalRef(env, payload);
			pthread_mutex_unlock(&dobby_bridge_lock);
			dobby_detach(attached); return false;
		}
	}
	jboolean result = (*env)->CallStaticBooleanMethod(env, dobby_bridge, method, dobby_context, payload);
	(*env)->DeleteLocalRef(env, payload);
	if ((*env)->ExceptionCheck(env)) {
		(*env)->ExceptionDescribe(env); (*env)->ExceptionClear(env); result = JNI_FALSE;
	}
	pthread_mutex_unlock(&dobby_bridge_lock);
	dobby_detach(attached);
	return result == JNI_TRUE;
}

// Return only paths selected by the Android shell. The Go backend treats this as a
// fixed native contract and validates the returned directory before opening
// any file; it never accepts a path from a profile or test command.
static char *dobby_call_diagnostic_paths(void) {
	bool attached = false; JNIEnv *env = dobby_env(&attached);
	if (env == NULL) { dobby_detach(attached); return NULL; }
	pthread_mutex_lock(&dobby_bridge_lock);
	if (dobby_bridge == NULL || dobby_context == NULL) {
		pthread_mutex_unlock(&dobby_bridge_lock);
		dobby_detach(attached);
		return NULL;
	}
	jmethodID method = (*env)->GetStaticMethodID(
		env, dobby_bridge, "diagnosticPaths", "(Landroid/content/Context;)Ljava/lang/String;"
	);
	if (method == NULL) {
		dobby_clear_exception(env);
		pthread_mutex_unlock(&dobby_bridge_lock);
		dobby_detach(attached);
		return NULL;
	}
	jobject value = (*env)->CallStaticObjectMethod(env, dobby_bridge, method, dobby_context);
	if ((*env)->ExceptionCheck(env)) {
		(*env)->ExceptionClear(env);
		value = NULL;
	}
	char *copy = dobby_jstring_utf8(env, (jstring)value);
	if (value != NULL) (*env)->DeleteLocalRef(env, value);
	pthread_mutex_unlock(&dobby_bridge_lock);
	dobby_detach(attached);
	return copy;
}

static char *dobby_call_load_source_url(void) {
	bool attached = false; JNIEnv *env = dobby_env(&attached);
	if (env == NULL) { dobby_detach(attached); return NULL; }
	pthread_mutex_lock(&dobby_bridge_lock);
	if (dobby_bridge == NULL || dobby_context == NULL) {
		pthread_mutex_unlock(&dobby_bridge_lock);
		dobby_detach(attached);
		return NULL;
	}
	jmethodID method = (*env)->GetStaticMethodID(env, dobby_bridge, "loadSourceURL", "(Landroid/content/Context;)Ljava/lang/String;");
	if (method == NULL) {
		dobby_clear_exception(env);
		pthread_mutex_unlock(&dobby_bridge_lock);
		dobby_detach(attached);
		return NULL;
	}
	jobject value = (*env)->CallStaticObjectMethod(env, dobby_bridge, method, dobby_context);
	if ((*env)->ExceptionCheck(env)) { (*env)->ExceptionClear(env); value = NULL; }
	char *copy = dobby_jstring_utf8(env, (jstring)value);
	if (value != NULL) (*env)->DeleteLocalRef(env, value);
	pthread_mutex_unlock(&dobby_bridge_lock);
	dobby_detach(attached);
	return copy;
}

static bool dobby_call_save_source_url(const char *source) {
	bool attached = false; JNIEnv *env = dobby_env(&attached);
	if (env == NULL || source == NULL) { dobby_detach(attached); return false; }
	pthread_mutex_lock(&dobby_bridge_lock);
	if (dobby_bridge == NULL || dobby_context == NULL) {
		pthread_mutex_unlock(&dobby_bridge_lock);
		dobby_detach(attached);
		return false;
	}
	jmethodID method = (*env)->GetStaticMethodID(env, dobby_bridge, "saveSourceURL", "(Landroid/content/Context;Ljava/lang/String;)Z");
	jstring value = (*env)->NewStringUTF(env, source);
	if (method == NULL || value == NULL) {
		dobby_clear_exception(env);
		if (value != NULL) (*env)->DeleteLocalRef(env, value);
		pthread_mutex_unlock(&dobby_bridge_lock);
		dobby_detach(attached);
		return false;
	}
	jboolean result = (*env)->CallStaticBooleanMethod(env, dobby_bridge, method, dobby_context, value);
	(*env)->DeleteLocalRef(env, value);
	if ((*env)->ExceptionCheck(env)) { (*env)->ExceptionClear(env); result = JNI_FALSE; }
	pthread_mutex_unlock(&dobby_bridge_lock);
	dobby_detach(attached);
	return result == JNI_TRUE;
}

static bool dobby_call_clear_source_url(void) {
	bool attached = false; JNIEnv *env = dobby_env(&attached);
	if (env == NULL) { dobby_detach(attached); return false; }
	pthread_mutex_lock(&dobby_bridge_lock);
	if (dobby_bridge == NULL || dobby_context == NULL) {
		pthread_mutex_unlock(&dobby_bridge_lock);
		dobby_detach(attached);
		return false;
	}
	jmethodID method = (*env)->GetStaticMethodID(env, dobby_bridge, "clearSourceURL", "(Landroid/content/Context;)Z");
	if (method == NULL) {
		dobby_clear_exception(env);
		pthread_mutex_unlock(&dobby_bridge_lock);
		dobby_detach(attached);
		return false;
	}
	jboolean result = (*env)->CallStaticBooleanMethod(env, dobby_bridge, method, dobby_context);
	if ((*env)->ExceptionCheck(env)) { (*env)->ExceptionClear(env); result = JNI_FALSE; }
	pthread_mutex_unlock(&dobby_bridge_lock);
	dobby_detach(attached);
	return result == JNI_TRUE;
}

static void dobby_set_android_context(uintptr_t vm, uintptr_t envPointer, uintptr_t context) {
	JNIEnv *env = (JNIEnv *)envPointer;
	if (vm == 0 || env == NULL || context == 0) return;
	pthread_mutex_lock(&dobby_bridge_lock);
	dobby_vm = (JavaVM *)vm;
	if (dobby_context != NULL) (*env)->DeleteGlobalRef(env, dobby_context);
	dobby_context = (*env)->NewGlobalRef(env, (jobject)context);
	if (dobby_context == NULL) dobby_clear_exception(env);
	if (dobby_bridge == NULL) {
		dobby_bridge = dobby_load_bridge(env, (jobject)context);
	}
	pthread_mutex_unlock(&dobby_bridge_lock);
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

func (jniPlatformCallbacks) LoadSourceURL() string {
	value := C.dobby_call_load_source_url()
	if value == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(value))
	return C.GoString(value)
}

func (jniPlatformCallbacks) SaveSourceURL(source string) bool {
	value := C.CString(source)
	defer C.free(unsafe.Pointer(value))
	return bool(C.dobby_call_save_source_url(value))
}

func (jniPlatformCallbacks) ClearSourceURL() bool {
	return bool(C.dobby_call_clear_source_url())
}

func setAndroidContext(vm, env, context uintptr) {
	C.dobby_set_android_context(C.uintptr_t(vm), C.uintptr_t(env), C.uintptr_t(context))
}

func prepareAndroidService() int { return int(C.dobby_call_prepare()) }

func exportAndroidLogs(raw []byte) bool {
	if len(raw) == 0 {
		return bool(C.dobby_call_export_logs(nil, 0))
	}
	return bool(C.dobby_call_export_logs(
		(*C.uchar)(unsafe.Pointer(&raw[0])), C.int(len(raw)),
	))
}

func androidDiagnosticPaths() string {
	value := C.dobby_call_diagnostic_paths()
	if value == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(value))
	return C.GoString(value)
}

// installJNIPlatform is called once from the package initializer. The Java
// bridge is only a callback transport; it never receives configuration bytes.
func installJNIPlatform(binding *mobilebinding.Binding) {
	binding.SetPlatformCallbacks(jniPlatformCallbacks{})
}

// The instrumentation APK uses the same narrow binding as the production
// Android activity. These JNI entry points are intentionally data-only wrappers;
// they do not expose the manager or native descriptors to Java. The explicit
// diagnostic export is the only non-session payload crossing this boundary.

//export Java_com_dobby_nativebridge_NativeGoSession_attach
func Java_com_dobby_nativebridge_NativeGoSession_attach(env *C.JNIEnv, _ C.jclass, context C.jobject) {
	if env == nil || unsafe.Pointer(context) == nil {
		return
	}
	setAndroidContext(uintptr(C.dobby_vm_for_env(env)), uintptr(unsafe.Pointer(env)), uintptr(unsafe.Pointer(context)))
	mobileSessions.AttachSourceStore()
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
