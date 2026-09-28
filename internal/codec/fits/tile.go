package fits

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/errs"
)

// Tile compression (https://fits.gsfc.nasa.gov/registry/tilecompression.html):
// the image is cut into tiles (ZTILEn, row by row by default), one binary
// table row per tile, whose COMPRESSED_DATA variable-length array points
// into the heap.

// Quantisation of floating-point tiles (ZQUANTIZ).
const (
	quantNone = iota
	quantNoDither
	quantDither1
	quantDither2
)

const (
	// nRandom is the length of the dither sequence.
	nRandom = 10000
	// zeroValue marks an exact 0.0 under SUBTRACTIVE_DITHER_2.
	zeroValue = -2147483646
	// defaultBlockSize and defaultBytePix are RICE_1's defaults.
	defaultBlockSize = 32
	defaultBytePix   = 4
)

// column is a binary table column.
type column struct {
	offset int  // within the row
	desc   byte // 'P' or 'Q' for variable-length arrays, else 0
	typ    byte // the element type letter (after P/Q for arrays)
}

// elemSize is the byte size of a table element type.
func elemSize(t byte) int {
	switch t {
	case 'L', 'B', 'A', 'X':
		return 1
	case 'I':
		return 2
	case 'J', 'E':
		return 4
	case 'K', 'D', 'C':
		return 8
	case 'M':
		return 16
	default:
		return 0
	}
}

// parseTFORM reads "rT", "rPT(max)" or "rQT(max)" and returns the column
// width in bytes.
func parseTFORM(s string) (column, int, error) {
	s = strings.TrimSpace(s)
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	r := 1
	if i > 0 {
		n, err := strconv.Atoi(s[:i])
		if err != nil || n > 1<<24 {
			return column{}, 0, corrupt("bad TFORM repeat")
		}
		r = n
	}
	if i >= len(s) {
		return column{}, 0, corrupt("bad TFORM")
	}
	var c column
	switch t := s[i]; t {
	case 'P', 'Q':
		if i+1 >= len(s) || elemSize(s[i+1]) == 0 {
			return column{}, 0, corrupt("bad TFORM array type")
		}
		c.desc, c.typ = t, s[i+1]
		if t == 'P' {
			return c, 8 * r, nil
		}
		return c, 16 * r, nil
	case 'X':
		c.typ = t
		return c, (r + 7) / 8, nil
	default:
		sz := elemSize(t)
		if sz == 0 {
			return column{}, 0, corrupt("bad TFORM type")
		}
		c.typ = t
		return c, sz * r, nil
	}
}

// table is the layout of a tile-compressed image's binary table.
type table struct {
	rowWidth, rows int64
	heap           int64 // THEAP: heap start within the data unit
	cols           map[string]column
}

func (h *header) table() (table, error) {
	var t table
	var err error
	ax, err := h.axes("NAXIS")
	if err != nil {
		return t, err
	}
	if len(ax) != 2 {
		return t, corrupt("binary table without two axes")
	}
	t.rowWidth, t.rows = ax[0], ax[1]
	tableSize, err := mul(t.rowWidth, t.rows)
	if err != nil {
		return t, err
	}
	if t.heap, err = h.integerOr("THEAP", tableSize); err != nil {
		return t, err
	}
	if t.heap < tableSize {
		return t, corrupt("THEAP inside the table")
	}
	n, err := h.integerOr("TFIELDS", 0)
	if err != nil {
		return t, err
	}
	if n < 1 || n > 999 {
		return t, corrupt("bad TFIELDS")
	}
	t.cols = make(map[string]column, n)
	off := 0
	for k := 1; k <= int(n); k++ {
		c, width, err := parseTFORM(h.str("TFORM" + strconv.Itoa(k)))
		if err != nil {
			return t, err
		}
		c.offset = off
		off += width
		if name := strings.ToUpper(h.str("TTYPE" + strconv.Itoa(k))); name != "" {
			if _, dup := t.cols[name]; !dup {
				t.cols[name] = c
			}
		}
	}
	if int64(off) > t.rowWidth {
		return t, corrupt("columns wider than a row")
	}
	return t, nil
}

