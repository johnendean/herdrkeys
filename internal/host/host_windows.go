package host

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/johnendean/herdrkeys/internal/chord"
)

var (
	user32                 = windows.NewLazySystemDLL("user32.dll")
	kernel32               = windows.NewLazySystemDLL("kernel32.dll")
	procSendInput          = user32.NewProc("SendInput")
	procGetAncestor        = user32.NewProc("GetAncestor")
	procSetForeground      = user32.NewProc("SetForegroundWindow")
	procShowWindow         = user32.NewProc("ShowWindow")
	procIsIconic           = user32.NewProc("IsIconic")
	procKeybdEvent         = user32.NewProc("keybd_event")
	procAttachConsole      = kernel32.NewProc("AttachConsole")
	procFreeConsole        = kernel32.NewProc("FreeConsole")
	procGetConsoleWindow   = kernel32.NewProc("GetConsoleWindow")
	procAllowSetForeground = user32.NewProc("AllowSetForegroundWindow")
)

const (
	inputKeyboard    = 1
	keyEventKeyUp    = 0x0002
	keyEventExtended = 0x0001
	vkReturn         = 0x0D
	vkMenu           = 0x12
	gaRootOwner      = 3
	swRestore        = 9
)

// keybdInput is KEYBDINPUT inside an INPUT, laid out for 64-bit Windows:
// a DWORD type, padding to the union's alignment, and the union sized to its
// largest member (MOUSEINPUT, 32 bytes).
type keybdInput struct {
	typ       uint32
	_         uint32
	vk        uint16
	scan      uint16
	flags     uint32
	time      uint32
	extraInfo uintptr
	_         [8]byte
}

// The right-hand modifiers and the Windows keys are extended keys: sent
// without the flag, Right Ctrl arrives as Left Ctrl.
var extended = map[uint16]bool{0xA3: true, 0xA5: true, 0x5B: true, 0x5C: true}

func send(inputs []keybdInput) error {
	if len(inputs) == 0 {
		return nil
	}
	n, _, err := procSendInput.Call(uintptr(len(inputs)), uintptr(unsafe.Pointer(&inputs[0])), unsafe.Sizeof(inputs[0]))
	if int(n) != len(inputs) {
		return fmt.Errorf("SendInput: %v", err)
	}
	return nil
}

func key(vk uint16, down bool) keybdInput {
	in := keybdInput{typ: inputKeyboard, vk: vk}
	if !down {
		in.flags |= keyEventKeyUp
	}
	if extended[vk] {
		in.flags |= keyEventExtended
	}
	return in
}

// press holds every key of the chord down in order, or lets them go in
// reverse. SendInput needs no permission, so the CN001 types on Windows with
// nothing to grant -- though Windows will not let it type into a window
// running as administrator.
func press(c chord.Chord, down bool) error {
	var inputs []keybdInput
	if down {
		for _, k := range c {
			inputs = append(inputs, key(k.VK, true))
		}
	} else {
		for i := len(c) - 1; i >= 0; i-- {
			inputs = append(inputs, key(c[i].VK, false))
		}
	}
	return send(inputs)
}

func typeReturn() error {
	return send([]keybdInput{key(vkReturn, true), key(vkReturn, false)})
}

// CanType: SendInput needs no permission on Windows.
func (t *Typist) CanType() bool       { return true }
func (t *Typist) CanTypeAfresh() bool { return true }
func (t *Typist) Ask()                {}
func (t *Typist) Who() string         { return "nothing: Windows needs no permission to type" }

// Raiser brings the window hosting a Herdr client to the front.
//
// No terminal appears among a Windows Herdr client's ancestors (it runs under
// the shell, whose parent is Explorer), so the window is found through the
// client's console instead: attach to it, and the console window's root owner
// is the Windows Terminal or conhost window it is drawn in.
type Raiser struct {
	// Override, from terminal_app: not used for finding the window on
	// Windows, which needs no hint, but kept so a setting does not vanish.
	Override string
}

// Raise brings the terminal to the front. Best effort: never worth failing a
// key press.
func (r *Raiser) Raise() {
	if w := r.Window(); w != 0 {
		foreground(w)
	}
}

// Window is the window hosting a Herdr client, or 0 when none can be found.
func (r *Raiser) Window() windows.HWND {
	for _, pid := range herdrClients() {
		if w := consoleWindowOf(pid); w != 0 {
			return w
		}
	}
	return 0
}

// Describe says what Raise would bring forward, for doctor.
func (r *Raiser) Describe() string {
	w := r.Window()
	if w == 0 {
		return ""
	}
	var pid uint32
	windows.GetWindowThreadProcessId(w, &pid)
	return fmt.Sprintf("%s (window %#x)", processName(pid), w)
}

// consoleWindowOf attaches to a process's console just long enough to ask
// for its window. The daemon has no console of its own -- it is started
// detached -- so attaching costs it nothing.
func consoleWindowOf(pid uint32) windows.HWND {
	procFreeConsole.Call()
	if ok, _, _ := procAttachConsole.Call(uintptr(pid)); ok == 0 {
		return 0
	}
	defer procFreeConsole.Call()
	w, _, _ := procGetConsoleWindow.Call()
	if w == 0 {
		return 0
	}
	// Under Windows Terminal the console window is a hidden pseudo-window
	// owned by the terminal's own; under conhost it is the window itself.
	if owner, _, _ := procGetAncestor.Call(w, gaRootOwner); owner != 0 {
		return windows.HWND(owner)
	}
	return windows.HWND(w)
}

// foreground brings a window forward despite the foreground lock.
//
// Windows lets a background process take the foreground only in narrow
// cases; one of them is the process having just sent input. A tap of Alt is
// that input -- harmless on its own, and the standard way round the lock.
func foreground(w windows.HWND) {
	if iconic, _, _ := procIsIconic.Call(uintptr(w)); iconic != 0 {
		procShowWindow.Call(uintptr(w), swRestore)
	}
	procAllowSetForeground.Call(uintptr(0xFFFFFFFF)) // ASFW_ANY
	if ok, _, _ := procSetForeground.Call(uintptr(w)); ok != 0 {
		return
	}
	procKeybdEvent.Call(vkMenu, 0, keyEventExtended, 0)
	procKeybdEvent.Call(vkMenu, 0, keyEventExtended|keyEventKeyUp, 0)
	procSetForeground.Call(uintptr(w))
}

// herdrClients is every Herdr client process: herdr.exe, except the server,
// whose pid is the first field of the marker file at its socket path.
func herdrClients() []uint32 {
	server := serverPID()
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snapshot)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	var out []uint32
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		name := windows.UTF16ToString(entry.ExeFile[:])
		if strings.EqualFold(name, "herdr.exe") && entry.ProcessID != server {
			out = append(out, entry.ProcessID)
		}
	}
	return out
}

// serverPID is the running server's pid, from the marker Herdr leaves at its
// socket path (`pid:start-time`, ADR 0021), or 0.
func serverPID() uint32 {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return 0
	}
	marker, err := os.ReadFile(appData + `\herdr\herdr.sock`)
	if err != nil {
		return 0
	}
	pid, _ := strconv.ParseUint(strings.SplitN(strings.TrimSpace(string(marker)), ":", 2)[0], 10, 32)
	return uint32(pid)
}

func processName(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "pid " + strconv.Itoa(int(pid))
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return "pid " + strconv.Itoa(int(pid))
	}
	return windows.UTF16ToString(buf[:size])
}
