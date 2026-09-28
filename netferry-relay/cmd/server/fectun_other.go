//go:build !unix

package main

import "errors"

type fectunArgs struct {
	port    int
	rate    float64
	rateMin float64
	target  string
	restart bool
}

func fectunTarget() string { return "" }

var errFectunUnsupported = errors.New("fectun is only supported on unix servers")

func runFectunUp(fectunArgs) error    { return errFectunUnsupported }
func runFectunServe(fectunArgs) error { return errFectunUnsupported }
