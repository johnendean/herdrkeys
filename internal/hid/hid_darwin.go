package hid

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"
)

// IOKit and CoreFoundation, through purego: no cgo, so every target builds
// from one machine (ADR 0020). Every signature is declared exactly; a pointer
// left to default would be truncated.

const (
	utf8Encoding     = 0x08000100 // kCFStringEncodingUTF8
	numberLongType   = 10         // kCFNumberLongType
	reportTypeOutput = 1          // kIOHIDReportTypeOutput
	pollSeconds      = 0.25       // how soon the reader notices Close
)

var (
	loadOnce sync.Once
	loadErr  error

	cfStringCreate      func(alloc uintptr, text string, encoding uint32) uintptr
	cfRelease           func(ref uintptr)
	cfRetain            func(ref uintptr) uintptr
	cfGetTypeID         func(ref uintptr) uintptr
	cfStringGetTypeID   func() uintptr
	cfStringGetCString  func(ref uintptr, buf *byte, size int, encoding uint32) bool
	cfNumberGetValue    func(ref uintptr, kind int, out *int64) bool
	cfSetGetCount       func(set uintptr) int
	cfSetGetValues      func(set uintptr, values *uintptr)
	cfRunLoopGetCurrent func() uintptr
	cfRunLoopRunInMode  func(mode uintptr, seconds float64, returnAfterSource bool) int32
	cfRunLoopStop       func(loop uintptr)
	runLoopDefaultMode  uintptr
	managerCreate       func(alloc uintptr, options uint32) uintptr
	managerSetMatching  func(manager uintptr, matching uintptr)
	managerCopyDevices  func(manager uintptr) uintptr
	deviceGetProperty   func(device uintptr, key uintptr) uintptr
	deviceOpen          func(device uintptr, options uint32) int32
	deviceClose         func(device uintptr, options uint32) int32
	deviceSetReport     func(device uintptr, kind uint32, id uintptr, report *byte, length int) int32
	deviceRegisterInput func(device uintptr, report *byte, length int, callback uintptr, context uintptr)
	deviceSchedule      func(device uintptr, loop uintptr, mode uintptr)
	deviceUnschedule    func(device uintptr, loop uintptr, mode uintptr)
	reportCallback      uintptr
)

func load() error {
	loadOnce.Do(func() {
		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			loadErr = err
			return
		}
		iokit, err := purego.Dlopen("/System/Library/Frameworks/IOKit.framework/IOKit", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			loadErr = err
			return
		}
		purego.RegisterLibFunc(&cfStringCreate, cf, "CFStringCreateWithCString")
		purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")
		purego.RegisterLibFunc(&cfRetain, cf, "CFRetain")
		purego.RegisterLibFunc(&cfGetTypeID, cf, "CFGetTypeID")
		purego.RegisterLibFunc(&cfStringGetTypeID, cf, "CFStringGetTypeID")
		purego.RegisterLibFunc(&cfStringGetCString, cf, "CFStringGetCString")
		purego.RegisterLibFunc(&cfNumberGetValue, cf, "CFNumberGetValue")
		purego.RegisterLibFunc(&cfSetGetCount, cf, "CFSetGetCount")
		purego.RegisterLibFunc(&cfSetGetValues, cf, "CFSetGetValues")
		purego.RegisterLibFunc(&cfRunLoopGetCurrent, cf, "CFRunLoopGetCurrent")
		purego.RegisterLibFunc(&cfRunLoopRunInMode, cf, "CFRunLoopRunInMode")
		purego.RegisterLibFunc(&cfRunLoopStop, cf, "CFRunLoopStop")
		purego.RegisterLibFunc(&managerCreate, iokit, "IOHIDManagerCreate")
		purego.RegisterLibFunc(&managerSetMatching, iokit, "IOHIDManagerSetDeviceMatching")
		purego.RegisterLibFunc(&managerCopyDevices, iokit, "IOHIDManagerCopyDevices")
		purego.RegisterLibFunc(&deviceGetProperty, iokit, "IOHIDDeviceGetProperty")
		purego.RegisterLibFunc(&deviceOpen, iokit, "IOHIDDeviceOpen")
		purego.RegisterLibFunc(&deviceClose, iokit, "IOHIDDeviceClose")
		purego.RegisterLibFunc(&deviceSetReport, iokit, "IOHIDDeviceSetReport")
		purego.RegisterLibFunc(&deviceRegisterInput, iokit, "IOHIDDeviceRegisterInputReportCallback")
		purego.RegisterLibFunc(&deviceSchedule, iokit, "IOHIDDeviceScheduleWithRunLoop")
		purego.RegisterLibFunc(&deviceUnschedule, iokit, "IOHIDDeviceUnscheduleFromRunLoop")
		// kCFRunLoopDefaultMode is a global CFStringRef: the symbol is its
		// address, and the string is what is stored there.
		mode, err := purego.Dlsym(cf, "kCFRunLoopDefaultMode")
		if err != nil {
			loadErr = err
			return
		}
		runLoopDefaultMode = *(*uintptr)(unsafe.Add(nil, mode))
		// One callback for every device, dispatched by context: purego can
		// make only so many.
		reportCallback = purego.NewCallback(onReport)
	})
	return loadErr
}