// array returns the bytes of the variable-length array of column c in
// row r, and its element size.
func (t *table) array(data []byte, c column, r int64) ([]byte, int, error) {
	if c.desc == 0 {
		return nil, 0, corrupt("not an array column")
	}
	row := r*t.rowWidth + int64(c.offset)
	be := binary.BigEndian
	var n, off int64
	if c.desc == 'P' {
		if row+8 > int64(len(data)) {
			return nil, 0, corrupt("truncated table")
		}
		n, off = s32(be.Uint32(data[row:])), s32(be.Uint32(data[row+4:]))
	} else {
		if row+16 > int64(len(data)) {
			return nil, 0, corrupt("truncated table")
		}
		n, off = s64(be.Uint64(data[row:])), s64(be.Uint64(data[row+8:]))
	}
	size := elemSize(c.typ)
	if n < 0 || off < 0 || n > maxBytes/16 || off > maxBytes/2 {
		return nil, 0, corrupt("bad array descriptor")
	}
	start := t.heap + off
	end := start + n*int64(size)
	if end > int64(len(data)) || start > end {
		return nil, 0, corrupt("array outside the heap")
	}
	return data[start:end], size, nil
}

// scalar reads a numeric scalar column of row r.
func (t *table) scalar(data []byte, c column, r int64) (float64, error) {
	p := r*t.rowWidth + int64(c.offset)
	size := int64(elemSize(c.typ))
	if c.desc != 0 || p+size > int64(len(data)) {
		return 0, corrupt("bad scalar column")
	}
	be := binary.BigEndian
	switch c.typ {
	case 'B':
		return float64(data[p]), nil
	case 'I':
		return float64(s16(be.Uint16(data[p:]))), nil
	case 'J':
		return float64(s32(be.Uint32(data[p:]))), nil
	case 'K':
		return float64(s64(be.Uint64(data[p:]))), nil
	case 'E':
		return float64(math.Float32frombits(be.Uint32(data[p:]))), nil
	case 'D':
		return math.Float64frombits(be.Uint64(data[p:])), nil
	default:
		return 0, corrupt("bad scalar column type")
	}
}

// tileParams are the header keywords of a tile-compressed image.
type tileParams struct {
	cmp                string
	blockSize, bytePix int
	quant              int
	dither0            int64
	tile               [3]int64
	// Keyword forms of the per-tile values (used when there is no column).
	zscale, zzero float64
	hasZScale     bool
	zblank        int64
	hasZBlank     bool
}

