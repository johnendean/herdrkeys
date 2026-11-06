package keybow

import "golang.org/x/sys/windows"

const driveRemovable = 2 // DRIVE_REMOVABLE

// mountPoints is every removable drive, which is what CIRCUITPY mounts as.
// Fixed and network drives are not asked: a sleeping network share can take
// seconds to answer, and the board is never one.
func mountPoints() []string {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil
	}
	var out []string
	for i := 0; i < 26; i++ {
		if mask&(1<<i) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		utf16, _ := windows.UTF16PtrFromString(root)
		if windows.GetDriveType(utf16) == driveRemovable {
			out = append(out, root)
		}
	}
	return out
}