func property(device uintptr, key string) (int64, string, bool) {
	name := cfStringCreate(0, key, utf8Encoding)
	defer cfRelease(name)
	ref := deviceGetProperty(device, name)
	if ref == 0 {
		return 0, "", false
	}
	if cfGetTypeID(ref) == cfStringGetTypeID() {
		buf := make([]byte, 256)
		if cfStringGetCString(ref, &buf[0], len(buf), utf8Encoding) {
			for i, b := range buf {
				if b == 0 {
					return 0, string(buf[:i]), true
				}
			}
		}
		return 0, "", false
	}
	var v int64
	if cfNumberGetValue(ref, numberLongType, &v) {
		return v, "", true
	}
	return 0, "", false
}

// The device list is short, so it is matched by hand rather than through a
// matching dictionary, which would need CoreFoundation's callback structs.
// Each match is retained, and the ref travels in Info until Open, which owns
// it from then on.
var (
	refsMu sync.Mutex
	refs   = map[string]uintptr{}
)

func find(vendorID, productID, usagePage uint16) ([]Info, error) {
	if err := load(); err != nil {
		return nil, err
	}
	manager := managerCreate(0, 0)
	if manager == 0 {
		return nil, errors.New("IOHIDManagerCreate failed")
	}
	defer cfRelease(manager)
	managerSetMatching(manager, 0)
	set := managerCopyDevices(manager)
	if set == 0 {
		return nil, nil
	}
	defer cfRelease(set)
	count := cfSetGetCount(set)
	if count == 0 {
		return nil, nil
	}
	devices := make([]uintptr, count)
	cfSetGetValues(set, &devices[0])
	var found []Info
	for _, dev := range devices {
		vid, _, _ := property(dev, "VendorID")
		pid, _, _ := property(dev, "ProductID")
		page, _, _ := property(dev, "PrimaryUsagePage")
		if vid != int64(vendorID) || pid != int64(productID) || page != int64(usagePage) {
			continue
		}
		_, serial, _ := property(dev, "SerialNumber")
		_, product, _ := property(dev, "Product")
		path := fmt.Sprintf("iokit:%x", dev)
		refsMu.Lock()
		if _, ok := refs[path]; !ok {
			refs[path] = cfRetain(dev)
		}
		refsMu.Unlock()
		found = append(found, Info{VendorID: vendorID, ProductID: productID, UsagePage: usagePage,
			Serial: serial, Product: product, path: path})
	}
	return found, nil
}

const inputSize = 512

type device struct {
	ref     uintptr
	id      uintptr
	buffer  []byte
	pinner  runtime.Pinner
	reports chan []byte
	closed  atomic.Bool
	loop    atomic.Uintptr
	done    chan struct{}
	mu      sync.Mutex
	err     error
}

var (
	devicesMu sync.Mutex
	devices   = map[uintptr]*device{}
	nextID    uintptr
)

