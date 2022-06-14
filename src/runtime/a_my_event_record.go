package runtime

import (
	"runtime/internal/atomic"
	"unsafe"
)

var SyncTraceEnable uint8
var untracks []uint32
var isShallowMemoryEnabled uint8

func AllocShallowMemory() {
	untracks = make([]uint32, 1<<32-1)
	atomic.Store8(&isShallowMemoryEnabled, 1)
}

func EnableSyncTracing() {
	atomic.Store8(&SyncTraceEnable, 1)
}

func DisableSyncTracing() {
	atomic.Store8(&SyncTraceEnable, 0)
}

func isSyncTraceDisabled() bool {
	return atomic.Load8(&SyncTraceEnable) == 0
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
	Goid     int64
	Now      int64
	Type     SyncEventType
	Addr     unsafe.Pointer
	File     string
	Line     int
	Metadata int64
}

type stTrace struct {
	mu     mutex
	traces []StTraceEvent
}

func gethash(gp uintptr, t SyncEventType) uint32 {
	list := []byte{uint8(t)}
	list = append(list, byte(gp>>56), byte(gp>>48), byte(gp>>40), byte(gp>>32))
	list = append(list, byte(gp>>24), byte(gp>>16), byte(gp>>8), byte(gp))
	return fnv1(0, list...)
}

func fnv1(x uint32, list ...byte) uint32 {
	for _, b := range list {
		x = x*16777619 ^ uint32(b)
	}
	return x
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
	if hasPrefix(fn, "runtime.") {
		return true
	}
	/*
		if hasPrefix(fn, "sync.") {
			return true
		}

		if hasPrefix(fn, "net.") || hasPrefix(fn, "tls.") || hasPrefix(fn, "time.") {
			return true
		}
	*/

	pkgpath := funcpkgpath(f)
	if hasPrefix(pkgpath, "github.com/ting-playground") {
		return true
	}

	return false

	// return hasPrefix(pkgpath, "go/") || hasPrefix(pkgpath, "golang.org")
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

func recordNewproc(gp *g, newg *g) {
	if isSyncTraceDisabled() {
		return
	}

	if tooFrequency(gp, NewProcEvent) {
		return
	}

	file, line := getGCallerInfo(gp, 2)

	// We don't use race API here to avoid the runtime panic
	lock(&StTrace.mu)
	StTrace.traces = append(StTrace.traces, StTraceEvent{
		Goid:     gp.goid,
		Now:      nanotime(),
		Type:     NewProcEvent,
		Addr:     unsafe.Pointer(gp),
		File:     file,
		Line:     line,
		Metadata: newg.goid,
		// No need function name
	})
	unlock(&StTrace.mu)

	return
}

//go:nosplit
func recordLastDeferReturn(skip int) {
	if isSyncTraceDisabled() {
		return
	}

	gp := getg()
	if gp.isNotUserSpaceG {
		return
	}

	if tooFrequency(gp, DeferReturnEvent) {
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
		Type: DeferReturnEvent,
		File: file,
		Line: line,
		Addr: unsafe.Pointer(FuncForPC(pc)),
	})
}

func recordDeferEvent() {
	if isSyncTraceDisabled() {
		return
	}

	gp := getg()
	if gp.isNotUserSpaceG {
		return
	}

	if tooFrequency(gp, DeferEvent) {
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
		Type: DeferEvent,
		File: file,
		Line: line,
		Addr: unsafe.Pointer(FuncForPC(pc)),
	})
}

func recordSelectEvent(c *hchan, event SyncEventType, order int64) {
	if isSyncTraceDisabled() {
		return
	}

	gp := getg()
	if gp.isNotUserSpaceG {
		return
	}

	if c != nil {
		lock(&c.lock)
		if c.syncid <= 0 {
			unlock(&c.lock)
			return
		}
		unlock(&c.lock)
	}

	if tooFrequency(gp, event) {
		return
	}

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
			Goid:     goid,
			Now:      nanotime(),
			Type:     event,
			Addr:     unsafe.Pointer(c),
			File:     file,
			Line:     line,
			Metadata: order,
		})
	}
}

func recordChanEvent(c *hchan, event SyncEventType, skip int) {
	if isSyncTraceDisabled() {
		return
	}

	gp := getg()
	if gp.isNotUserSpaceG {
		return
	}

	var metadata int64
	if c != nil {
		lock(&c.lock)
		metadata = int64(c.dataqsiz)
		if c.syncid <= 0 {
			unlock(&c.lock)
			return
		}
		unlock(&c.lock)
	} else {
		metadata = -1
	}

	if tooFrequency(gp, event) {
		return
	}

	goid := gp.goid
	_, file, line, _ := Caller(skip)

	if hasSuffix(file, "trapper/trap.go") {
		_, file, line, _ = Caller(skip + 1)
	}

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
			Goid:     goid,
			Now:      nanotime(),
			Type:     event,
			Addr:     unsafe.Pointer(c),
			File:     file,
			Line:     line,
			Metadata: metadata,
		})
	}
}

