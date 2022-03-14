package runtime

import (
	"runtime/internal/atomic"
	"unsafe"
)

var SyncTraceEnable bool

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

func getGCallerInfo(gp *g, skip int) (file string, line int) {
	rpc := make([]uintptr, 1)
	n := gcallers(gp, 2, rpc)
	if n >= 1 {
		frame, _ := CallersFrames(rpc).Next()
		file, line = frame.File, frame.Line
	}

	return file, line
}

func markNewproc(gp *g, goid int64) {
	if !SyncTraceEnable {
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
		Hold: goid,
		// No need function name
	})
	unlock(&StTrace.mu)

	return
}

//go:nosplit
func markLastDeferReturn(skip int) {
	if !SyncTraceEnable {
		return
	}

	goid := getg().goid

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
	if !SyncTraceEnable {
		return
	}

	goid := getg().goid

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

func markSelectEvent(addr unsafe.Pointer, goid int64, event SyncEventType, order int64) {
	if !SyncTraceEnable {
		return
	}

	if goid == 0 {
		goid = getg().goid
	}

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
			Addr: addr,
			File: file,
			Line: line,
			Hold: order,
		})
	}
}

func MarkEvent(addr unsafe.Pointer, goid int64, event int, skip int) {
	if !SyncTraceEnable {
		return
	}

	if goid == 0 {
		goid = getg().goid
	}

	_, file, line, _ := Caller(skip)

	eventType := SyncEventType(event)
	isCtxType := eventType == CtxDoneEvent || eventType == CtxCancelEvent

	// skip internal synchronizations in context
	if !isCtxType && hasSuffix(file, "src/context/context.go") {
		file, line = "", 0
	}

	if hasSuffix(file, "asm_amd64.s") {
		// if we are in runtime.goexit
		if line == 1581 {
			file, line = getGCallerInfo(getg(), 1)
		} else {
			_, file, line, _ = Caller(skip + 1)
		}
	}

	if hasSuffix(file, "trapper/trap.go") {
		_, file, line, _ = Caller(skip + 1)
	}

	if file != "" && line != 0 {
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
	lock mutex
	data map[*hchan]int64

	enable bool
	ch     chan SyncSignal
}

func (s *syncTrapperMap) Lock() {
	lock(&s.lock)
	if raceenabled {
		raceacquire(unsafe.Pointer(&s.lock))
	}
}

func (s *syncTrapperMap) Unlock() {
	if raceenabled {
		racerelease(unsafe.Pointer(&s.lock))
	}
	unlock(&s.lock)
}

func (s *syncTrapperMap) Store(c *hchan, id int64) {
	s.Lock()
	s.data[c] = id
	s.Unlock()
}

func (s *syncTrapperMap) Load(c *hchan) int64 {
	s.Lock()
	id, exists := s.data[c]
	s.Unlock()
	if exists {
		return id
	}
	return -1
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

func (s *syncTrapperMap) Clear() {
	s.Lock()
	s.data = make(map[*hchan]int64)
	s.Unlock()
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
	data: make(map[*hchan]int64),
	ch:   make(chan SyncSignal),
}

func waitSched(c *hchan) {
	id := SyncTrapperMap.Load(c)
	if id == -1 {
		return
	}

	isWakeUp := new(uint32)
	SyncTrapperMap.Queued(id, isWakeUp)
	for atomic.Load(isWakeUp) != 1 {
		timeSleep(1000000)
	}
}
