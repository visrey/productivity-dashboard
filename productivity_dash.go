package main

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"
)

// Windows API constants for Input and Power Management
const (
	INPUT_MOUSE    = 0
	INPUT_KEYBOARD = 1

	MOUSEEVENTF_MOVE       = 0x0001
	MOUSEEVENTF_LEFTDOWN   = 0x0002
	MOUSEEVENTF_LEFTUP     = 0x0004
	MOUSEEVENTF_WHEEL      = 0x0800
	MOUSEEVENTF_ABSOLUTE   = 0x8000

	KEYEVENTF_EXTENDEDKEY = 0x0001
	KEYEVENTF_KEYUP       = 0x0002
	KEYEVENTF_UNICODE     = 0x0004
	KEYEVENTF_SCANCODE    = 0x0008

	VK_BACK  = 0x08
	VK_TAB   = 0x09
	VK_RETURN = 0x0D
	VK_MENU  = 0x12 // Alt key
	VK_RIGHT = 0x27
	VK_F15   = 0x7E

	ES_CONTINUOUS       = 0x80000000
	ES_SYSTEM_REQUIRED  = 0x00000001
	ES_DISPLAY_REQUIRED = 0x00000002

	SM_CXSCREEN = 0
	SM_CYSCREEN = 1
)

// Windows API Structs aligned for 64-bit / 32-bit architectures
type POINT struct {
	X, Y int32
}

type MOUSEINPUT struct {
	Dx          int32
	Dy          int32
	MouseData   uint32
	DwFlags     uint32
	Time        uint32
	DwExtraInfo uintptr
}

type KEYBDINPUT struct {
	WVk         uint16
	WScan       uint16
	DwFlags     uint32
	Time        uint32
	DwExtraInfo uintptr
}

// mouseInputUnion represents the exact memory layout required by Windows SendInput
type mouseInputUnion struct {
	Type uint32
	_    uint32 // 4-byte padding for 64-bit alignment
	Mi   MOUSEINPUT
}

// kbdInputUnion represents the exact memory layout for keyboard events padded to match MOUSEINPUT size
type kbdInputUnion struct {
	Type uint32
	_    uint32 // 4-byte padding for 64-bit alignment
	Ki   KEYBDINPUT
	_    [8]byte // Padding so total union matches MOUSEINPUT size (40 bytes on 64-bit)
}

var (
	user32                 = syscall.NewLazyDLL("user32.dll")
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procSendInput          = user32.NewProc("SendInput")
	procGetCursorPos       = user32.NewProc("GetCursorPos")
	procGetSystemMetrics   = user32.NewProc("GetSystemMetrics")
	procMapVirtualKey      = user32.NewProc("MapVirtualKeyW")
	procSetThreadExecState = kernel32.NewProc("SetThreadExecutionState")
)

// TelemetryStats tracks live session metrics for the local web dashboard
type TelemetryStats struct {
	mu              sync.Mutex
	StartTime       time.Time `json:"startTime"`
	CurrentPhase    string    `json:"currentPhase"`
	PhaseEndsAt     time.Time `json:"phaseEndsAt"`
	LastAction      string    `json:"lastAction"`
	NextActionInSec int       `json:"nextActionInSec"`
	MouseMoves      int       `json:"mouseMoves"`
	MouseClicks     int       `json:"mouseClicks"`
	MouseScrolls    int       `json:"mouseScrolls"`
	Keystrokes      int       `json:"keystrokes"`
	ActionBreakdown [4]int    `json:"actionBreakdown"`
	RecentLogs      []string  `json:"recentLogs"`
}

var stats = TelemetryStats{
	StartTime:    time.Now(),
	CurrentPhase: "ACTIVE",
	LastAction:   "Initializing simulator...",
}

func recordLog(msg string) {
	stats.mu.Lock()
	defer stats.mu.Unlock()
	timestamped := fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), msg)
	stats.LastAction = msg
	stats.RecentLogs = append([]string{timestamped}, stats.RecentLogs...)
	if len(stats.RecentLogs) > 15 {
		stats.RecentLogs = stats.RecentLogs[:15]
	}
}

