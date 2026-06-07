package main

import "os"

// osExit is a variable so tests can swap it to intercept os.Exit calls.
// Tests set osExit = func(code int) { panic(exitCodePanic(code)) }
// and then recover() to assert exit codes without terminating the process.
var osExit = realOsExit

func realOsExit(code int) {
	os.Exit(code)
}

// exitCodePanic is a typed panic value used by tests to capture exit codes.
type exitCodePanic int
