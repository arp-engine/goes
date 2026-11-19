package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Breakpoint struct {
	File    string
	Line    int
	Enabled bool
}

type Session struct {
	mu          sync.RWMutex
	breakpoints []Breakpoint
	trapHandler func(*Trap)
}

var globalSession = NewSession()

const breakpointsFileEnv = "GOES_DEBUG_BREAKPOINTS_FILE"
const defaultBreakpointsFileName = "godev-plus-plus-goes-breakpoints.json"
const breakpointsFileWatchInterval = 50 * time.Millisecond

type breakpointFilePayload struct {
	Version     int64        `json:"version"`
	Breakpoints []Breakpoint `json:"breakpoints"`
}

var breakpointFileMu sync.Mutex
var breakpointFilePath string
var breakpointFileMod time.Time
var breakpointFileSize int64
var breakpointFileVersion int64
var breakpointWatchMu sync.Mutex
var breakpointWatchRefs int
var breakpointWatchStop chan struct{}
var breakpointWatchDone chan struct{}
var breakpointWatchOnce sync.Once

var dlvControlFunctions = []any{
	SetBreakpoint,
	SetBreakpointsJSON,
	ClearBreakpoints,
}

func init() {
	if len(dlvControlFunctions) == 0 {
		panic("debugger: missing dlv control functions")
	}
}

func NewSession() *Session {
	return &Session{}
}

func SetBreakpoints(breakpoints []Breakpoint) {
	globalSession.SetBreakpoints(breakpoints)
}

//go:noinline
func SetBreakpoint(file string, line int) error {
	return globalSession.SetBreakpoint(Breakpoint{File: file, Line: line, Enabled: true})
}

//go:noinline
func SetBreakpointsJSON(jsonText string) error {
	var breakpoints []Breakpoint
	if err := json.Unmarshal([]byte(jsonText), &breakpoints); err != nil {
		return fmt.Errorf("debugger: invalid breakpoints json: %w", err)
	}
	globalSession.SetBreakpoints(breakpoints)
	return nil
}

//go:noinline
func ClearBreakpoints() {
	globalSession.ClearBreakpoints()
}

func SetTrapHandler(handler func(*Trap)) {
	globalSession.SetTrapHandler(handler)
}

func HasBreakpoints() bool {
	return globalSession.HasBreakpoints()
}

func ShouldBreak(file string, line int) bool {
	ensureBreakpointsFileWatch()
	return globalSession.ShouldBreak(file, line)
}

func HandleTrap(trap *Trap) {
	globalSession.HandleTrap(trap)
}

func BreakpointsFilePath() string {
	if path := os.Getenv(breakpointsFileEnv); path != "" {
		return path
	}
	return filepath.Join(os.TempDir(), defaultBreakpointsFileName)
}

func PollBreakpointsFile() error {
	path := BreakpointsFilePath()
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.Mode().IsRegular() {
		return nil
	}

	breakpointFileMu.Lock()
	defer breakpointFileMu.Unlock()
	if path == breakpointFilePath && info.ModTime().Equal(breakpointFileMod) && info.Size() == breakpointFileSize {
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var payload breakpointFilePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("debugger: invalid breakpoints file %s: %w", path, err)
	}
	if path == breakpointFilePath && payload.Version < breakpointFileVersion {
		return nil
	}

	globalSession.SetBreakpoints(payload.Breakpoints)
	breakpointFilePath = path
	breakpointFileMod = info.ModTime()
	breakpointFileSize = info.Size()
	breakpointFileVersion = payload.Version
	return nil
}

func StartBreakpointsFileWatch() func() {
	breakpointWatchMu.Lock()
	defer breakpointWatchMu.Unlock()
	breakpointWatchRefs++
	if breakpointWatchRefs == 1 {
		breakpointWatchStop = make(chan struct{})
		breakpointWatchDone = make(chan struct{})
		go watchBreakpointsFile(breakpointWatchStop, breakpointWatchDone)
	}
	return stopBreakpointsFileWatch
}

func ensureBreakpointsFileWatch() {
	breakpointWatchOnce.Do(func() {
		StartBreakpointsFileWatch()
	})
}

func stopBreakpointsFileWatch() {
	breakpointWatchMu.Lock()
	if breakpointWatchRefs == 0 {
		breakpointWatchMu.Unlock()
		return
	}
	breakpointWatchRefs--
	if breakpointWatchRefs > 0 {
		breakpointWatchMu.Unlock()
		return
	}
	stop := breakpointWatchStop
	done := breakpointWatchDone
	breakpointWatchStop = nil
	breakpointWatchDone = nil
	breakpointWatchMu.Unlock()

	close(stop)
	<-done
}

func watchBreakpointsFile(stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	_ = PollBreakpointsFile()
	ticker := time.NewTicker(breakpointsFileWatchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			_ = PollBreakpointsFile()
		}
	}
}

func (s *Session) SetBreakpoints(breakpoints []Breakpoint) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.breakpoints = append(s.breakpoints[:0], breakpoints...)
}

func (s *Session) SetBreakpoint(breakpoint Breakpoint) error {
	if s == nil {
		return fmt.Errorf("debugger: missing session")
	}
	if breakpoint.File == "" {
		return fmt.Errorf("debugger: breakpoint file is required")
	}
	if breakpoint.Line <= 0 {
		return fmt.Errorf("debugger: breakpoint line must be positive")
	}
	breakpoint.Enabled = true
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.breakpoints {
		if existing.Line == breakpoint.Line && sameDebugFile(existing.File, breakpoint.File) {
			s.breakpoints[i] = breakpoint
			return nil
		}
	}
	s.breakpoints = append(s.breakpoints, breakpoint)
	return nil
}

func (s *Session) ClearBreakpoints() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.breakpoints = s.breakpoints[:0]
}

func (s *Session) HasBreakpoints() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, breakpoint := range s.breakpoints {
		if breakpoint.Enabled {
			return true
		}
	}
	return false
}

func (s *Session) SetTrapHandler(handler func(*Trap)) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trapHandler = handler
}

func (s *Session) ShouldBreak(file string, line int) bool {
	if s == nil || file == "" || line <= 0 {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, breakpoint := range s.breakpoints {
		if !breakpoint.Enabled || breakpoint.Line != line {
			continue
		}
		if sameDebugFile(breakpoint.File, file) {
			return true
		}
	}
	return false
}

func (s *Session) HandleTrap(trap *Trap) {
	if s == nil || trap == nil {
		return
	}
	s.mu.RLock()
	handler := s.trapHandler
	s.mu.RUnlock()
	if handler != nil {
		handler(trap)
	}
	Break(trap)
}

func sameDebugFile(breakpointFile string, actualFile string) bool {
	if breakpointFile == "" || actualFile == "" {
		return false
	}
	breakpointClean := filepath.Clean(breakpointFile)
	actualClean := filepath.Clean(actualFile)
	if strings.EqualFold(breakpointClean, actualClean) {
		return true
	}
	if !filepath.IsAbs(breakpointClean) && strings.EqualFold(filepath.Base(breakpointClean), filepath.Base(actualClean)) {
		return true
	}
	return false
}

type Trap struct {
	File       string
	Line       int
	SourceLine string
	Function   string
	Stack      []StackFrame
	Locals     []Variable
	Globals    []Variable
}

type StackFrame struct {
	Function   string
	File       string
	Line       int
	SourceLine string
}

type Variable struct {
	Name         string
	Type         string
	Value        string
	RuntimeValue Value
}

//go:noinline
func Break(trap *Trap) {
}
