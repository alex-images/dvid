/*
	This file supports DVID distributed systems and goroutines.
*/

package dvid

import (
	"sync"
	"time"
)

type cgoStatus int

const (
	MaxNumberConcurrentCgo = 10000

	cgoStarted cgoStatus = 1
	cgoStopped cgoStatus = -1
)

var (
	// CgoActive is a buffered channel for signaling cgo routines that are active.
	cgoActive chan cgoStatus

	cgoNumActive int
	startCgo     sync.Mutex
)

func init() {
	// Create CgoActive channel to keep tract of the # of active cgo routines.
	// This is useful for graceful shutdown of DVID when there are outstanding
	// goroutines using cgo.
	cgoActive = make(chan cgoStatus, MaxNumberConcurrentCgo)
	go func() {
		for {
			switch <-cgoActive {
			case cgoStarted:
				cgoNumActive++
			case cgoStopped:
				cgoNumActive--
			}
		}
	}()
}

// BlockOnActiveCgo will block until all active cgo routines have been finished or
// queued for starting.  This requires cgo routines to be bracketed by:
//    dvid.StartCgo()
//    /* Some cgo code */
//    dvid.StopCgo()
func BlockOnActiveCgo() {
	startCgo.Lock()
	defer startCgo.Unlock()

	Infof("Checking for any active cgo routines...\n")
	waits := 0
	for {
		if (cgoNumActive == 0 && len(cgoActive) == 0) || waits >= 5 {
			return
		}
		Infof("Waited %d seconds for %d active cgo routines (%d messages to be processed)...\n",
			waits, cgoNumActive, len(cgoActive))
		waits++
		time.Sleep(1 * time.Second)
	}
}

// StartCgo is used to mark the beginning of a cgo routine and blocks if we have requested
// a BlockOnActiveCgo().  This is used for graceful shutdowns and monitoring.
func StartCgo() {
	startCgo.Lock()
	cgoActive <- cgoStarted
	startCgo.Unlock()
}

func StopCgo() {
	cgoActive <- cgoStopped
}
