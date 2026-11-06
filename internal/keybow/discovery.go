package keybow

import "sort"

// The Keybow 2040's USB identity.
const (
	VendorID  = 0x16D0 // 5840, Pimoroni
	ProductID = 0x08C6 // 2246, Keybow 2040
)

// Port is one of a Keybow's serial ports, and the USB interface it is on.
//
// Enabling `usb_cdc.data` gives the board two serial ports, console and
// data, and the OS names them in ways that shift with hub topology -- so a
// path cannot be hardcoded. CircuitPython puts the console on the lower
// interface and the data channel on the higher one, on every platform.
type Port struct {
	Path      string
	Interface int
}

// FindPorts is every serial port belonging to a Keybow 2040, lowest
// interface first.
func FindPorts() []Port {
	ports := findPorts()
	unique := map[string]Port{}
	for _, p := range ports {
		unique[p.Path] = p
	}
	out := make([]Port, 0, len(unique))
	for _, p := range unique {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Interface < out[j].Interface })
	return out
}

// DataPort is the port frames go to, given a board's ports, or "" if there
// is none.
//
// With console and data both enabled the data channel is the higher
// interface. With only one port present the board has not had `boot.py`
// deployed yet, so there is no data channel to talk to -- and writing frames
// into the console would feed JSON to the REPL rather than fail cleanly.
func DataPort(ports []Port) string {
	if len(ports) < 2 {
		return ""
	}
	return ports[len(ports)-1].Path
}

// ConsolePort is the port the REPL is on, or "".
func ConsolePort(ports []Port) string {
	if len(ports) == 0 {
		return ""
	}
	return ports[0].Path
}
