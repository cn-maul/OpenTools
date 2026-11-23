package plugins

import "sync"

// syncRWMutex aliases sync.RWMutex for the plugins subcomponents that share
// one lock. Keeping it here avoids importing sync in every file twice.
type syncRWMutex = sync.RWMutex
