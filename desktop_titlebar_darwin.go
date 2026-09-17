//go:build desktop && darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa

#import <Cocoa/Cocoa.h>
#import <dispatch/dispatch.h>
#import <stdint.h>

static BOOL isRivoWindow(NSWindow *window) {
	NSString *title = window.title ?: @"";
	return [title isEqualToString:@"Rivo"] || [title hasSuffix:@" — Rivo"];
}

static void applyRivoTitleBarMode(void *context) {
	BOOL single = (BOOL)(intptr_t)context;
	for (NSWindow *window in NSApp.windows) {
		if (!isRivoWindow(window)) {
			continue;
		}
		if (single) {
			window.titlebarAppearsTransparent = YES;
			window.titleVisibility = NSWindowTitleHidden;
			window.styleMask |= NSWindowStyleMaskFullSizeContentView;
		} else {
			window.styleMask &= ~NSWindowStyleMaskFullSizeContentView;
			window.titlebarAppearsTransparent = NO;
			window.titleVisibility = NSWindowTitleVisible;
		}
	}
}

static void setRivoSingleWindowTitleBar(BOOL single) {
	dispatch_async_f(dispatch_get_main_queue(), (void *)(intptr_t)single, applyRivoTitleBarMode);
}
*/
import "C"

func setNativeSingleWindowTitleBar(single bool) {
	C.setRivoSingleWindowTitleBar(C.BOOL(single))
}