func main() {
	rand.Seed(time.Now().UnixNano())

	fmt.Println("Starting Native Hardware-Queue Activity Simulator...")
	fmt.Println("Using Win32 SendInput + Hardware Scancodes + Click/Scroll Events")
	fmt.Println("Cycle: 5 Minutes Active -> 3 Minutes Idle.")
	fmt.Println("📊 Live Productivity Dashboard: http://localhost:8080")
	fmt.Println("Press Ctrl+C in this terminal to stop.")
	fmt.Println("----------------------------------------------------------------")

	// Start the lightweight embedded Productivity Dashboard in the background
	go startDashboardServer(":8080")

	snippets := loadSnippets("snippets.txt")
	fmt.Printf("Loaded %d code snippets.\n", len(snippets))
	fmt.Println("----------------------------------------------------------------")

	for {
		// --- ACTIVE PHASE ---
		fmt.Printf("[%s] 🟢 Entering ACTIVE phase for 5 minutes...\n", time.Now().Format("15:04:05"))
		keepSystemAwake(true)

		activePhaseEnd := time.Now().Add(5 * time.Minute)
		stats.mu.Lock()
		stats.CurrentPhase = "ACTIVE (5m)"
		stats.PhaseEndsAt = activePhaseEnd
		stats.mu.Unlock()
		recordLog("Entered ACTIVE phase (5 minutes)")

		for time.Now().Before(activePhaseEnd) {
			performRandomAction(snippets)

			// Wait 8 to 22 seconds between actions (avoids rolling 30s inactivity triggers)
			waitSecs := rand.Intn(15) + 8
			for remaining := waitSecs; remaining > 0; remaining-- {
				stats.mu.Lock()
				stats.NextActionInSec = remaining
				stats.mu.Unlock()
				time.Sleep(1 * time.Second)
			}
		}

		// --- IDLE PHASE ---
		fmt.Printf("[%s] 🔴 Entering IDLE phase for 3 minutes (No activity)...\n", time.Now().Format("15:04:05"))
		keepSystemAwake(false)

		idleEnd := time.Now().Add(3 * time.Minute)
		stats.mu.Lock()
		stats.CurrentPhase = "IDLE BREAK (3m)"
		stats.PhaseEndsAt = idleEnd
		stats.NextActionInSec = 180
		stats.mu.Unlock()
		recordLog("Entered IDLE phase (3 minutes natural break)")

		time.Sleep(3 * time.Minute)
	}
}

func keepSystemAwake(awake bool) {
	if awake {
		procSetThreadExecState.Call( uintptr(ES_CONTINUOUS | ES_SYSTEM_REQUIRED | ES_DISPLAY_REQUIRED) )
	} else {
		procSetThreadExecState.Call( uintptr(ES_CONTINUOUS) )
	}
}

func performRandomAction(snippets []string) {
	// 4 distinct hardware-queued actions including clicks and scrolls
	actionType := rand.Intn(4)
	stats.mu.Lock()
	stats.ActionBreakdown[actionType]++
	stats.mu.Unlock()

	switch actionType {
	case 0:
		// ACTION 1: Smooth Mouse Glide + Real Left Click + Scroll
		moveMouseSmoothlyNative()
		time.Sleep(time.Duration(rand.Intn(300)+150) * time.Millisecond)

		sendMouseClick()
		time.Sleep(time.Duration(rand.Intn(400)+200) * time.Millisecond)

		// Scroll down slightly, wait, scroll back up
		sendMouseScroll(-120)
		time.Sleep(time.Duration(rand.Intn(800)+400) * time.Millisecond)
		sendMouseScroll(120)

		sendVirtualKey(VK_F15)
		fmt.Printf("   -> Glided mouse, clicked focus, scrolled page, and sent hardware F15.\n")
		recordLog("Action 0: Mouse glide + Left click + Wheel scroll + F15")

	case 1:
		// ACTION 2: Switch Windows (Alt + Tab), do minor activity there, then switch back
		fmt.Printf("   -> Switching window (Alt + Tab) for 15s...\n")
		recordLog("Action 1: Switched window via Alt+Tab (15s review)")
		sendAltTab()

		// Break up the 15s wait with a small scroll so the tracker sees activity in the second window
		time.Sleep(7 * time.Second)
		moveMouseSmoothlyNative()
		sendMouseScroll(-120)
		time.Sleep(8 * time.Second)

		sendAltTab()
		fmt.Printf("   -> Switched back to primary window.\n")
		recordLog("Action 1: Returned to primary window via Alt+Tab")

	case 2:
		// ACTION 3: Click to focus, Type Snippet with variable human rhythm, pause, delete
		snippet := snippets[rand.Intn(len(snippets))]

		// Click at current mouse position first to ensure editor focus and register a click metric
		sendMouseClick()
		time.Sleep(200 * time.Millisecond)

		// Tap Right Arrow to clear any accidental text highlight
		sendVirtualKey(VK_RIGHT)
		time.Sleep(150 * time.Millisecond)

		fmt.Printf("   -> Typing snippet: %q\n", truncateString(snippet, 30))
		recordLog(fmt.Sprintf("Action 2: Typing code snippet (%d chars)", utf8.RuneCountInString(snippet)))
		typeStringHumanly(snippet)

		// Pause for 5 to 12 seconds while keeping micro-movements alive
		pauseSecs := rand.Intn(8) + 5
		fmt.Printf("      (Reviewing code for %d seconds...)\n", pauseSecs)
		time.Sleep(time.Duration(pauseSecs) * time.Second)

		// Clean up every character that was typed
		runeCount := utf8.RuneCountInString(snippet)
		for i := 0; i < runeCount; i++ {
			sendVirtualKey(VK_BACK)
			// Human-like variable backspace repeat rate (25ms - 55ms)
			time.Sleep(time.Duration(rand.Intn(30)+25) * time.Millisecond)
		}
		fmt.Printf("      (Snippet cleaned up)\n")
		recordLog("Action 2: Cleaned up temporary code snippet")

	case 3:
		// ACTION 4: Reading / Navigating simulation (Scroll + Right Arrow + Micro-move)
		moveMouseSmoothlyNative()
		sendMouseScroll(-240) // Scroll down 2 notches
		time.Sleep(time.Duration(rand.Intn(1500)+1000) * time.Millisecond)
		sendMouseScroll(240) // Scroll back up 2 notches
		sendVirtualKey(VK_RIGHT)
		fmt.Printf("   -> Simulated document reading (Mouse glide + Scroll Down/Up).\n")
		recordLog("Action 3: Simulated document reading & wheel scroll")
	}
}

