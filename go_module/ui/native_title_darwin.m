//go:build darwin && !ios && cgo

#import <AppKit/AppKit.h>

#include <stdint.h>

int dobby_set_macos_window_title(uintptr_t windowHandle, const char *title) {
    @autoreleasepool {
        if (![NSThread isMainThread]) {
            return 3;
        }
        NSWindow *window = (__bridge NSWindow *)(void *)windowHandle;
        if (window == nil || title == NULL) {
            return window == nil ? 1 : 2;
        }

        NSString *value = [NSString stringWithUTF8String:title];
        [window setTitle:value ?: @""];
        NSAccessibilityPostNotification(window, NSAccessibilityTitleChangedNotification);
        return 0;
    }
}
