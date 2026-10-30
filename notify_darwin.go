package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation -framework UserNotifications
#include <stdlib.h>
#import <Foundation/Foundation.h>
#import <UserNotifications/UserNotifications.h>

@interface clackNotifier : NSObject <UNUserNotificationCenterDelegate>
@end
@implementation clackNotifier
- (void)userNotificationCenter:(UNUserNotificationCenter *)center
       willPresentNotification:(UNNotification *)notification
         withCompletionHandler:(void (^)(UNNotificationPresentationOptions))completionHandler {
	completionHandler(UNNotificationPresentationOptionBanner | UNNotificationPresentationOptionList |
	                  UNNotificationPresentationOptionSound);
}
@end

static clackNotifier *notifier;

static int bundled(void) {
	return [[NSBundle mainBundle] bundleIdentifier] != nil;
}

static void postNotification(const char *title, const char *body) {
	NSString *t = [NSString stringWithUTF8String:title];
	NSString *b = [NSString stringWithUTF8String:body];
	UNUserNotificationCenter *center = [UNUserNotificationCenter currentNotificationCenter];
	if (notifier == nil) {
		notifier = [clackNotifier new];
		center.delegate = notifier;
	}
	[center requestAuthorizationWithOptions:(UNAuthorizationOptionAlert | UNAuthorizationOptionSound)
	                      completionHandler:^(BOOL granted, NSError *error) {
		if (!granted) return;
		UNMutableNotificationContent *content = [UNMutableNotificationContent new];
		content.title = t;
		content.body = b;
		content.sound = [UNNotificationSound defaultSound];
		UNNotificationRequest *req = [UNNotificationRequest requestWithIdentifier:[[NSUUID UUID] UUIDString]
		                                                                  content:content trigger:nil];
		[center addNotificationRequest:req withCompletionHandler:nil];
	}];
}
*/
import "C"

import (
	"os/exec"
	"unsafe"
)

func notify(title, body string) error {
	if C.bundled() != 0 {
		ct, cb := C.CString(title), C.CString(body)
		defer C.free(unsafe.Pointer(ct))
		defer C.free(unsafe.Pointer(cb))
		C.postNotification(ct, cb)
		return nil
	}
	cmd := exec.Command("/usr/bin/osascript",
		"-e", "on run argv", "-e", "display notification (item 2 of argv) with title (item 1 of argv)", "-e", "end run",
		title, body)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
