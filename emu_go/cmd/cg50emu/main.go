// Command cg50emu runs the emulator from the command line (diagnostic modes, the plain web UI):
//
//	go -C emu_go run ./cmd/cg50emu [maxIns] [timerPeriod] [mode]
package main

import "cg50emu"

func main() { cg50emu.Main() }
