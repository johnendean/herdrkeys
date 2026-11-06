package keybow

import (
	"context"
	"os/exec"
	"time"
)

// findPorts asks the IORegistry, where a port's node sits beneath the USB
// device and interface it belongs to.
func findPorts() []Port {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(ctx, "ioreg", "-a", "-r", "-c", "IOUSBHostDevice", "-l").Output()
	if err != nil || len(raw) == 0 {
		return nil
	}
	return portsFromIOReg(raw)
}
