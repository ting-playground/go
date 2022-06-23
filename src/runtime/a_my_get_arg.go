package runtime

import (
	"runtime/internal/sys"
	"unsafe"
)

func getFirstArg(f funcInfo, argp unsafe.Pointer) uint64 {
	// The "instruction" of argument printing is encoded in _FUNCDATA_ArgInfo.
	// See cmd/compile/internal/ssagen.emitArgInfo for the description of the
	// encoding.
	// These constants need to be in sync with the compiler.
	const (
		_endSeq         = 0xff
		_startAgg       = 0xfe
		_endAgg         = 0xfd
		_dotdotdot      = 0xfc
		_offsetTooLarge = 0xfb
	)

	const (
		limit    = 10                       // print no more than 10 args/components
		maxDepth = 5                        // no more than 5 layers of nesting
		maxLen   = (maxDepth*3+2)*limit + 1 // max length of _FUNCDATA_ArgInfo (see the compiler side for reasoning)
	)

	p := (*[maxLen]uint8)(funcdata(f, _FUNCDATA_ArgInfo))
	if p == nil {
		return 0
	}

	firstArg := func(off, sz uint8) uint64 {
		x := readUnaligned64(add(argp, uintptr(off)))
		// mask out irrelavant bits
		if sz < 8 {
			shift := 64 - sz*8
			if sys.BigEndian {
				x = x >> shift
			} else {
				x = x << shift >> shift
			}
		}
		return x
	}

	start := true
	printcomma := func() {
		if !start {
			print(", ")
		}
	}
	pi := 0
printloop:
	for {
		o := p[pi]
		pi++
		switch o {
		case _endSeq:
			break printloop
		case _startAgg:
			printcomma()
			print("{")
			start = true
			continue
		case _endAgg:
			print("}")
		case _dotdotdot:
			printcomma()
			print("...")
		case _offsetTooLarge:
			printcomma()
			print("_")
		default:
			printcomma()
			sz := p[pi]
			pi++
			return firstArg(o, sz)
		}
		start = false
	}
	return 0
}