func (h *header) tileParams(im *imageHDU, sc *scaling) (tileParams, error) {
	p := tileParams{
		cmp:       strings.ToUpper(h.str("ZCMPTYPE")),
		blockSize: defaultBlockSize,
		bytePix:   defaultBytePix,
		tile:      [3]int64{int64(im.Width), 1, 1},
	}
	switch p.cmp {
	case "RICE_1", "RICE_ONE":
		p.cmp = "RICE_1"
	case "GZIP_1", "GZIP_2", "NOCOMPRESS":
	case "HCOMPRESS_1":
		return p, errs.WithPublic(errs.New(errs.KindUnsupported, "fits: HCOMPRESS_1 tiles"),
			"FITS HCOMPRESS tile compression is not supported")
	case "PLIO_1":
		return p, errs.WithPublic(errs.New(errs.KindUnsupported, "fits: PLIO_1 tiles"),
			"FITS PLIO tile compression is not supported")
	default:
		return p, errs.WithPublic(errs.Newf(errs.KindUnsupported, "fits: tile compression %q", p.cmp),
			"this FITS tile compression is not supported")
	}
	for i := 1; i <= 999; i++ {
		name := strings.ToUpper(h.str("ZNAME" + strconv.Itoa(i)))
		if name == "" {
			break
		}
		v, ok, err := h.integer("ZVAL" + strconv.Itoa(i))
		if err != nil || !ok {
			continue
		}
		switch name {
		case "BLOCKSIZE":
			p.blockSize = int(min(max(v, 0), 1<<20))
		case "BYTEPIX":
			p.bytePix = int(min(max(v, 0), 8))
		}
	}
	dims := [3]int64{int64(im.Width), int64(im.Height), int64(im.Planes)}
	for k := range 3 {
		v, ok, err := h.integer("ZTILE" + strconv.Itoa(k+1))
		if err != nil {
			return p, err
		}
		if ok {
			if v < 1 {
				return p, corrupt("bad ZTILE")
			}
			p.tile[k] = min(v, dims[k])
		}
	}
	switch q := strings.ToUpper(h.str("ZQUANTIZ")); q {
	case "", "NO_DITHER":
		p.quant = quantNoDither
	case "SUBTRACTIVE_DITHER_1":
		p.quant = quantDither1
	case "SUBTRACTIVE_DITHER_2":
		p.quant = quantDither2
	case "NONE":
		p.quant = quantNone
	default:
		return p, errs.WithPublic(errs.Newf(errs.KindUnsupported, "fits: quantisation %q", q),
			"this FITS quantisation method is not supported")
	}
	var err error
	if p.dither0, err = h.integerOr("ZDITHER0", 1); err != nil {
		return p, err
	}
	if p.zscale, p.hasZScale, err = h.real("ZSCALE"); err != nil {
		return p, err
	}
	if p.zzero, err = h.realOr("ZZERO", 0); err != nil {
		return p, err
	}
	if p.zblank, p.hasZBlank, err = h.integer("ZBLANK"); err != nil {
		return p, err
	}
	if !p.hasZBlank && sc.hasBlank {
		p.zblank, p.hasZBlank = sc.blank, true
	}
	return p, nil
}

// randoms is the dither sequence of the tile-compression convention
// (fits_init_randoms): a Park-Miller generator seeded with 1.
func randoms() []float32 {
	r := make([]float32, nRandom)
	const a, m = 16807.0, 2147483647.0
	seed := 1.0
	for i := range r {
		t := a * seed
		seed = t - m*math.Trunc(t/m)
		r[i] = float32(seed / m)
	}
	return r
}

