package agent

// ProcessAlive reports whether pid names a live process on this machine.
// It is the shared liveness primitive for lease-holder classification
// (tl 1201): a lease whose recorded holder pid is dead is a zombie leftover,
// while a lease held by a live foreign process is real cross-process mutual
// exclusion and must keep being respected. Callers must treat an unknown pid
// (<= 0) as dead so identity-less holders never render as "another window".
//
// The variable is the test seam (same pattern as desktop's
// leaseReconcileProcessAlive and servepool's processAlive); production code
// reads it through the function value so a test swap applies at call time.
var ProcessAlive = defaultProcessAlive
