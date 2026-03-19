// smappservice.m — thin C wrapper around SMAppService (macOS 13+).
// Compiled by build.rs via the `cc` crate; linked into the main app only.
//
// NOTE: We deliberately avoid @available() here.  That macro emits a call to
// ___isPlatformVersionAtLeast which lives in the clang compiler_rt, and the
// `cc` build does not automatically link that library.  Instead we use
// NSProcessInfo.isOperatingSystemAtLeastVersion: for the runtime check, which
// is available since macOS 10.10 and does not require any extra link step.
// Compiler warnings about "SMAppService may not respond" are suppressed below.

#import <ServiceManagement/ServiceManagement.h>
#import <Foundation/Foundation.h>

#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wunguarded-availability-new"
#pragma clang diagnostic ignored "-Wdeprecated-declarations"

static BOOL isMacOS13OrLater(void) {
    NSOperatingSystemVersion v = {13, 0, 0};
    return [[NSProcessInfo processInfo] isOperatingSystemAtLeastVersion:v];
}

// Returns SMAppServiceStatus (0=NotRegistered, 1=Enabled, 2=RequiresApproval,
// 3=NotFound) or -1 if OS is too old / SMAppService unavailable.
int netferry_helper_status(void) {
    if (!isMacOS13OrLater()) return -1;
    SMAppService *svc =
        [SMAppService daemonServiceWithPlistName:
            @"com.hoveychen.netferry.helper.plist"];
    return svc ? (int)svc.status : -1;
}

// Registers (installs) the helper daemon.
// Shows the one-time macOS authorisation dialog when not yet approved.
// Returns 0 on success, -1 on error, -2 if OS too old.
int netferry_register_helper(void) {
    if (!isMacOS13OrLater()) return -2;
    SMAppService *svc =
        [SMAppService daemonServiceWithPlistName:
            @"com.hoveychen.netferry.helper.plist"];
    if (!svc) return -2;
    NSError *err = nil;
    BOOL ok = [svc registerAndReturnError:&err];
    if (!ok && err) NSLog(@"NetFerry: register helper error: %@", err);
    return ok ? 0 : -1;
}

// Unregisters (removes) the helper daemon.
int netferry_unregister_helper(void) {
    if (!isMacOS13OrLater()) return -2;
    SMAppService *svc =
        [SMAppService daemonServiceWithPlistName:
            @"com.hoveychen.netferry.helper.plist"];
    if (!svc) return -2;
    NSError *err = nil;
    BOOL ok = [svc unregisterAndReturnError:&err];
    if (!ok && err) NSLog(@"NetFerry: unregister helper error: %@", err);
    return ok ? 0 : -1;
}

#pragma clang diagnostic pop
