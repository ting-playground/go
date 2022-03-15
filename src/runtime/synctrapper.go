package runtime

import (
	"runtime/internal/atomic"
	"unsafe"
)

var SyncTraceEnable uint8

func EnableSyncTracing() {
	atomic.Store8(&SyncTraceEnable, 1)
}

func DisableSyncTracing() {
	atomic.Store8(&SyncTraceEnable, 0)
}

func isSyncTraceDisabled() bool {
	return atomic.Load8(&SyncTraceEnable) == 0
}

/*
func syncTraceEnabled() bool {
	s := gogetenv("SYNCTRAPPER_TRACE")
	if s == "0" {
		return false
	}
	if s == "1" {
		return true
	}

	return false
}
*/

var StTrace = &stTrace{}

type SyncEventType int

const (
	LockEvent SyncEventType = iota
	UnlockEvent
	WLockEvent
	WUnlockEvent
	RLockEvent
	RUnlockEvent
	ChanSend1Event
	ChanRecv2Event
	ChanRecv1Envet
	ChanCloseEvent
	ChanReflectSendEvent
	ChanReflectRecvEvent
	ChanReflectCloseEvent
	SelectSendEvent
	SelectBufSendEvent
	SelectBufRecvEvent
	SelectCloseRecvEvent
	SelectNbSendEvent
	SelectRecvEvent
	SelectNbRecvEvent
	SelectDefaultEvent
	WGWaitEvent
	WGDoneEvent
	CondWaitEvent
	CondSignalEvent
	CondBroadcastEvent
	NewProcEvent
	MainFuncStartEvent
	ChanMakeEvent
	ChanMake64Event
	ChanReflectMakeEvent
	CtxCancelEvent
	CtxDoneEvent
	SelectWakeUpEvent
	DeferEvent
	DeferReturnEvent
)

type StTraceEvent struct {
	Goid int64
	Now  int64
	Type int
	Addr unsafe.Pointer
	File string
	Line int
	Hold int64
}

func (s StTraceEvent) IsType(t SyncEventType) bool {
	return s.Type == int(t)
}

type stTrace struct {
	mu     mutex
	traces []StTraceEvent
}

func (s *stTrace) lock() {
	lock(&s.mu)
	if raceenabled {
		raceacquire(unsafe.Pointer(&s.mu))
	}
}

func (s *stTrace) unlock() {
	if raceenabled {
		racerelease(unsafe.Pointer(&s.mu))
	}
	unlock(&s.mu)
}

func (s *stTrace) Reset() {
	s.lock()
	s.traces = []StTraceEvent{}
	s.unlock()
}

func (s *stTrace) Get() []StTraceEvent {
	var result []StTraceEvent
	s.lock()
	result = make([]StTraceEvent, len(s.traces))
	copy(result, s.traces)
	s.unlock()

	return result
}

func (s *stTrace) append(event StTraceEvent) {
	s.lock()
	s.traces = append(s.traces, event)
	s.unlock()
}

func hasSuffix(target, suffix string) bool {
	szlhs := len(target)
	szrhs := len(suffix)
	return szlhs > szrhs && target[szlhs-szrhs:] == suffix
}

func isNotUserSpaceGoroutine(gp *g) bool {
	// Keep this in sync with cmd/trace/trace.go:isSystemGoroutine.
	f := findfunc(gp.startpc)
	if !f.valid() {
		return false
	}
	if f.funcID == funcID_runtime_main || f.funcID == funcID_handleAsyncEvent {
		return false
	}
	if f.funcID == funcID_runfinq {
		return !fingRunning
	}

	fn := funcname(f)
	if hasPrefix(fn, "runtime.") || hasPrefix(fn, "sync.") || hasPrefix(fn, "net.") {
		return true
	}

	pkgpath := funcpkgpath(f)
	return hasPrefix(pkgpath, "github.com/ting-playground")
}

func getGCallerInfo(gp *g, skip int) (file string, line int) {
	rpc := make([]uintptr, 1)
	n := gcallers(gp, 2, rpc)
	if n >= 1 {
		frame, _ := CallersFrames(rpc).Next()
		file, line = frame.File, frame.Line
	}
	return
}

func markNewproc(gp *g, newg *g) {
	if isSyncTraceDisabled() {
		return
	}

	file, line := getGCallerInfo(gp, 2)

	// We don't use race API here to avoid the runtime panic
	lock(&StTrace.mu)
	StTrace.traces = append(StTrace.traces, StTraceEvent{
		Goid: gp.goid,
		Now:  nanotime(),
		Type: int(NewProcEvent),
		Addr: unsafe.Pointer(gp),
		File: file,
		Line: line,
		Hold: newg.goid,
		// No need function name
	})
	unlock(&StTrace.mu)

	return
}

//go:nosplit
func markLastDeferReturn(skip int) {
	if isSyncTraceDisabled() {
		return
	}

	gp := getg()
	if gp.isNotUserSpaceG {
		return
	}

	goid := gp.goid

	pc, file, line, _ := Caller(skip)

	if hasSuffix(file, "asm_amd64.s") {
		return
	}

	StTrace.append(StTraceEvent{
		Goid: goid,
		Now:  nanotime(),
		Type: int(DeferReturnEvent),
		File: file,
		Line: line,
		Addr: unsafe.Pointer(FuncForPC(pc)),
	})
}

