#import <Foundation/Foundation.h>

//! Project version number for CommonDI.
FOUNDATION_EXPORT double CommonDIVersionNumber;

//! Project version string for CommonDI.
FOUNDATION_EXPORT const unsigned char CommonDIVersionString[];

// In this header, you should import all the public headers of your framework using statements like #import <CommonDI/PublicHeader.h>

// C ABI consumed by the Go/Fyne iOS executable. The implementation lives in
// GoUIBridge.swift; keeping declarations here avoids exposing Swift types to
// the Go linker.
FOUNDATION_EXPORT char *dobby_ui_configure(const char *, long long, const unsigned char *, int);
FOUNDATION_EXPORT char *dobby_ui_start(const char *, long long, const char *, int);
FOUNDATION_EXPORT char *dobby_ui_stop(const char *, long long);
FOUNDATION_EXPORT char *dobby_ui_snapshot(const char *);
FOUNDATION_EXPORT char *dobby_ui_reset(const char *, long long);
FOUNDATION_EXPORT void dobby_ui_export_logs(const unsigned char *, int);
FOUNDATION_EXPORT void dobby_ui_free_string(char *);
FOUNDATION_EXPORT void dobby_ui_startup(const char *);
FOUNDATION_EXPORT void dobby_ui_attached(void);
FOUNDATION_EXPORT char *dobby_ui_diagnostic_paths(void);
