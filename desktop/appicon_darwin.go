//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit
#import <AppKit/AppKit.h>
#import <dispatch/dispatch.h>

static void agxSetApplicationIcon(void *bytes, long length) {
	@autoreleasepool {
		NSData *data = [NSData dataWithBytes:bytes length:(NSUInteger)length];
		NSImage *image = [[NSImage alloc] initWithData:data];
		if (image != nil) {
			dispatch_async(dispatch_get_main_queue(), ^{
				[[NSApplication sharedApplication] setApplicationIconImage:image];
			});
		}
	}
}
*/
import "C"
import "unsafe"

func setApplicationIcon(icon []byte) {
	if len(icon) == 0 {
		return
	}
	C.agxSetApplicationIcon(unsafe.Pointer(&icon[0]), C.long(len(icon)))
}
