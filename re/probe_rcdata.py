#!/usr/bin/env python3
"""Characterize the RCDATA OS blobs: entropy + try standard decompressors."""
import os, math, collections, zlib, struct

R = r"F:\ru\myprojects\may\cg50\os\pe2_rsrc\.rsrc\1033\RCDATA"
BLOBS = ["3069", "3070", "3071"]

def entropy(b):
    c=collections.Counter(b); n=len(b)
    return -sum((v/n)*math.log2(v/n) for v in c.values())

def try_zlib(b, label):
    # try raw deflate and zlib at offsets 0..8
    import zlib
    for off in range(0,9):
        for wbits in (15, -15, 31, 47):
            try:
                d=zlib.decompressobj(wbits)
                out=d.decompress(b[off:], 4096)
                if len(out)>=512:
                    return f"{label}: decompressed >=512B at off={off} wbits={wbits} (first16={out[:16].hex()})"
            except Exception:
                pass
    return None

for name in BLOBS:
    p=os.path.join(R,name); b=open(p,"rb").read()
    e=entropy(b)
    bc=collections.Counter(b)
    print(f"\nRCDATA/{name}: {len(b)} bytes  entropy={e:.3f}/8.0  distinct_bytes={len(bc)}")
    print(f"  first 32: {b[:32].hex()}")
    print(f"  last  16: {b[-16:].hex()}")
    # plausible decompressed-size header? read a few LE/BE u32 at start
    le=[struct.unpack_from('<I',b,o)[0] for o in (0,4,8,12)]
    be=[struct.unpack_from('>I',b,o)[0] for o in (0,4,8,12)]
    print(f"  u32 LE@0,4,8,12: {[hex(x) for x in le]}")
    print(f"  u32 BE@0,4,8,12: {[hex(x) for x in be]}")
    z=try_zlib(b, name)
    print("  zlib/deflate:", z if z else "no standard zlib/deflate match")
    # quick check for gzip/lzma/lz4/bzip2 magics anywhere near start
    for magic,nm in [(b"\x1f\x8b","gzip"),(b"\x42\x5a\x68","bzip2"),
                     (b"\xfd7zXZ","xz"),(b"\x04\x22\x4d\x18","lz4"),(b"\x5d\x00\x00","lzma")]:
        if b[:64].find(magic)!=-1: print(f"  found {nm} magic near start @ {b[:64].find(magic)}")
