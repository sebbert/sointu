//go:build plugin && darwin

// The view that the host shows for the plugin: the tracker is a window of its
// own, which the view says, with a button to show it.

#import <Cocoa/Cocoa.h>
#include "view.h"

@interface SointuVST3View : NSView {
  @public
	uintptr_t plugin;
}
@end

@implementation SointuVST3View
- (void)showTracker:(id)sender {
	sointu_view_show(plugin);
}
@end

void *sointu_view_attach(void *parent, uintptr_t plugin, int width, int height) {
	NSView *host = (__bridge NSView *)parent;
	SointuVST3View *view = [[SointuVST3View alloc] initWithFrame:NSMakeRect(0, 0, width, height)];
	view->plugin = plugin;
	NSTextField *label = [NSTextField labelWithString:@"The Sointu tracker is a window of its own."];
	label.frame = NSMakeRect(16, 48, width - 32, 20);
	[view addSubview:label];
	NSButton *button = [NSButton buttonWithTitle:@"Show window" target:view action:@selector(showTracker:)];
	button.frame = NSMakeRect(12, 12, 130, 28);
	[view addSubview:button];
	[host addSubview:view];
	return view; // retained by alloc, released in sointu_view_remove
}

void sointu_view_remove(void *v) {
	SointuVST3View *view = (SointuVST3View *)v;
	[view removeFromSuperview];
	[view release];
}
