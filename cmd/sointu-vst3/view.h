// The view that the host shows for the plugin, made by the system: on macOS
// an NSView, in view_darwin.m.

#include <stdint.h>

#if defined(__APPLE__)
// sointu_view_attach adds a view of the given size to parent, with a button
// that calls sointu_view_show(plugin), and returns it, or NULL.
void *sointu_view_attach(void *parent, uintptr_t plugin, int width, int height);
// sointu_view_remove removes the view from its parent and frees it.
void sointu_view_remove(void *view);
#else
static void *sointu_view_attach(void *parent, uintptr_t plugin, int width, int height) { return NULL; }
static void sointu_view_remove(void *view) {}
#endif

void sointu_view_show(uintptr_t plugin);
