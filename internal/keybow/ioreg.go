package keybow

import "bytes"

// portsFromIOReg finds the Keybow's ports in what `ioreg -a` printed.
func portsFromIOReg(raw []byte) []Port {
	tree, err := decodePlist(bytes.NewReader(raw))
	if err != nil {
		return nil
	}
	roots, _ := tree.([]any)
	var found []Port
	for _, root := range roots {
		if node, ok := root.(map[string]any); ok {
			walkIOReg(node, -1, -1, -1, &found)
		}
	}
	return found
}

func walkIOReg(node map[string]any, vid, pid, iface int64, found *[]Port) {
	if v, ok := node["idVendor"].(int64); ok {
		vid = v
		pid, _ = node["idProduct"].(int64)
	}
	if i, ok := node["bInterfaceNumber"].(int64); ok {
		iface = i
	}
	if callout, ok := node["IOCalloutDevice"].(string); ok && callout != "" &&
		vid == VendorID && pid == ProductID {
		n := iface
		if n < 0 {
			// No interface above it: the suffix macOS gave the port ends in
			// one.
			n = 0
			if suffix, _ := node["IOTTYSuffix"].(string); suffix != "" {
				if last := suffix[len(suffix)-1]; last >= '0' && last <= '9' {
					n = int64(last - '0')
				}
			}
		}
		*found = append(*found, Port{Path: callout, Interface: int(n)})
	}
	children, _ := node["IORegistryEntryChildren"].([]any)
	for _, child := range children {
		if c, ok := child.(map[string]any); ok {
			walkIOReg(c, vid, pid, iface, found)
		}
	}
}
