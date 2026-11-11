package main

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

var sink [][]byte

func main() {
	for i := 0; i < 200000; i++ {
		sink = append(sink, make([]byte, 1024))
		if len(sink) > 1000 {
			sink = sink[:0]
		}
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	gc := debug.SetGCPercent(100)
	fmt.Printf("memlimit=%d gogc=%d gomaxprocs=%d numgc=%d gccpu=%.3f\n", debug.SetMemoryLimit(-1), gc, runtime.GOMAXPROCS(0), m.NumGC, m.GCCPUFraction)
}
