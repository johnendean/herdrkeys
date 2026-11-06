package hid

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	hidDLL            = windows.NewLazySystemDLL("hid.dll")
	procGetAttributes = hidDLL.NewProc("HidD_GetAttributes")
	procGetPreparsed  = hidDLL.NewProc("HidD_GetPreparsedData")
	procFreePreparsed = hidDLL.NewProc("HidD_FreePreparsedData")
	procGetCaps       = hidDLL.NewProc("HidP_GetCaps")
	procGetSerial     = hidDLL.NewProc("HidD_GetSerialNumberString")
	procGetProduct    = hidDLL.NewProc("HidD_GetProductString")
	hidInterfaceGUID  = windows.GUID{Data1: 0x4D1E55B2, Data2: 0xF16F, Data3: 0x11CF, Data4: [8]byte{0x88, 0xCB, 0x00, 0x11, 0x11, 0x00, 0x00, 0x30}}
	hidpStatusSuccess = uintptr(0x00110000)
)

type attributes struct {
	size                         uint32
	vendorID, productID, version uint16
}

type caps struct {
	usage, usagePage                           uint16
	inputLength, outputLength, featureLength   uint16
	reserved                                   [17]uint16
	linkNodes                                  uint16
	inputButtons, inputValues, inputData       uint16
	outputButtons, outputValues, outputData    uint16
	featureButtons, featureValues, featureData uint16
}

// describe opens path just far enough to ask what it is.
func describe(path string) (attributes, caps, string, string, error) {
	var a attributes
	var c caps
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return a, c, "", "", err
	}
	// No access asked for: enough to query, and allowed even while another
	// app holds the device.
	h, err := windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return a, c, "", "", err
	}
	defer windows.CloseHandle(h)
	a.size = uint32(unsafe.Sizeof(a))
	if ok, _, _ := procGetAttributes.Call(uintptr(h), uintptr(unsafe.Pointer(&a))); ok == 0 {
		return a, c, "", "", errors.New("HidD_GetAttributes failed")
	}
	var preparsed uintptr
	if ok, _, _ := procGetPreparsed.Call(uintptr(h), uintptr(unsafe.Pointer(&preparsed))); ok == 0 {
		return a, c, "", "", errors.New("HidD_GetPreparsedData failed")
	}
	defer procFreePreparsed.Call(preparsed)
	if status, _, _ := procGetCaps.Call(preparsed, uintptr(unsafe.Pointer(&c))); status != hidpStatusSuccess {
		return a, c, "", "", fmt.Errorf("HidP_GetCaps: %#x", status)
	}
	return a, c, hidString(procGetSerial, h), hidString(procGetProduct, h), nil
}

func hidString(proc *windows.LazyProc, h windows.Handle) string {
	buf := make([]uint16, 128)
	if ok, _, _ := proc.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)*2)); ok == 0 {
		return ""
	}
	return windows.UTF16ToString(buf)
}

func find(vendorID, productID, usagePage uint16) ([]Info, error) {
	paths, err := windows.CM_Get_Device_Interface_List("", &hidInterfaceGUID, windows.CM_GET_DEVICE_INTERFACE_LIST_PRESENT)
	if err != nil {
		return nil, err
	}
	var found []Info
	for _, path := range paths {
		a, c, serial, product, err := describe(path)
		if err != nil || a.vendorID != vendorID || a.productID != productID || c.usagePage != usagePage {
			continue
		}
		found = append(found, Info{VendorID: vendorID, ProductID: productID, UsagePage: usagePage,
			Serial: serial, Product: product, path: path})
	}
	return found, nil
}

// device is an opened interface, driven with overlapped I/O by hand so a read
// waiting for a press never holds up a write.
type device struct {
	handle       windows.Handle
	inputLength  int
	outputLength int
	writing      sync.Mutex
	writeEv      windows.Handle
	readEv       windows.Handle
	reports      chan []byte
	closed       atomic.Bool
	done         chan struct{}
	mu           sync.Mutex
	err          error
}

func open(info Info) (Device, error) {
	_, c, _, _, err := describe(info.path)
	if err != nil {
		return nil, err
	}
	name, _ := windows.UTF16PtrFromString(info.path)
	// Shared, as IOKit opens it on macOS: the device is not seized, so a
	// refusal here means another app opened it exclusively.
	h, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		if errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return nil, fmt.Errorf("another app holds it (%w)", err)
		}
		return nil, err
	}
	d := &device{handle: h, inputLength: int(c.inputLength), outputLength: int(c.outputLength),
		reports: make(chan []byte, 64), done: make(chan struct{})}
	if d.readEv, err = windows.CreateEvent(nil, 1, 0, nil); err == nil {
		d.writeEv, err = windows.CreateEvent(nil, 1, 0, nil)
	}
	if err != nil {
		d.release()
		return nil, err
	}
	go d.read()
	return d, nil
}

func (d *device) io(event windows.Handle, op func(*uint32, *windows.Overlapped) error) (int, error) {
	var n uint32
	o := windows.Overlapped{HEvent: event}
	err := op(&n, &o)
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		err = windows.GetOverlappedResult(d.handle, &o, &n, true)
	}
	return int(n), err
}

func (d *device) read() {
	defer close(d.done)
	defer close(d.reports)
	buf := make([]byte, d.inputLength)
	for !d.closed.Load() {
		n, err := d.io(d.readEv, func(n *uint32, o *windows.Overlapped) error {
			return windows.ReadFile(d.handle, buf, n, o)
		})
		if d.closed.Load() {
			d.fail(ErrClosed)
			return
		}
		if err != nil {
			d.fail(err) // unplugged, most likely
			return
		}
		if n > 1 {
			d.reports <- append([]byte(nil), buf[1:n]...) // past the report ID
		}
	}
}

func (d *device) fail(err error) {
	d.mu.Lock()
	if d.err == nil {
		d.err = err
	}
	d.mu.Unlock()
}

func (d *device) Write(report []byte) error {
	if d.closed.Load() {
		return ErrClosed
	}
	d.writing.Lock()
	defer d.writing.Unlock()
	// Report ID 0, then the report, padded to the length the device declares.
	out := make([]byte, max(d.outputLength, len(report)+1))
	copy(out[1:], report)
	n, err := d.io(d.writeEv, func(n *uint32, o *windows.Overlapped) error {
		return windows.WriteFile(d.handle, out, n, o)
	})
	if err != nil {
		return err
	}
	if n != len(out) {
		return fmt.Errorf("wrote %d of %d bytes", n, len(out))
	}
	return nil
}

func (d *device) Reports() <-chan []byte { return d.reports }

func (d *device) Err() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.err
}

func (d *device) Close() error {
	if d.closed.Swap(true) {
		return nil
	}
	windows.CancelIoEx(d.handle, nil)
	<-d.done
	d.writing.Lock()
	defer d.writing.Unlock()
	return d.release()
}

func (d *device) release() error {
	if d.readEv != 0 {
		windows.CloseHandle(d.readEv)
	}
	if d.writeEv != 0 {
		windows.CloseHandle(d.writeEv)
	}
	return windows.CloseHandle(d.handle)
}
