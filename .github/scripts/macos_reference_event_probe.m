#import <Cocoa/Cocoa.h>

@interface DobbyVPNReferenceProbeDelegate : NSObject <NSApplicationDelegate>
@property(nonatomic, strong) NSWindow *window;
@property(nonatomic, strong) NSButton *button;
@end

@implementation DobbyVPNReferenceProbeDelegate

- (void)applicationDidFinishLaunching:(NSNotification *)notification {
    (void)notification;
    NSScreen *screen = [NSScreen mainScreen];
    if (screen == nil) {
        fprintf(stderr, "no main screen\n");
        [NSApp terminate:nil];
        return;
    }

    NSRect screenFrame = [screen frame];
    NSRect windowFrame = NSMakeRect(120, 120, 420, 220);
    self.window = [[NSWindow alloc] initWithContentRect:windowFrame
        styleMask:NSWindowStyleMaskTitled
        backing:NSBackingStoreBuffered
        defer:NO];
    self.button = [[NSButton alloc] initWithFrame:NSMakeRect(95, 75, 230, 56)];
    [self.button setTitle:@"DobbyVPN native event probe"];
    [self.button setButtonType:NSButtonTypePushOnPushOff];
    [self.button setTarget:self];
    [self.button setAction:@selector(buttonClicked:)];
    [[self.window contentView] addSubview:self.button];
    [self.window makeKeyAndOrderFront:nil];
    [NSApp activateIgnoringOtherApps:YES];

    CGFloat centerX = NSMinX(windowFrame) + 95 + 230.0 / 2.0;
    CGFloat centerY = NSMaxY(screenFrame)
        - (NSMinY(windowFrame) + 75 + 56.0 / 2.0);
    printf("{\"ready\":true,\"x\":%d,\"y\":%d,\"width\":420,\"height\":220}\n",
        (int)llround(centerX), (int)llround(centerY));
    fflush(stdout);
}

- (void)buttonClicked:(id)sender {
    (void)sender;
    printf("{\"clicked\":true}\n");
    fflush(stdout);
    [NSApp terminate:nil];
}

@end

int main(int argc, const char *argv[]) {
    (void)argc;
    (void)argv;
    @autoreleasepool {
        NSApplication *app = [NSApplication sharedApplication];
        [app setActivationPolicy:NSApplicationActivationPolicyRegular];
        DobbyVPNReferenceProbeDelegate *delegate =
            [[DobbyVPNReferenceProbeDelegate alloc] init];
        [app setDelegate:delegate];
        [app run];
    }
    return 0;
}