func onReport(context, result, sender, kind, id, report uintptr, length int) {
	devicesMu.Lock()
	d := devices[context]
	devicesMu.Unlock()
	if d == nil || int32(result) != 0 || length <= 0 || d.closed.Load() {
		return
	}
	data := append([]byte(nil), unsafe.Slice((*byte)(unsafe.Add(nil, report)), length)...)
	select {
	case d.reports <- data:
	default: // the daemon has fallen far behind; a dropped edge beats a stalled run loop
	}
}

func open(info Info) (Device, error) {
	if err := load(); err != nil {
		return nil, err
	}
	refsMu.Lock()
	ref, ok := refs[info.path]
	delete(refs, info.path)
	refsMu.Unlock()
	if !ok {
		return nil, errors.New("no such device; find it again")
	}
	// Opened without seizing it, so this succeeds alongside another reader;
	// another app that has seized it refuses us with kIOReturnExclusiveAccess.
	if status := deviceOpen(ref, 0); status != 0 {
		cfRelease(ref)
		return nil, fmt.Errorf("IOHIDDeviceOpen failed: %s", ioReturn(status))
	}
	d := &device{ref: ref, buffer: make([]byte, inputSize), reports: make(chan []byte, 64), done: make(chan struct{})}
	// IOKit keeps the buffer's address and writes into it on every report,
	// so it must not move or be collected while the device is open.
	d.pinner.Pin(&d.buffer[0])
	devicesMu.Lock()
	nextID++
	d.id = nextID
	devices[d.id] = d
	devicesMu.Unlock()
	deviceRegisterInput(ref, &d.buffer[0], len(d.buffer), reportCallback, d.id)
	scheduled := make(chan struct{})
	go d.run(scheduled)
	<-scheduled
	return d, nil
}

// run is the reader: IOKit delivers input reports only to a run loop, so one
// runs on a thread of its own, doing nothing else.
func (d *device) run(scheduled chan<- struct{}) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(d.done)
	defer close(d.reports)
	loop := cfRunLoopGetCurrent()
	deviceSchedule(d.ref, loop, runLoopDefaultMode)
	d.loop.Store(loop)
	close(scheduled)
	for !d.closed.Load() {
		cfRunLoopRunInMode(runLoopDefaultMode, pollSeconds, false)
	}
	deviceUnschedule(d.ref, loop, runLoopDefaultMode)
}

func (d *device) Write(report []byte) error {
	if d.closed.Load() {
		return ErrClosed
	}
	if len(report) == 0 {
		return nil
	}
	if status := deviceSetReport(d.ref, reportTypeOutput, 0, &report[0], len(report)); status != 0 {
		err := fmt.Errorf("IOHIDDeviceSetReport failed: %s", ioReturn(status))
		d.mu.Lock()
		if d.err == nil {
			d.err = err
		}
		d.mu.Unlock()
		return err
	}
	return nil
}

func (d *device) Reports() <-chan []byte { return d.reports }

func (d *device) Err() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err == nil {
		return ErrClosed
	}
	return d.err
}

func (d *device) Close() error {
	if d.closed.Swap(true) {
		return nil
	}
	if loop := d.loop.Load(); loop != 0 {
		cfRunLoopStop(loop) // so the reader leaves now, not at the end of its poll
	}
	<-d.done
	devicesMu.Lock()
	delete(devices, d.id)
	devicesMu.Unlock()
	deviceClose(d.ref, 0)
	cfRelease(d.ref)
	d.pinner.Unpin()
	return nil
}

func ioReturn(status int32) string {
	names := map[uint32]string{
		0xE00002BC: "kIOReturnError",
		0xE00002C0: "kIOReturnNoDevice",
		0xE00002C1: "kIOReturnNotPrivileged",
		0xE00002C2: "kIOReturnBadArgument",
		0xE00002C5: "kIOReturnExclusiveAccess -- another app holds it",
		0xE00002CD: "kIOReturnNotOpen",
		0xE00002D6: "kIOReturnTimeout",
		0xE00002D8: "kIOReturnNotAttached -- unplugged",
		0xE00002E2: "kIOReturnNotPermitted",
	}
	if name, ok := names[uint32(status)]; ok {
		return name
	}
	return fmt.Sprintf("IOReturn %#x", uint32(status))
}
