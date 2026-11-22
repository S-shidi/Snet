package main

import (
	"log"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/windows"
)

const (
	instanceMutexName = `Local\SNetWindowsAppMutex`
	instanceEventName = `Local\SNetWindowsShowEvent`
)

var instanceMutex windows.Handle

// acquireSingleInstance must run before Wails is initialised. It returns the
// event handle used to wake the already-running window when a second copy of
// the executable is started.
func acquireSingleInstance() (windows.Handle, bool) {
	mutexName, _ := windows.UTF16PtrFromString(instanceMutexName)
	mutex, err := windows.CreateMutex(nil, false, mutexName)
	if err == windows.ERROR_ALREADY_EXISTS {
		windows.CloseHandle(mutex)
		signalExistingInstance()
		return 0, false
	}
	if err != nil {
		// Let the app start rather than locking the user out because of an
		// unusual security policy. A missing mutex only risks duplicate trays.
		log.Printf("single instance mutex: %v", err)
		return 0, true
	}
	instanceMutex = mutex // Held for the lifetime of the process.

	eventName, _ := windows.UTF16PtrFromString(instanceEventName)
	event, err := windows.CreateEvent(nil, 0, 0, eventName)
	if err != nil {
		log.Printf("single instance event: %v", err)
		return 0, true
	}
	return event, true
}

func signalExistingInstance() {
	name, _ := windows.UTF16PtrFromString(instanceEventName)
	for i := 0; i < 20; i++ {
		event, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, name)
		if err == nil {
			_ = windows.SetEvent(event)
			_ = windows.CloseHandle(event)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func watchShowRequests(event windows.Handle, show func()) {
	if event == 0 {
		return
	}
	go func() {
		for {
			status, err := windows.WaitForSingleObject(event, windows.INFINITE)
			if err != nil || status != windows.WAIT_OBJECT_0 {
				return
			}
			application.InvokeSync(show)
		}
	}()
}
