package types

import (
	"bufio"
	"io"
	"io/ioutil"
)

type Reader struct {
	r   io.Reader
	b   [16]byte
	n   int
	err error
}

func NewReader(r io.Reader, n int) *Reader {
	return &Reader{r: r, n: n}
}

func (r *Reader) Err() error {
	return r.err
}

func (r *Reader) Remain() int {
	return r.n
}

func (r *Reader) Reset(x io.Reader) {
	r.r = x
	r.b = [16]byte{}
	r.n = 0
	r.err = nil
}

func (r *Reader) Limit(n int) {
	r.n = n
}

func (r *Reader) ReadByte() byte {
	if r.readFull(r.b[:1]) == 1 {
		return r.b[0]
	}
	return 0
}

func (r *Reader) ReadInt8() int8 {
	return int8(r.ReadByte())
}

func (r *Reader) ReadInt16() int16 {
	if r.readFull(r.b[:2]) == 2 {
		return makeInt16(r.b[:2])
	}
	return 0
}

func (r *Reader) ReadInt32() int32 {
	if r.readFull(r.b[:4]) == 4 {
		return makeInt32(r.b[:4])
	}
	return 0
}

func (r *Reader) ReadInt64() int64 {
	if r.readFull(r.b[:8]) == 8 {
		return makeInt64(r.b[:8])
	}
	return 0
}

func (r *Reader) ReadVarInt() int64 {
	x := uint64(0)
	s := uint(0)

	for {
		b := r.ReadByte()

		if b < 0x80 {
			if r.err != nil {
				return 0
			}
			x |= uint64(b) << s
			return int64(x>>1) ^ -(int64(x) & 1)
		}

		x |= uint64(b&0x7F) << s
		s += 7
	}
}

func (r *Reader) ReadSlice(n int) []byte {
	b := make([]byte, n)
	i := r.readFull(b)
	return b[:i]
}

func (r *Reader) ReadBool() bool {
	return r.ReadByte() != 0
}

func (r *Reader) ReadFixString() string {
	n := r.ReadInt16()
	if n < 0 {
		return ""
	}
	return string(r.ReadSlice(int(n)))
}

func (r *Reader) ReadVarString() string {
	n := r.ReadVarInt()
	if n < 0 {
		return ""
	}
	return string(r.ReadSlice(int(n)))
}

func (r *Reader) ReadFixBytes() []byte {
	n := r.ReadInt32()
	if n < 0 {
		return nil
	}
	return r.ReadSlice(int(n))
}

func (r *Reader) ReadVarBytes() []byte {
	n := r.ReadVarInt()
	if n < 0 {
		return nil
	}
	return r.ReadSlice(int(n))
}

func (r *Reader) readFull(b []byte) int {
	if r.err != nil {
		if r.err == io.EOF {
			r.err = io.ErrUnexpectedEOF
		}
		return 0
	}
	if r.n < len(b) {
		r.DiscardAll()
		if r.err == nil {
			r.err = ErrShortRead
		}
		return 0
	}
	n, err := io.ReadAtLeast(r.r, b, len(b))
	r.n -= n
	if err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		r.err = err
	}
	return n
}

func (r *Reader) Read(b []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	if r.n == 0 {
		return 0, io.EOF
	}
	n := len(b)
	if n > r.n {
		n = r.n
	}
	n, err := r.r.Read(b)
	r.n -= n
	r.err = err
	return n, err
}

func (r *Reader) DiscardBytes() {
	r.Discard(int(r.ReadInt32()))
}

func (r *Reader) DiscardString() {
	r.Discard(int(r.ReadInt16()))
}

func (r *Reader) DiscardAll() {
	r.Discard(r.n)
}

func (r *Reader) Discard(n int) int {
	if r.err != nil {
		return 0
	}
	if r.n < n {
		n, r.err = r.n, ErrShortRead
	}
	n, err := discard(r.r, n)
	r.n -= n
	if err != nil {
		r.err = err
	}
	return n
}

func discard(r io.Reader, n int) (d int, err error) {
	if n > 0 {
		switch x := r.(type) {
		case *Reader:
			d = x.Discard(n)
		case *bufio.Reader:
			d, err = x.Discard(n)
		default:
			var c int64
			c, err = io.CopyN(ioutil.Discard, x, int64(n))
			d = int(c)
		}
		switch {
		case d < n && err == nil:
			err = io.ErrUnexpectedEOF
		case d == n && err != nil:
			err = nil
		}
	}
	return
}