func findCallTrap(skip int) (file string, line int) {
	pc := make([]uintptr, 10)
	n := Callers(skip, pc)
	if n < 1 {
		return
	}

	pc = pc[:n]
	frames := CallersFrames(pc)
	for i := 0; i < 3; i++ {
		frame, more := frames.Next()

		file, line = frame.File, frame.Line
		if hasSuffix(file, "trapper/trap.go") {
			frame, _ = frames.Next()
			return frame.File, frame.Line
		}

		if hasSuffix(file, "asm_amd64.s") {
			// if we are in runtime.goexit
			if line == 1581 {
				pc := getg().startpc
				return FuncForPC(pc).FileLine(pc)
			}

			frame, _ = frames.Next()
			return frame.File, frame.Line
		}

		if !more {
			break
		}
	}
	return
}

func RecordEvent(addr unsafe.Pointer, event SyncEventType, skip int) {
	if isSyncTraceDisabled() {
		return
	}

	gp := getg()
	if gp.isNotUserSpaceG {
		return
	}

	if tooFrequency(gp, event) {
		return
	}

	goid := gp.goid
	file, line := findCallTrap(skip)

	// we only append synchronizations trapped by trapper/trap.go
	if file != "" && line != 0 {
		StTrace.append(StTraceEvent{
			Goid: goid,
			Now:  nanotime(),
			Type: event,
			Addr: addr,
			File: file,
			Line: line,
		})
		return
	}

	return
}

func tooFrequency(gp *g, t SyncEventType) bool {
	if atomic.Load8(&isShallowMemoryEnabled) == 0 {
		return false
	}

	hash := gethash(uintptr(unsafe.Pointer(gp)), t)
	k := atomic.Load(&untracks[hash])
	if k > 256 {
		return true
	}
	atomic.Store(&untracks[hash], uint32(k+1))
	return false
}

type syncTrapperMap struct {
	enable uint8
	ch     chan SyncSignal
}

func (s *syncTrapperMap) IsEnabled() bool {
	return atomic.Load8(&s.enable) == 1
}

func (s *syncTrapperMap) Enable() {
	atomic.Store8(&s.enable, 1)
}

func (s *syncTrapperMap) Disable() {
	atomic.Store8(&s.enable, 0)
}

func (s *syncTrapperMap) send(id int64, isWakedUp *uint32) bool {
	if atomic.Load8(&s.enable) == 1 {
		s.ch <- SyncSignal{ID: id, IsWakedUp: isWakedUp}
		return true
	}

	return false
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
	if c == nil {
		return
	}

	lock(&c.lock)
	id := c.syncid
	unlock(&c.lock)

	if id <= 0 {
		return
	}

	isWakeUp := new(uint32)
	if SyncTrapperMap.send(id, isWakeUp) {
		for atomic.Load(isWakeUp) != 1 {
			Gosched()
		}
	}
}

type fastRandSource struct {
	lock     mutex
	fastrand [2]uint32
}

var globalFRS *fastRandSource = &fastRandSource{
	fastrand: [2]uint32{0, 1},
}

func FastRandSeed(lhs, rhs uint64, seed uintptr) {
	lock(&globalFRS.lock)
	globalFRS.fastrand[0] = uint32(int64Hash(lhs, seed))
	globalFRS.fastrand[1] = uint32(int64Hash(rhs, ^seed))
	if globalFRS.fastrand[0]|globalFRS.fastrand[1] == 0 {
		globalFRS.fastrand[1] = 1
	}
	unlock(&globalFRS.lock)
}

func FastRand() uint32 {
	lock(&globalFRS.lock)
	s1, s0 := globalFRS.fastrand[0], globalFRS.fastrand[1]
	s1 ^= s1 << 17
	s1 = s1 ^ s0 ^ s1>>7 ^ s0>>16
	globalFRS.fastrand[0], globalFRS.fastrand[1] = s0, s1
	unlock(&globalFRS.lock)

	return s0 + s1
}

func FastRandN(n uint32) uint32 {
	// This is similar to fastrand() % n, but faster.
	// See https://lemire.me/blog/2016/06/27/a-fast-alternative-to-the-modulo-reduction/
	return uint32(uint64(FastRand()) * uint64(n) >> 32)
}
