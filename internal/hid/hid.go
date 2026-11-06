// Package hid is the small part of HID the CN001 needs: find an interface by
// vendor, product and usage page, write output reports to it, and read its
// input reports. IOKit on macOS (through purego), hid.dll on Windows.
package hid

import "errors"

// Info is one HID interface, found.
type Info struct {
	VendorID, ProductID, UsagePage uint16
	Serial, Product                string
	// path names the interface to Open: a device path on Windows, an IOKit
	// registry entry on macOS.
	path string
}

// Device is an opened HID interface.
type Device interface {
	// Write sends one output report, without its report ID byte.
	Write(report []byte) error
	// Reports delivers input reports as they arrive, without their report ID
	// byte. Closed when the device goes away; Err then says why.
	Reports() <-chan []byte
	Err() error
	Close() error
}

// ErrClosed is a device used after Close.
var ErrClosed = errors.New("hid device closed")

// Find is every interface with these IDs.
func Find(vendorID, productID, usagePage uint16) ([]Info, error) {
	return find(vendorID, productID, usagePage)
}

// Open opens an interface Find returned.
func Open(info Info) (Device, error) { return open(info) }
