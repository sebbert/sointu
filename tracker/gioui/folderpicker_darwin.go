//go:build darwin

package gioui

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit

#import <AppKit/AppKit.h>

void sointuFolderChosen(char *path);

// chooseFolder opens a panel for choosing a folder, starting in dir, on the
// main thread, and answers with sointuFolderChosen: the path, or an empty
// one if the user cancelled. The panel floats, as the window of a plugin
// does, which would else be in front of it.
static void chooseFolder(char *dir) {
	NSString *start = dir ? [NSString stringWithUTF8String:dir] : nil;
	free(dir);
	dispatch_async(dispatch_get_main_queue(), ^{
		NSOpenPanel *panel = [NSOpenPanel openPanel];
		panel.canChooseFiles = NO;
		panel.canChooseDirectories = YES;
		panel.allowsMultipleSelection = NO;
		panel.prompt = @"Browse";
		panel.level = NSFloatingWindowLevel;
		if (start.length > 0) {
			panel.directoryURL = [NSURL fileURLWithPath:start isDirectory:YES];
		}
		[NSApp activateIgnoringOtherApps:YES];
		[panel beginWithCompletionHandler:^(NSModalResponse result) {
			if (result == NSModalResponseOK && panel.URL.path != nil) {
				sointuFolderChosen((char *)panel.URL.path.fileSystemRepresentation);
			} else {
				sointuFolderChosen("");
			}
		}];
	});
}
*/
import "C"

import "sync"

var folderChosen struct {
	sync.Mutex
	f func(path string)
}

// canChooseFolder tells whether chooseFolder opens a panel of the system.
const canChooseFolder = true

// chooseFolder asks the user for a folder, starting in dir, and calls f with
// it, or with "" if the user cancelled, on a thread of its own. One panel is
// open at a time: see Tracker.Exploring.
func chooseFolder(dir string, f func(path string)) {
	folderChosen.Lock()
	folderChosen.f = f
	folderChosen.Unlock()
	C.chooseFolder(C.CString(dir))
}

//export sointuFolderChosen
func sointuFolderChosen(path *C.char) {
	p := C.GoString(path)
	folderChosen.Lock()
	f := folderChosen.f
	folderChosen.f = nil
	folderChosen.Unlock()
	if f != nil {
		go f(p)
	}
}