// decodeTiles decompresses the tiles of the drawn planes into physical
// values.
func decodeTiles(im *imageHDU, sc *scaling) (samples, error) {
	h := &im.h
	t, err := h.table()
	if err != nil {
		return nil, err
	}
	p, err := h.tileParams(im, sc)
	if err != nil {
		return nil, err
	}
	cdata, ok := t.cols["COMPRESSED_DATA"]
	if !ok || cdata.desc == 0 {
		return nil, corrupt("no COMPRESSED_DATA column")
	}
	gzCol, hasGz := t.cols["GZIP_COMPRESSED_DATA"]
	rawCol, hasRaw := t.cols["UNCOMPRESSED_DATA"]
	zsCol, hasZS := t.cols["ZSCALE"]
	zzCol, hasZZ := t.cols["ZZERO"]
	zbCol, hasZB := t.cols["ZBLANK"]
	hasZB = hasZB && zbCol.desc == 0

	w, hgt := int64(im.Width), int64(im.Height)
	drawn := int64(im.drawn())
	nt := [3]int64{
		(w + p.tile[0] - 1) / p.tile[0],
		(hgt + p.tile[1] - 1) / p.tile[1],
		(int64(im.Planes) + p.tile[2] - 1) / p.tile[2],
	}
	perPlane := nt[0] * nt[1]
	// Only the tiles of the drawn planes are read.
	need := perPlane * ((drawn + p.tile[2] - 1) / p.tile[2])
	if need > t.rows {
		return nil, corrupt("fewer tiles than the image needs")
	}
	floatImg := im.Bitpix < 0
	quantised := floatImg && p.quant != quantNone && (hasZS || p.hasZScale)
	var rnd []float32
	if quantised && (p.quant == quantDither1 || p.quant == quantDither2) {
		rnd = randoms()
	}
	n := w * hgt
	out := make([]float32, drawn*n)
	for i := range out {
		out[i] = float32(math.NaN())
	}
	for r := range need {
		tx, ty, tz := r%nt[0], (r/nt[0])%nt[1], r/perPlane
		x0, y0, z0 := tx*p.tile[0], ty*p.tile[1], tz*p.tile[2]
		tw, th, td := min(p.tile[0], w-x0), min(p.tile[1], hgt-y0), min(p.tile[2], int64(im.Planes)-z0)
		count := int(tw * th * td)
		vals, isFloat, err := p.decodeTile(im, &t, cdata, gzCol, hasGz, rawCol, hasRaw, r, count, quantised)
		if err != nil {
			return nil, err
		}
		if vals == nil {
			continue // an empty tile: no data
		}
		zscale, zzero := p.zscale, p.zzero
		if hasZS {
			if zscale, err = t.scalar(im.data, zsCol, r); err != nil {
				return nil, err
			}
		}
		if hasZZ {
			if zzero, err = t.scalar(im.data, zzCol, r); err != nil {
				return nil, err
			}
		}
		blank, hasBlank := p.zblank, p.hasZBlank
		if hasZB {
			b, err := t.scalar(im.data, zbCol, r)
			if err != nil {
				return nil, err
			}
			blank, hasBlank = int64(b), true
		}
		iseed, next := 0, 0
		if rnd != nil {
			iseed = int(((r+p.dither0-1)%nRandom + nRandom) % nRandom)
			next = int(rnd[iseed] * 500)
		}
		k := 0
		for z := range td {
			for y := range th {
				for x := range tw {
					plane := z0 + z
					var v float64
					switch {
					case isFloat:
						v = vals.f[k]
					case quantised:
						iv := vals.i[k]
						switch {
						case hasBlank && iv == blank:
							v = math.NaN()
						case p.quant == quantDither2 && iv == zeroValue:
							v = 0
						case rnd != nil:
							v = (float64(iv)-float64(rnd[next])+0.5)*zscale + zzero
						default:
							v = float64(iv)*zscale + zzero
						}
					default:
						iv := vals.i[k]
						if hasBlank && iv == blank {
							v = math.NaN()
						} else {
							v = sc.zero + sc.scale*float64(iv)
						}
					}
					if rnd != nil {
						next++
						if next == nRandom {
							iseed = (iseed + 1) % nRandom
							next = int(rnd[iseed] * 500)
						}
					}
					k++
					if plane < drawn {
						out[plane*n+(y0+y)*w+x0+x] = float32(v)
					}
				}
			}
		}
	}
	return func(pl, i int) float64 { return float64(out[pl*int(n)+i]) }, nil
}

// tileValues are the decoded values of one tile: integers, or floats for
// lossless float tiles.
type tileValues struct {
	i []int64
	f []float64
}

