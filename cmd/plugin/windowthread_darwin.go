package plugin

/*
#include <pthread.h>
*/
import "C"

// onWindowThread tells if the caller runs on the thread that windows are
// made and closed on: the main thread. A call from the host runs on the
// thread of the host that made it.
func onWindowThread() bool { return C.pthread_main_np() != 0 }
