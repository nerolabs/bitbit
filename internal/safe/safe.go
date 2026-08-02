// Package safe turns a panic on a decode or frame-processing path into a
// contained failure instead of a dead node.
//
// Silt's decoders parse bytes that arrive from the network and from users
// — i.e. from an adversary (anti-persona #14, resource exhaustion /
// malformed input). A decoder that panics on a malformed frame takes the
// whole process down, and a node that vanishes mid-request is
// indistinguishable from one that corrupted the bytes — exactly the
// silent-crash shape tenets S1 and S3 forbid. These helpers make a
// hostile input fail the *request*, never the *process*.
//
// The decoders themselves stay panic-free by construction, and the fuzz
// targets in each decoder package prove it. These guards are the
// defence-in-depth net underneath that proof: the last line that keeps
// the node up against a panic no fuzzer has reached yet.
package safe

import "fmt"

// Recover, deferred at the top of a function with a named error return,
// converts a panic in the body into that error. Use it to harden a decode
// entry point without threading recovery through every branch:
//
//	func Decode(b []byte) (out *T, err error) {
//	    defer safe.Recover("pkg: decode", &err)
//	    ...
//	}
//
// On a normal return recover reports nil and errp is left untouched, so a
// clean error path is preserved exactly.
func Recover(what string, errp *error) {
	if r := recover(); r != nil {
		*errp = fmt.Errorf("%s: recovered panic: %v", what, r)
	}
}

// Guard, deferred at the top of a per-connection goroutine (which has no
// caller to hand an error back to), stops a panic from unwinding into the
// runtime and killing the process. It reports the panic through report so
// the dropped connection is observable, never silent (tenets S3/V4).
//
//	go func() {
//	    defer safe.Guard(func(r any) { t.logf(...) })
//	    ...
//	}()
func Guard(report func(r any)) {
	if r := recover(); r != nil {
		report(r)
	}
}
