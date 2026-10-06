package app

import (
	"bufio"
	"encoding/binary"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"testing"

	"github.com/xantiagoma/xtandard-image/internal/config"
	"github.com/xantiagoma/xtandard-image/internal/video/ffmpeg"
)

// writeAVI writes an uncompressed AVI (24-bit BGR frames, 25 fps) of n
// frames of w × h: a moving gradient, so the encoder has real work, of
// n × w × h × 3 bytes (FFmpeg's avi demuxer and rawvideo decoder read it).
func writeAVI(tb testing.TB, path string, w, h, n int) int64 {
	tb.Helper()
	f, err := os.Create(path)
	if err != nil {
		tb.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	bw := bufio.NewWriterSize(f, 1<<20)
	le := binary.LittleEndian
	u32 := func(v int) []byte { return le.AppendUint32(nil, uint32(v)) } //nolint:gosec // G115: header sizes fit
	frame := w * h * 3                                                   // rows of 4-byte multiples for w%4 == 0
	put := func(b ...[]byte) {
		for _, p := range b {
			if _, err := bw.Write(p); err != nil {
				tb.Fatal(err)
			}
		}
	}
	avih := make([]byte, 0, 56)
	for _, v := range []int{40000, frame * 25, 0, 0x10, n, 0, 1, frame, w, h, 0, 0, 0, 0} {
		avih = append(avih, u32(v)...)
	}
	strh := append([]byte("vids"), make([]byte, 4)...)
	for _, v := range []int{0, 0, 0, 1, 25, 0, n, frame, -1 & 0xFFFFFFFF, 0} {
		strh = append(strh, u32(v)...)
	}
	strh = append(strh, make([]byte, 8)...) // rcFrame
	strf := make([]byte, 0, 40)
	for _, v := range []int{40, w, h} {
		strf = append(strf, u32(v)...)
	}
	strf = append(strf, 1, 0, 24, 0) // planes, bit count
	for _, v := range []int{0, frame, 0, 0, 0, 0} {
		strf = append(strf, u32(v)...)
	}
	chunk := func(id string, body []byte) []byte { return append(append([]byte(id), u32(len(body))...), body...) }
	strl := append([]byte("strl"), append(chunk("strh", strh), chunk("strf", strf)...)...)
	hdrl := append([]byte("hdrl"), append(chunk("avih", avih), chunk("LIST", strl)...)...)
	movi := 4 + n*(8+frame)
	idx := n * 16
	riff := 4 + 8 + len(hdrl) + 8 + movi + 8 + idx
	put([]byte("RIFF"), u32(riff), []byte("AVI "), chunk("LIST", hdrl), []byte("LIST"), u32(movi), []byte("movi"))
	row := make([]byte, w*3)
	for i := range n {
		put([]byte("00db"), u32(frame))
		for y := range h {
			for x := range w {
				row[3*x], row[3*x+1], row[3*x+2] = byte(x+4*i), byte(y+2*i), byte((x^y)+i) //nolint:gosec // G115: wrapping is the pattern
			}
			put(row)
		}
	}
	put([]byte("idx1"), u32(idx))
	for i := range n {
		put([]byte("00db"), u32(0x10), u32(4+i*(8+frame)), u32(frame))
	}
	if err := bw.Flush(); err != nil {
		tb.Fatal(err)
	}
	return int64(12 + 8 + len(hdrl) + 8 + movi + 8 + idx)
}

// peakRSS is the process's peak resident set size in MiB.
func peakRSS() float64 {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	if runtime.GOOS == "darwin" {
		return float64(ru.Maxrss) / (1 << 20) // bytes
	}
	return float64(ru.Maxrss) / (1 << 10) // KiB
}

// BenchmarkVideoSpool transcodes a ~100 MB source (an uncompressed AVI
// generated here) end to end, with sources and outputs in memory or in
// spool files, and reports the process's peak RSS. Run each variant in a
// process of its own (the peak is per process):
//
//	go test -c -o app.test ./internal/app
//	for i in $(seq 10); do ./app.test -test.run '^$' -test.bench 'VideoSpool/memory' -test.benchtime 3x -test.benchmem; done
//
// (and the same for VideoSpool/spool), then benchstat the two outputs.
// The origin and result caches are off, so every iteration downloads,
// transcodes and serves.
func BenchmarkVideoSpool(b *testing.B) {
	if !ffmpeg.Available() {
		b.Skip("this image has no FFmpeg")
	}
	dir := b.TempDir()
	size := writeAVI(b, filepath.Join(dir, "big.avi"), 640, 360, 150)
	for _, c := range []struct {
		name      string
		threshold int64
	}{
		{"memory", 0},
		{"spool", 1 << 20},
	} {
		b.Run(c.name, func(b *testing.B) {
			cfg := videoConfig(b, config.RoleAll, true)
			cfg.Sources["fixtures"] = config.Source{Type: config.SourceFS, Root: dir}
			cfg.Cache.Origin.MemoryBytes = 0
			cfg.Cache.Result.MemoryBytes = 0
			cfg.Video.Transform.SpoolThreshold = config.ByteSize(c.threshold)
			cfg.Video.Transform.Workers = 1
			a := start(b, cfg, hooks{keepVips: true})
			u := "http://" + a.public.String() + "/video/unsafe/w:640/q:100/plain/big.avi@webm"
			var out int
			base := peakRSS()
			b.ReportAllocs()
			b.SetBytes(size)
			for b.Loop() {
				r := get(b, u)
				if r.status != http.StatusOK {
					b.Fatalf("%d %s", r.status, r.body)
				}
				out = len(r.body)
			}
			b.ReportMetric(peakRSS(), "peak-RSS-MiB")
			b.ReportMetric(peakRSS()-base, "RSS-growth-MiB") // over the started service's peak
			b.ReportMetric(float64(out)/(1<<20), "out-MiB")
			b.Logf("source %s bytes, output %d bytes", strconv.FormatInt(size, 10), out)
		})
	}
}