// decodeTile decompresses tile r into count values.
func (p *tileParams) decodeTile(im *imageHDU, t *table, cdata, gzCol column, hasGz bool, rawCol column, hasRaw bool,
	r int64, count int, quantised bool,
) (*tileValues, bool, error) {
	data, esize, err := t.array(im.data, cdata, r)
	if err != nil {
		return nil, false, err
	}
	floatImg := im.Bitpix < 0
	if len(data) == 0 {
		// Floating-point tiles that could not be quantised are stored
		// losslessly (gzip) or uncompressed in their own column.
		switch {
		case hasGz:
			gz, _, err := t.array(im.data, gzCol, r)
			if err != nil {
				return nil, false, err
			}
			if len(gz) > 0 {
				size := abs64(int64(im.Bitpix)) / 8
				b, err := gunzip(gz, count*int(size))
				if err != nil {
					return nil, false, err
				}
				if p.cmp == "GZIP_2" {
					b = unshuffle(b, int(size))
				}
				v, err := beValues(b, int(size), floatImg, count)
				return v, floatImg, err
			}
		case hasRaw:
			raw, size, err := t.array(im.data, rawCol, r)
			if err != nil {
				return nil, false, err
			}
			if len(raw) > 0 {
				v, err := beValues(raw, size, rawCol.typ == 'E' || rawCol.typ == 'D', count)
				return v, rawCol.typ == 'E' || rawCol.typ == 'D', err
			}
		}
		return nil, false, nil
	}
	// Integer element size of the compressed stream: 4 for quantised
	// floats, else the image's.
	size := int(abs64(int64(im.Bitpix)) / 8)
	if quantised {
		size = 4
	}
	switch p.cmp {
	case "RICE_1":
		if floatImg && !quantised {
			return nil, false, corrupt("RICE_1 on unquantised floats")
		}
		v, err := riceDecode(data, count, p.bytePix, p.blockSize)
		if err != nil {
			return nil, false, err
		}
		return &tileValues{i: v}, false, nil
	case "GZIP_1", "GZIP_2":
		b, err := gunzip(data, count*size)
		if err != nil {
			return nil, false, err
		}
		if p.cmp == "GZIP_2" {
			b = unshuffle(b, size)
		}
		isF := floatImg && !quantised
		v, err := beValues(b, size, isF, count)
		return v, isF, err
	default: // NOCOMPRESS: the values are the array's elements
		isF := cdata.typ == 'E' || cdata.typ == 'D'
		v, err := beValues(data, esize, isF, count)
		return v, isF, err
	}
}

// gunzip inflates a gzip (or zlib-less deflate in a gzip wrapper) stream
// of exactly want bytes.
func gunzip(b []byte, want int) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, errs.Wrapf(errs.KindCorrupt, err, "fits: gzip tile")
	}
	out := make([]byte, want)
	if _, err := io.ReadFull(zr, out); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			return nil, corrupt("gzip tile too short")
		}
		return nil, errs.Wrapf(errs.KindCorrupt, err, "fits: gzip tile")
	}
	return out, nil
}

// unshuffle undoes GZIP_2's byte shuffling: all first bytes of the values,
// then all second bytes, and so on.
func unshuffle(b []byte, size int) []byte {
	if size <= 1 {
		return b
	}
	n := len(b) / size
	out := make([]byte, len(b))
	for j := range size {
		for i := range n {
			out[i*size+j] = b[j*n+i]
		}
	}
	return out
}

// beValues reads count big-endian values of size bytes: floats (4 or 8
// bytes) or integers (bytes unsigned, others signed).
func beValues(b []byte, size int, float bool, count int) (*tileValues, error) {
	if size < 1 || len(b) < count*size {
		return nil, corrupt("tile too short")
	}
	be := binary.BigEndian
	v := &tileValues{}
	if float {
		v.f = make([]float64, count)
		for k := range count {
			switch size {
			case 4:
				v.f[k] = float64(math.Float32frombits(be.Uint32(b[k*4:])))
			case 8:
				v.f[k] = math.Float64frombits(be.Uint64(b[k*8:]))
			default:
				return nil, corrupt("bad float size")
			}
		}
		return v, nil
	}
	v.i = make([]int64, count)
	for k := range count {
		switch size {
		case 1:
			v.i[k] = int64(b[k])
		case 2:
			v.i[k] = s16(be.Uint16(b[k*2:]))
		case 4:
			v.i[k] = s32(be.Uint32(b[k*4:]))
		case 8:
			v.i[k] = s64(be.Uint64(b[k*8:]))
		default:
			return nil, corrupt("bad integer size")
		}
	}
	return v, nil
}