// moveMouseSmoothlyNative uses cubic Bezier-like steps via Win32 SendInput
// rather than SetCursorPos so low-level mouse hooks & GetLastInputInfo register every step.
func moveMouseSmoothlyNative() {
	var pt POINT
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))

	// Constrain movement within a +/- 45 pixel box so it stays inside the active window
	offsetX := int32(rand.Intn(91) - 45)
	offsetY := int32(rand.Intn(91) - 45)

	steps := rand.Intn(15) + 20 // 20 to 35 micro-steps
	stepDx := float64(offsetX) / float64(steps)
	stepDy := float64(offsetY) / float64(steps)

	var accumX, accumY float64
	for i := 0; i < steps; i++ {
		accumX += stepDx
		accumY += stepDy

		moveX := int32(math.Round(accumX))
		moveY := int32(math.Round(accumY))

		if moveX != 0 || moveY != 0 {
			sendMouseMoveRelative(moveX, moveY)
			accumX -= float64(moveX)
			accumY -= float64(moveY)
		}
		// Variable human mouse polling delay (8ms to 18ms)
		time.Sleep(time.Duration(rand.Intn(11)+8) * time.Millisecond)
	}
}

func sendMouseMoveRelative(dx, dy int32) {
	stats.mu.Lock()
	stats.MouseMoves++
	stats.mu.Unlock()

	input := mouseInputUnion{
		Type: INPUT_MOUSE,
		Mi: MOUSEINPUT{
			Dx:      dx,
			Dy:      dy,
			DwFlags: MOUSEEVENTF_MOVE,
		},
	}
	procSendInput.Call(1, uintptr(unsafe.Pointer(&input)), unsafe.Sizeof(input))
}

func sendMouseClick() {
	stats.mu.Lock()
	stats.MouseClicks++
	stats.mu.Unlock()

	down := mouseInputUnion{
		Type: INPUT_MOUSE,
		Mi:   MOUSEINPUT{DwFlags: MOUSEEVENTF_LEFTDOWN},
	}
	up := mouseInputUnion{
		Type: INPUT_MOUSE,
		Mi:   MOUSEINPUT{DwFlags: MOUSEEVENTF_LEFTUP},
	}
	procSendInput.Call(1, uintptr(unsafe.Pointer(&down)), unsafe.Sizeof(down))
	// Humans hold the mouse button down for 40-90ms when clicking
	time.Sleep(time.Duration(rand.Intn(50)+40) * time.Millisecond)
	procSendInput.Call(1, uintptr(unsafe.Pointer(&up)), unsafe.Sizeof(up))
}

func sendMouseScroll(delta int32) {
	stats.mu.Lock()
	stats.MouseScrolls++
	stats.mu.Unlock()

	input := mouseInputUnion{
		Type: INPUT_MOUSE,
		Mi: MOUSEINPUT{
			MouseData: uint32(delta),
			DwFlags:   MOUSEEVENTF_WHEEL,
		},
	}
	procSendInput.Call(1, uintptr(unsafe.Pointer(&input)), unsafe.Sizeof(input))
}

