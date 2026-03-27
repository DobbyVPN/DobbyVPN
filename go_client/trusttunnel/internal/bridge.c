#include "dobby_bridge.h"

#ifdef _WIN32
extern __declspec(dllexport) void go_state_changed(void* arg, int state);
extern __declspec(dllexport) void go_log_message(int level, const char* msg);
extern __declspec(dllexport) int go_protect_socket(int fd);
#else
extern void go_state_changed(void* arg, int state);
extern void go_log_message(int level, const char* msg);
extern int go_protect_socket(int fd);
#endif

void c_state_changed(void* arg, int state) {
    go_state_changed(arg, state);
}

void c_log_message(int level, const char* msg) {
    go_log_message(level, msg);
}

int c_protect_cb(int fd) {
	return go_protect_socket(fd);
}