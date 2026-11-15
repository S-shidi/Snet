//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -mmacosx-version-min=10.13
#cgo LDFLAGS: -framework Cocoa -mmacosx-version-min=10.13
#import <Cocoa/Cocoa.h>

// SnetDragRegionView is a transparent AppKit view that owns the title-bar drag
// area. Doing this natively avoids the WKWebView event path entirely, which is
// the path that intermittently stops receiving mouse events after a native
// window drag.
@interface SnetDragRegionView : NSView
@end

@implementation SnetDragRegionView
- (BOOL)acceptsFirstMouse:(NSEvent *)event {
    (void)event;
    return YES;
}
- (void)mouseDown:(NSEvent *)event {
    if (event.clickCount == 2) {
        [self.window zoom:nil];
        return;
    }
    [self.window performWindowDragWithEvent:event];
}
@end

static void snetInstallDragRegion(void *nativeWindow, double left, double right, double height) {
    NSWindow *window = (__bridge NSWindow *)nativeWindow;
    if (window == nil) {
        return;
    }
    NSView *content = [window contentView];
    if (content == nil) {
        return;
    }
    NSView *existing = nil;
    for (NSView *subview in [content subviews]) {
        if ([subview isKindOfClass:[SnetDragRegionView class]]) {
            existing = subview;
            break;
        }
    }
    if (existing != nil) {
        [existing removeFromSuperview];
    }

    SnetDragRegionView *view = [[SnetDragRegionView alloc] initWithFrame:NSZeroRect];
    view.translatesAutoresizingMaskIntoConstraints = NO;
    [content addSubview:view];
    [NSLayoutConstraint activateConstraints:@[
        [view.topAnchor constraintEqualToAnchor:content.topAnchor],
        [view.leadingAnchor constraintEqualToAnchor:content.leadingAnchor constant:left],
        [view.trailingAnchor constraintEqualToAnchor:content.trailingAnchor constant:-right],
        [view.heightAnchor constraintEqualToConstant:height],
    ]];
}
*/
import "C"

import "unsafe"

// installMacDragRegion adds the native drag handle above the WebView content.
// A 96pt left inset clears the traffic lights; a 150pt right inset keeps the
// refresh/settings/logout controls outside the drag region.
func installMacDragRegion(window unsafe.Pointer) {
	if window == nil {
		return
	}
	C.snetInstallDragRegion(window, C.double(96), C.double(150), C.double(52))
}
