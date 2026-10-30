package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

static void *newIcon(const void *png, int len) {
	@autoreleasepool {
		NSData *data = [NSData dataWithBytes:png length:len];
		return (__bridge_retained void *)[[NSImage alloc] initWithData:data]; // kept for good
	}
}

static void setDockIcon(void *img) {
	dispatch_async(dispatch_get_main_queue(), ^{
		@autoreleasepool {
			if (NSApp != nil && img != NULL) [NSApp setApplicationIconImage:(__bridge NSImage *)img];
		}
	});
}
*/
import "C"

import (
	"sync"
	"unsafe"
)

var icons sync.Map // *byte → unsafe.Pointer

func setDockIcon(png []byte) {
	key := &png[0]
	img, ok := icons.Load(key)
	if !ok {
		img, _ = icons.LoadOrStore(key, C.newIcon(unsafe.Pointer(&png[0]), C.int(len(png))))
	}
	C.setDockIcon(img.(unsafe.Pointer))
}
