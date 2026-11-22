#!/usr/bin/env python3
"""Minimal Android sparse image pack/unpack for Amlogic rootfs images."""
import struct
import sys

MAGIC = 0xED26FF3A
RAW = 0xCAC1
FILL = 0xCAC2
DONT_CARE = 0xCAC3
CRC32 = 0xCAC4


def unpack(src: str, dst: str) -> None:
    with open(src, "rb") as f:
        hdr = f.read(28)
        magic, major, minor, file_hdr_sz, chunk_hdr_sz, blk_sz, total_blks, total_chunks, checksum = struct.unpack(
            "<IHHHHIIII", hdr
        )
        if magic != MAGIC:
            raise SystemExit(f"not an Android sparse image: {src}")
        with open(dst, "wb") as out:
            for _ in range(total_chunks):
                ch = f.read(chunk_hdr_sz)
                typ, _reserved, chunk_sz, _total_sz = struct.unpack("<HHII", ch)
                data_len = chunk_sz * blk_sz
                if typ == RAW:
                    left = data_len
                    while left:
                        buf = f.read(min(1024 * 1024, left))
                        if not buf:
                            raise SystemExit("truncated sparse image")
                        out.write(buf)
                        left -= len(buf)
                elif typ == FILL:
                    fill = f.read(4)
                    out.write(fill * chunk_sz)
                elif typ == DONT_CARE:
                    out.write(b"\0" * data_len)
                elif typ == CRC32:
                    f.read(4)
                else:
                    raise SystemExit(f"unsupported sparse chunk type: {typ:#x}")
    print(f"unpacked {src} -> {dst}")


def pack(src: str, dst: str) -> None:
    with open(src, "rb") as f:
        data = f.read()
    blk_sz = 4096
    if len(data) % blk_sz:
        data += b"\0" * (blk_sz - len(data) % blk_sz)
    total_blks = len(data) // blk_sz
    header = struct.pack("<IHHHHIIII", MAGIC, 1, 0, 28, 12, blk_sz, total_blks, 1, 0)
    chunk = struct.pack("<HHII", RAW, 0, total_blks, 12 + len(data))
    with open(dst, "wb") as f:
        f.write(header)
        f.write(chunk)
        f.write(data)
    print(f"packed {src} -> {dst} ({total_blks} blocks)")


def main() -> None:
    if len(sys.argv) != 4 or sys.argv[1] not in {"pack", "unpack"}:
        raise SystemExit(f"usage: {sys.argv[0]} pack|unpack <src> <dst>")
    if sys.argv[1] == "pack":
        pack(sys.argv[2], sys.argv[3])
    else:
        unpack(sys.argv[2], sys.argv[3])


if __name__ == "__main__":
    main()
