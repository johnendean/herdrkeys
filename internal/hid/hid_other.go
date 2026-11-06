//go:build !windows && !darwin

package hid

import (
	"fmt"
	"runtime"
)

func find(vendorID, productID, usagePage uint16) ([]Info, error) {
	return nil, fmt.Errorf("hid is not supported on %s", runtime.GOOS)
}

func open(Info) (Device, error) { return nil, fmt.Errorf("hid is not supported on %s", runtime.GOOS) }
