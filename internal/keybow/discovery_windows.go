package keybow

import (
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// findPorts reads the registry. `Enum\USB\VID_16D0&PID_08C6&MI_nn\<instance>`
// is each of the board's interfaces, and its `Device Parameters\PortName` is
// the COM port it was given -- but those keys outlive the board, so only a
// port `HARDWARE\DEVICEMAP\SERIALCOMM` lists as present now counts.
func findPorts() []Port {
	present := presentPorts()
	if len(present) == 0 {
		return nil
	}
	prefix := fmt.Sprintf(`VID_%04X&PID_%04X&MI_`, VendorID, ProductID)
	usb, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Enum\USB`, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil
	}
	defer usb.Close()
	devices, err := usb.ReadSubKeyNames(-1)
	if err != nil {
		return nil
	}
	var found []Port
	for _, device := range devices {
		if !strings.HasPrefix(strings.ToUpper(device), prefix) {
			continue
		}
		iface, err := strconv.ParseInt(device[len(prefix):], 16, 32)
		if err != nil {
			continue
		}
		for _, name := range portNames(`SYSTEM\CurrentControlSet\Enum\USB\` + device) {
			if present[strings.ToUpper(name)] {
				found = append(found, Port{Path: name, Interface: int(iface)})
			}
		}
	}
	return found
}

// presentPorts is every COM port that exists right now.
func presentPorts() map[string]bool {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DEVICEMAP\SERIALCOMM`, registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer key.Close()
	names, err := key.ReadValueNames(-1)
	if err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, name := range names {
		if port, _, err := key.GetStringValue(name); err == nil {
			out[strings.ToUpper(port)] = true
		}
	}
	return out
}

// portNames is the PortName of every instance under one interface's key.
func portNames(path string) []string {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil
	}
	defer key.Close()
	instances, err := key.ReadSubKeyNames(-1)
	if err != nil {
		return nil
	}
	var out []string
	for _, instance := range instances {
		params, err := registry.OpenKey(registry.LOCAL_MACHINE, path+`\`+instance+`\Device Parameters`, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		if name, _, err := params.GetStringValue("PortName"); err == nil && name != "" {
			out = append(out, name)
		}
		params.Close()
	}
	return out
}
