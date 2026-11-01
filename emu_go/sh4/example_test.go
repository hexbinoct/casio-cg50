package sh4_test

import (
	"context"
	"fmt"
	"strings"

	"cg50emu/sh4"
)

func ExampleDecode() {
	// mov.l @(0x4,pc),r1 ; jsr @r1 ; nop ; (pad) ; .long 0x80020070
	code := []byte{0xd1, 0x01, 0x41, 0x0b, 0x00, 0x09, 0x00, 0x09, 0x80, 0x02, 0x00, 0x70}
	img := sh4.NewImage(0x00300000, code)
	for a := img.Base; a < img.Base+8; a += 2 {
		in, _ := img.Decode(a)
		line := fmt.Sprintf("%08x  %-22s %v delay=%v %s", in.Addr, in.Text, in.Kind, in.Delayed,
			sh4.Comment(img.Word, nil, &in))
		fmt.Println(strings.TrimSpace(line))
	}
	// Output:
	// 00300000  mov.l @(0x4,pc),r1     literal delay=false =0x80020070
	// 00300002  jsr @r1                call-reg delay=true -> syscall
	// 00300004  nop                    normal delay=false
	// 00300006  nop                    normal delay=false
}

func ExampleAnalyze() {
	// start: bsr f ; nop ; rts ; nop     f: rts ; mov #1,r0
	code := []byte{0xb0, 0x02, 0x00, 0x09, 0x00, 0x0b, 0x00, 0x09, 0x00, 0x0b, 0xe0, 0x01}
	img := sh4.NewImage(0x00300000, code)
	an, err := sh4.Analyze(context.Background(), img, sh4.AddinOptions(img, nil))
	if err != nil {
		panic(err)
	}
	for _, f := range an.Funcs() {
		fmt.Printf("%s at %08x, %d bytes\n", an.FuncName(f.Addr), f.Addr, f.Size)
		for _, x := range an.XrefsTo(f.Addr) {
			fmt.Printf("  %v from %08x\n", x.Kind, x.From)
		}
	}
	// Output:
	// start at 00300000, 8 bytes
	// sub_00300008 at 00300008, 4 bytes
	//   call from 00300000
}