// sendVirtualKey sends a real hardware scancode + virtual key to satisfy low-level hooks
func sendVirtualKey(vk uint16) {
	stats.mu.Lock()
	stats.Keystrokes++
	stats.mu.Unlock()

	scan, _, _ := procMapVirtualKey.Call(uintptr(vk), 0)

	down := kbdInputUnion{
		Type: INPUT_KEYBOARD,
		Ki: KEYBDINPUT{
			WVk:     vk,
			WScan:   uint16(scan),
			DwFlags: 0,
		},
	}
	up := kbdInputUnion{
		Type: INPUT_KEYBOARD,
		Ki: KEYBDINPUT{
			WVk:     vk,
			WScan:   uint16(scan),
			DwFlags: KEYEVENTF_KEYUP,
		},
	}

	procSendInput.Call(1, uintptr(unsafe.Pointer(&down)), unsafe.Sizeof(down))
	time.Sleep(time.Duration(rand.Intn(40)+30) * time.Millisecond)
	procSendInput.Call(1, uintptr(unsafe.Pointer(&up)), unsafe.Sizeof(up))
}

func sendAltTab() {
	altScan, _, _ := procMapVirtualKey.Call(uintptr(VK_MENU), 0)
	tabScan, _, _ := procMapVirtualKey.Call(uintptr(VK_TAB), 0)

	altDown := kbdInputUnion{Type: INPUT_KEYBOARD, Ki: KEYBDINPUT{WVk: VK_MENU, WScan: uint16(altScan)}}
	tabDown := kbdInputUnion{Type: INPUT_KEYBOARD, Ki: KEYBDINPUT{WVk: VK_TAB, WScan: uint16(tabScan)}}
	tabUp := kbdInputUnion{Type: INPUT_KEYBOARD, Ki: KEYBDINPUT{WVk: VK_TAB, WScan: uint16(tabScan), DwFlags: KEYEVENTF_KEYUP}}
	altUp := kbdInputUnion{Type: INPUT_KEYBOARD, Ki: KEYBDINPUT{WVk: VK_MENU, WScan: uint16(altScan), DwFlags: KEYEVENTF_KEYUP}}

	procSendInput.Call(1, uintptr(unsafe.Pointer(&altDown)), unsafe.Sizeof(altDown))
	time.Sleep(60 * time.Millisecond)
	procSendInput.Call(1, uintptr(unsafe.Pointer(&tabDown)), unsafe.Sizeof(tabDown))
	time.Sleep(50 * time.Millisecond)
	procSendInput.Call(1, uintptr(unsafe.Pointer(&tabUp)), unsafe.Sizeof(tabUp))
	time.Sleep(60 * time.Millisecond)
	procSendInput.Call(1, uintptr(unsafe.Pointer(&altUp)), unsafe.Sizeof(altUp))
}

func typeStringHumanly(s string) {
	for _, r := range s {
		if r == '\n' {
			sendVirtualKey(VK_RETURN)
		} else if r == '\t' {
			sendVirtualKey(VK_TAB)
		} else {
			sendUnicodeChar(r)
		}
		// Human typing cadence: 55ms to 165ms between keys, with occasional brief pauses
		delay := rand.Intn(110) + 55
		if rand.Intn(10) == 0 {
			delay += rand.Intn(250) + 100 // Occasional micro-hesitation
		}
		time.Sleep(time.Duration(delay) * time.Millisecond)
	}
}

func sendUnicodeChar(r rune) {
	stats.mu.Lock()
	stats.Keystrokes++
	stats.mu.Unlock()

	utf16Units := utf16.Encode([]rune{r})
	for _, u := range utf16Units {
		down := kbdInputUnion{
			Type: INPUT_KEYBOARD,
			Ki: KEYBDINPUT{
				WScan:   u,
				DwFlags: KEYEVENTF_UNICODE,
			},
		}
		up := kbdInputUnion{
			Type: INPUT_KEYBOARD,
			Ki: KEYBDINPUT{
				WScan:   u,
				DwFlags: KEYEVENTF_UNICODE | KEYEVENTF_KEYUP,
			},
		}
		procSendInput.Call(1, uintptr(unsafe.Pointer(&down)), unsafe.Sizeof(down))
		time.Sleep(time.Duration(rand.Intn(35)+20) * time.Millisecond)
		procSendInput.Call(1, uintptr(unsafe.Pointer(&up)), unsafe.Sizeof(up))
	}
}

