package safe

import (
	"errors"
	"strings"
	"testing"
)

func TestRecoverConvertsPanicToError(t *testing.T) {
	got := func() (err error) {
		defer Recover("pkg: decode", &err)
		panic("boom")
	}()
	if got == nil {
		t.Fatal("panic was not converted to an error")
	}
	if !strings.Contains(got.Error(), "pkg: decode") || !strings.Contains(got.Error(), "boom") {
		t.Fatalf("error missing context: %v", got)
	}
}

func TestRecoverLeavesCleanErrorPathUntouched(t *testing.T) {
	sentinel := errors.New("ordinary failure")
	got := func() (err error) {
		defer Recover("pkg: decode", &err)
		return sentinel
	}()
	if !errors.Is(got, sentinel) {
		t.Fatalf("Recover clobbered a normal error return: %v", got)
	}
}

func TestRecoverNilOnSuccess(t *testing.T) {
	got := func() (err error) {
		defer Recover("pkg: decode", &err)
		return nil
	}()
	if got != nil {
		t.Fatalf("Recover invented an error on the success path: %v", got)
	}
}

func TestGuardContainsPanicAndReports(t *testing.T) {
	var reported any
	// The function returns normally rather than crashing the test binary,
	// which is the whole point: a panic in a goroutine body is contained.
	func() {
		defer Guard(func(r any) { reported = r })
		panic("goroutine boom")
	}()
	if reported != "goroutine boom" {
		t.Fatalf("Guard did not report the panic value, got %v", reported)
	}
}

func TestGuardDoesNotReportWithoutPanic(t *testing.T) {
	called := false
	func() {
		defer Guard(func(r any) { called = true })
	}()
	if called {
		t.Fatal("Guard reported a panic that never happened")
	}
}
