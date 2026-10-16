//go:build plugin && darwin

package gioui

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit

#import <AppKit/AppKit.h>
#include <stdint.h>
#include <sys/sysctl.h>
#include <unistd.h>

void sointuHostFront(int front);

static pid_t parentOf(pid_t pid) {
	struct kinfo_proc info;
	size_t len = sizeof(info);
	int mib[4] = {CTL_KERN, KERN_PROC, KERN_PROC_PID, pid};
	if (sysctl(mib, 4, &info, &len, NULL, 0) != 0 || len == 0)
		return 0;
	return info.kp_eproc.e_ppid;
}

// isHost tells if an application is the host of the plugin: this process
// (a host that loads its plugins itself, or the helper process whose window
// the user clicked), a process that this one descends from, or the
// application whose bundle the executable of this process is in (a helper
// process that the application did not start itself).
static int isHost(NSRunningApplication *app) {
	if (app == nil)
		return 0;
	pid_t front = app.processIdentifier;
	pid_t p = getpid();
	for (int i = 0; i < 16 && p > 1; i++) {
		if (p == front)
			return 1;
		p = parentOf(p);
	}
	NSString *bundle = app.bundleURL.path;
	NSString *exe = [NSBundle mainBundle].executablePath;
	if (bundle.length > 0 && exe != nil && [exe hasPrefix:[bundle stringByAppendingString:@"/"]])
		return 1;
	return 0;
}

// isApplication tells if this process is an application of its own, with
// its windows among those that the user switches between: a host that loads
// its plugins itself. A helper process of a host is not.
static int isApplication(void) {
	return [NSRunningApplication currentApplication].activationPolicy == NSApplicationActivationPolicyRegular;
}

static int hostIsFront(void) {
	return isHost([NSWorkspace sharedWorkspace].frontmostApplication);
}

static void watchFront(void) {
	static dispatch_once_t once;
	dispatch_once(&once, ^{
		[[NSWorkspace sharedWorkspace].notificationCenter
			addObserverForName:NSWorkspaceDidActivateApplicationNotification
			            object:nil
			             queue:[NSOperationQueue mainQueue]
			        usingBlock:^(NSNotification *note) {
			sointuHostFront(isHost(note.userInfo[NSWorkspaceApplicationKey]));
		}];
	});
}

// orderBack puts the window of a view behind the other windows of its level.
static void orderBack(uintptr_t viewRef) {
	CFTypeRef view = (CFTypeRef)viewRef;
	CFRetain(view);
	dispatch_async(dispatch_get_main_queue(), ^{
		NSView *v = (__bridge NSView *)view;
		[v.window orderBack:nil];
		CFRelease(view);
	});
}
*/
import "C"

import (
	"sync"

	"gioui.org/app"
	"gioui.org/io/event"
)

// hostFrontWatchers are the trackers of the plugin instances in this
// process, which are all told when the host comes to the front or goes.
var hostFrontWatchers = struct {
	sync.Mutex
	chans map[*Tracker]chan bool
}{chans: map[*Tracker]chan bool{}}

//export sointuHostFront
func sointuHostFront(front C.int) {
	hostFrontWatchers.Lock()
	defer hostFrontWatchers.Unlock()
	for _, c := range hostFrontWatchers.chans {
		select { // only the latest matters
		case <-c:
		default:
		}
		c <- front != 0
	}
}

// watchHostFront makes the window float above the host while the host is
// the application in front: see window.go. In a host that loads its plugins
// itself the window is one of the windows of the host, which the user
// switches between like its others: it does not float.
func (t *Tracker) watchHostFront() {
	if C.isApplication() != 0 {
		return
	}
	c := make(chan bool, 1)
	hostFrontWatchers.Lock()
	hostFrontWatchers.chans[t] = c
	hostFrontWatchers.Unlock()
	t.window.hostFront = c
	t.window.front = C.hostIsFront() != 0
	C.watchFront()
}

func (t *Tracker) unwatchHostFront() {
	hostFrontWatchers.Lock()
	delete(hostFrontWatchers.chans, t)
	hostFrontWatchers.Unlock()
}

// platformEvent keeps the view of the window, for sendWindowBack.
func (t *Tracker) platformEvent(e event.Event) {
	if e, ok := e.(app.AppKitViewEvent); ok {
		t.window.view = e.View
	}
}

// sendWindowBack puts the window behind the others, when another
// application than the host came to the front: the window, which floated
// until now, would else stay in front of that application's.
func (t *Tracker) sendWindowBack() {
	if t.window.view != 0 {
		C.orderBack(C.uintptr_t(t.window.view))
	}
}