func loadSnippets(filename string) []string {
	data, err := os.ReadFile(filename)
	if err != nil {
		fmt.Printf("Could not read '%s'. Using default built-in snippets.\n", filename)
		return []string{
			"// reviewing edge case parameters",
			"console.log('Validating payload state...');",
			"// check nil pointer dereference before return",
			"/* temporary state inspection */",
		}
	}

	rawSnippets := strings.Split(string(data), "\n\n")
	var validSnippets []string

	for _, s := range rawSnippets {
		trimmed := strings.TrimSpace(s)
		if trimmed != "" {
			validSnippets = append(validSnippets, trimmed)
		}
	}

	if len(validSnippets) == 0 {
		return []string{"// Testing..."}
	}

	return validSnippets
}

func truncateString(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}

// startDashboardServer hosts a local web dashboard at http://localhost:8080
func startDashboardServer(addr string) {
	http.HandleFunc("/api/stats", func(w http.ResponseWriter, r *http.Request) {
		stats.mu.Lock()
		defer stats.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(stats)
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(dashboardHTML))
	})

	_ = http.ListenAndServe(addr, nil)
}

const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<title>Workspace Productivity & Telemetry</title>
<script src="https://cdn.tailwindcss.com"></script>
</head>
<body class="bg-slate-950 text-slate-100 min-h-screen p-6 font-sans">
	<div class="max-w-5xl mx-auto space-y-6">
		<div class="flex items-center justify-between bg-slate-900 border border-slate-800 p-5 rounded-2xl">
			<div>
				<h1 class="text-xl font-bold">Engineering Flow & Session Telemetry</h1>
				<p class="text-xs text-slate-400">Live Local Hardware Queue Monitor (5m Active / 3m Break)</p>
			</div>
			<span id="phaseBadge" class="px-3 py-1 rounded-full text-xs font-bold bg-emerald-500/20 text-emerald-300 border border-emerald-500/30">ACTIVE</span>
		</div>
		<div class="grid grid-cols-2 md:grid-cols-4 gap-4">
			<div class="bg-slate-900 border border-slate-800 p-4 rounded-xl">
				<div class="text-xs text-slate-400">Mouse Micro-Steps</div>
				<div id="mMoves" class="text-2xl font-bold text-emerald-400 mt-1">0</div>
			</div>
			<div class="bg-slate-900 border border-slate-800 p-4 rounded-xl">
				<div class="text-xs text-slate-400">Hardware Clicks</div>
				<div id="mClicks" class="text-2xl font-bold text-amber-400 mt-1">0</div>
			</div>
			<div class="bg-slate-900 border border-slate-800 p-4 rounded-xl">
				<div class="text-xs text-slate-400">Wheel Scrolls</div>
				<div id="mScrolls" class="text-2xl font-bold text-indigo-400 mt-1">0</div>
			</div>
			<div class="bg-slate-900 border border-slate-800 p-4 rounded-xl">
				<div class="text-xs text-slate-400">Scancode Keys</div>
				<div id="mKeys" class="text-2xl font-bold text-cyan-400 mt-1">0</div>
			</div>
		</div>
		<div class="bg-slate-900 border border-slate-800 p-5 rounded-2xl">
			<div class="flex justify-between text-xs text-slate-400 mb-3">
				<span>Current Activity: <strong id="lastAct" class="text-white">...</strong></span>
				<span>Next Action In: <strong id="nextSec" class="text-emerald-400">0s</strong></span>
			</div>
			<div id="logs" class="bg-slate-950 border border-slate-800 rounded-xl p-3 font-mono text-xs space-y-1.5 max-h-72 overflow-y-auto"></div>
		</div>
	</div>
	<script>
		setInterval(async () => {
			try {
				const res = await fetch('/api/stats');
				const d = await res.json();
				document.getElementById('phaseBadge').textContent = d.currentPhase;
				document.getElementById('mMoves').textContent = d.mouseMoves;
				document.getElementById('mClicks').textContent = d.mouseClicks;
				document.getElementById('mScrolls').textContent = d.mouseScrolls;
				document.getElementById('mKeys').textContent = d.keystrokes;
				document.getElementById('lastAct').textContent = d.lastAction;
				document.getElementById('nextSec').textContent = d.nextActionInSec + 's';
				document.getElementById('logs').innerHTML = (d.recentLogs || []).map(l => '<div>' + l + '</div>').join('');
			} catch (e) {}
		}, 1000);
	</script>
</body>
</html>`