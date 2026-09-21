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
        NSString *expected = value ?: @"";
        [window setTitle:expected];
        // GLFW/AppKit updates the visible title, but AX clients may retain
        // the NSWindow accessibility-title value independently. Keep both
        // public AppKit attributes aligned before announcing the change.
        [window setAccessibilityTitle:expected];
        if (![[window title] isEqualToString:expected]) {
            return 4;
        }
        if (![[window accessibilityTitle] isEqualToString:expected]) {
            return 5;
        }
        NSAccessibilityPostNotification(window, NSAccessibilityTitleChangedNotification);
        return 0;
    }
}
