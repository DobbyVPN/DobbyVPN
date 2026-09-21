//go:build darwin && !ios && cgo

#define GL_SILENCE_DEPRECATION

#import <AppKit/AppKit.h>
#import <objc/runtime.h>
#import <pthread.h>

typedef id (*DobbyNSGLInitializer)(id, SEL, const NSOpenGLPixelFormatAttribute*);

static DobbyNSGLInitializer dobby_original_nsgl_initializer = NULL;
static pthread_once_t dobby_nsgl_install_once = PTHREAD_ONCE_INIT;
static int dobby_nsgl_install_status = 1;

static id dobby_nsgl_initializer(
    id self,
    SEL selector,
    const NSOpenGLPixelFormatAttribute* attributes
) {
    id pixel_format = dobby_original_nsgl_initializer(self, selector, attributes);
    if (pixel_format != nil || attributes == NULL ||
        attributes[0] != NSOpenGLPFAAccelerated ||
        attributes[1] != NSOpenGLPFAClosestPolicy) {
        return pixel_format;
    }

    // GLFW's accelerated request is always the first Boolean attribute. The
    // remainder is already a valid, terminated AppKit list. Do not copy by
    // searching for zero: integer attributes may legitimately have value 0.
    return [[NSOpenGLPixelFormat alloc] initWithAttributes:attributes + 1];
}

static void dobby_install_nsgl_software_fallback_once(void) {
    Method method = class_getInstanceMethod(
        [NSOpenGLPixelFormat class],
        @selector(initWithAttributes:)
    );
    if (method == NULL) {
        return;
    }

    dobby_original_nsgl_initializer =
        (DobbyNSGLInitializer)method_getImplementation(method);
    if (dobby_original_nsgl_initializer == NULL) {
        return;
    }

    method_setImplementation(method, (IMP)dobby_nsgl_initializer);
    dobby_nsgl_install_status = 0;
}

int dobby_install_nsgl_software_fallback(void) {
    pthread_once(
        &dobby_nsgl_install_once,
        dobby_install_nsgl_software_fallback_once
    );
    return dobby_nsgl_install_status;
}