func markDeferEvent() {
	if isSyncTraceDisabled() {
		return
	}

	gp := getg()
	if gp.isNotUserSpaceG {
		return
	}

	goid := gp.goid

	pc, file, line, _ := Caller(2)

	if hasSuffix(file, "asm_amd64.s") {
		pc, file, line, _ = Caller(3)
	}

	StTrace.append(StTraceEvent{
		Goid: goid,
		Now:  nanotime(),
		Type: int(DeferEvent),
		File: file,
		Line: line,
		Addr: unsafe.Pointer(FuncForPC(pc)),
	})
}

func markSelectEvent(c *hchan, event SyncEventType, order int64) {
	if isSyncTraceDisabled() {
		return
	}

	gp := getg()
	if gp.isNotUserSpaceG {
		return
	}

	lock(&c.lock)
	if c.syncid <= 0 {
		unlock(&c.lock)
		return
	}
	unlock(&c.lock)

	goid := gp.goid

	var file string
	var line int

	_, file, line, _ = Caller(2)

	isCtxType := event == CtxDoneEvent || event == CtxCancelEvent

	// skip internal synchronizations in context
	if !isCtxType && hasSuffix(file, "src/context/context.go") {
		file, line = "", 0
	} else if hasSuffix(file, "asm_amd64.s") {
		_, file, line, _ = Caller(3)
	}

	if file != "" && line != 0 {
		StTrace.append(StTraceEvent{
			Goid: goid,
			Now:  nanotime(),
			Type: int(event),
			Addr: unsafe.Pointer(c),
			File: file,
			Line: line,
			Hold: order,
		})
	}
}

func markChanEvent(c *hchan, event SyncEventType, skip int) {
	if isSyncTraceDisabled() {
		return
	}

	gp := getg()
	if gp.isNotUserSpaceG {
		return
	}

	lock(&c.lock)
	if c.syncid <= 0 {
		unlock(&c.lock)
		return
	}
	unlock(&c.lock)

	goid := gp.goid
	_, file, line, _ := Caller(skip)

	if hasSuffix(file, "asm_amd64.s") {
		// if we are in runtime.goexit
		if line == 1581 {
			pc := getg().startpc
			file, line = FuncForPC(pc).FileLine(pc)
		} else {
			_, file, line, _ = Caller(skip + 1)
		}
	}

	if file != "" && line != 0 {
		StTrace.append(StTraceEvent{
			Goid: goid,
			Now:  nanotime(),
			Type: int(event),
			Addr: unsafe.Pointer(c),
			File: file,
			Line: line,
		})
	}
}

func MarkEvent(addr unsafe.Pointer, goid int64, event int, skip int) {
	if isSyncTraceDisabled() {
		return
	}

	gp := getg()
	if gp.isNotUserSpaceG {
		return
	}

	if goid == 0 {
		goid = gp.goid
	}

	_, file, line, _ := Caller(skip)

	eventType := SyncEventType(event)
	isCtxType := eventType == CtxDoneEvent || eventType == CtxCancelEvent

	// skip internal synchronizations in context
	if !isCtxType && hasSuffix(file, "src/context/context.go") {
		file, line = "", 0
	}

	var trapped bool
	if hasSuffix(file, "trapper/trap.go") {
		_, file, line, _ = Caller(skip + 1)
		trapped = true
	}

	if hasSuffix(file, "asm_amd64.s") {
		trapped = true
		// if we are in runtime.goexit
		if line == 1581 {
			pc := getg().startpc
			file, line = FuncForPC(pc).FileLine(pc)
		} else {
			_, file, line, _ = Caller(skip + 1)
		}
	}

	// we only append synchronizations trapped by trapper/trap.go
	if trapped && file != "" && line != 0 {
		StTrace.append(StTraceEvent{
			Goid: goid,
			Now:  nanotime(),
			Type: event,
			Addr: addr,
			File: file,
			Line: line,
		})
	}
}

type syncTrapperMap struct {
	enable bool
	ch     chan SyncSignal
}

func (s *syncTrapperMap) IsEnabled() bool {
	return s.enable
}

func (s *syncTrapperMap) Enable() {
	s.enable = true
}

func (s *syncTrapperMap) Disable() {
	s.enable = false
}

func (s *syncTrapperMap) Queued(id int64, isWakedUp *uint32) {
	s.ch <- SyncSignal{ID: id, IsWakedUp: isWakedUp}
}

func (s *syncTrapperMap) Notify() chan SyncSignal {
	return s.ch
}

type SyncSignal struct {
	ID        int64
	IsWakedUp *uint32
}

var SyncTrapperMap *syncTrapperMap = &syncTrapperMap{
	ch: make(chan SyncSignal),
}

func waitSched(c *hchan) {
	lock(&c.lock)
	id := c.syncid
	unlock(&c.lock)

	if id < 0 {
		return
	}

	isWakeUp := new(uint32)
	SyncTrapperMap.Queued(id, isWakeUp)
	for atomic.Load(isWakeUp) != 1 {
		timeSleep(1000000)
	}
}
