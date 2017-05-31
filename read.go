package kafka

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"reflect"
)

type readBytesFunc func(r *bufio.Reader, sz int, n int) (remain int, err error)

type readArrayFunc func(r *bufio.Reader, sz int) (remain int, err error)

var errShortRead = errors.New("not enough bytes available to load the response")

func peekRead(r *bufio.Reader, sz int, n int, f func([]byte)) (int, error) {
	if n > sz {
		return sz, errShortRead
	}
	b, err := r.Peek(n)
	if err != nil {
		return sz, err
	}
	f(b)
	return discardN(r, sz, n)
}

func readInt8(r *bufio.Reader, sz int, v *int8) (int, error) {
	return peekRead(r, sz, 1, func(b []byte) { *v = makeInt8(b) })
}

func readInt16(r *bufio.Reader, sz int, v *int16) (int, error) {
	return peekRead(r, sz, 2, func(b []byte) { *v = makeInt16(b) })
}

func readInt32(r *bufio.Reader, sz int, v *int32) (int, error) {
	return peekRead(r, sz, 4, func(b []byte) { *v = makeInt32(b) })
}

func readInt64(r *bufio.Reader, sz int, v *int64) (int, error) {
	return peekRead(r, sz, 8, func(b []byte) { *v = makeInt64(b) })
}

func readString(r *bufio.Reader, sz int, v *string) (int, error) {
	return readStringWith(r, sz, func(r *bufio.Reader, sz int, n int) (remain int, err error) {
		*v, remain, err = readNewString(r, sz, n)
		return
	})
}

func readStringWith(r *bufio.Reader, sz int, cb readBytesFunc) (int, error) {
	var err error
	var len int16

	if sz, err = readInt16(r, sz, &len); err != nil {
		return sz, err
	}

	n := int(len)
	if n > sz {
		return sz, errShortRead
	}

	return cb(r, sz, n)
}

func readNewString(r *bufio.Reader, sz int, n int) (string, int, error) {
	b, sz, err := readNewBytes(r, sz, n)
	return string(b), sz, err
}

func readBytes(r *bufio.Reader, sz int, v *[]byte) (int, error) {
	return readBytesWith(r, sz, func(r *bufio.Reader, sz int, n int) (remain int, err error) {
		*v, remain, err = readNewBytes(r, sz, n)
		return
	})
}

func readBytesWith(r *bufio.Reader, sz int, cb readBytesFunc) (int, error) {
	var err error
	var len int32

	if sz, err = readInt32(r, sz, &len); err != nil {
		return sz, err
	}

	n := int(len)
	if n > sz {
		return sz, errShortRead
	}

	return cb(r, sz, n)
}

func readNewBytes(r *bufio.Reader, sz int, n int) ([]byte, int, error) {
	var err error
	var b []byte

	if n > 0 {
		b = make([]byte, n)
		n, err = io.ReadFull(r, b)
		b = b[:n]
		sz -= n
	}

	return b, sz, err
}

func readArrayWith(r *bufio.Reader, sz int, cb readArrayFunc) (int, error) {
	var err error
	var len int32

	if sz, err = readInt32(r, sz, &len); err != nil {
		return sz, err
	}

	for n := int(len); n > 0; n-- {
		if sz, err = cb(r, sz); err != nil {
			break
		}
	}

	return sz, err
}

func read(r *bufio.Reader, sz int, a interface{}) (int, error) {
	switch v := a.(type) {
	case *int8:
		return readInt8(r, sz, v)
	case *int16:
		return readInt16(r, sz, v)
	case *int32:
		return readInt32(r, sz, v)
	case *int64:
		return readInt64(r, sz, v)
	case *string:
		return readString(r, sz, v)
	case *[]byte:
		return readBytes(r, sz, v)
	}
	switch v := reflect.ValueOf(a).Elem(); v.Kind() {
	case reflect.Struct:
		return readStruct(r, sz, v)
	case reflect.Slice:
		return readSlice(r, sz, v)
	default:
		panic(fmt.Sprintf("unsupported type: %T", a))
	}
}

func readStruct(r *bufio.Reader, sz int, v reflect.Value) (int, error) {
	var err error
	for i, n := 0, v.NumField(); i != n; i++ {
		if sz, err = read(r, sz, v.Field(i).Addr().Interface()); err != nil {
			return sz, err
		}
	}
	return sz, nil
}

func readSlice(r *bufio.Reader, sz int, v reflect.Value) (int, error) {
	var err error
	var len int32

	if sz, err = readInt32(r, sz, &len); err != nil {
		return sz, err
	}

	if n := int(len); n < 0 {
		v.Set(reflect.Zero(v.Type()))
	} else {
		v.Set(reflect.MakeSlice(v.Type(), n, n))

		for i := 0; i != n; i++ {
			if sz, err = read(r, sz, v.Index(i).Addr().Interface()); err != nil {
				return sz, err
			}
		}
	}

	return sz, nil
}

func readFetchResponseHeader(r *bufio.Reader, size int) (throttle int32, remain int, err error) {
	var n int32
	var p struct {
		Partition           int32
		ErrorCode           int16
		HighwaterMarkOffset int64
		MessageSetSize      int32
	}

	if remain, err = readInt32(r, size, &throttle); err != nil {
		return
	}

	if remain, err = readInt32(r, remain, &n); err != nil {
		return
	}

	// This error should never trigger, unless there's a bug in the kafka client
	// or server.
	if n != 1 {
		err = fmt.Errorf("1 kafka topic was expected in the fetch response but the client received %d", n)
		return
	}

	// We ignore the topic name because we've requests messages for a single
	// topic, unless there's a bug in the kafka server we will have received
	// the name of the topic that we requested.
	if remain, err = discardString(r, remain); err != nil {
		return
	}

	if remain, err = readInt32(r, remain, &n); err != nil {
		return
	}

	// This error should never trigger, unless there's a bug in the kafka client
	// or server.
	if n != 1 {
		err = fmt.Errorf("1 kafka partition was expected in the fetch response but the client received %d", n)
		return
	}

	if remain, err = read(r, remain, &p); err != nil {
		return
	}

	if p.ErrorCode != 0 {
		err = Error(p.ErrorCode)
		return
	}

	// This error should never trigger, unless there's a bug in the kafka client
	// or server.
	if remain != int(p.MessageSetSize) {
		err = fmt.Errorf("the size of the message set in a fetch response doesn't match the number of remaining bytes (message set size = %d, remaining bytes = %d)", p.MessageSetSize, remain)
		return
	}

	return
}

func readMessageHeader(r *bufio.Reader, sz int) (offset int64, attributes int8, timestamp int64, remain int, err error) {
	var version int8

	if remain, err = readInt64(r, sz, &offset); err != nil {
		return
	}

	// On discarding the message size and CRC:
	// ---------------------------------------
	//
	// - Not sure why kafka gives the message size here, we already have the
	// number of remaining bytes in the response and kafka should only truncate
	// the trailing message.
	//
	// - TCP is already taking care of ensuring data integrirty, no need to
	// waste resources doing it a second time so we just skip the message CRC.
	//
	if remain, err = discardN(r, remain, 8); err != nil {
		return
	}

	if remain, err = readInt8(r, remain, &version); err != nil {
		return
	}

	if remain, err = readInt8(r, remain, &attributes); err != nil {
		return
	}

	switch version {
	case 0:
	case 1:
		remain, err = readInt64(r, remain, &timestamp)
	default:
		err = fmt.Errorf("unsupported message version %d found in fetch response", version)
	}

	return
}

func readMessage(r *bufio.Reader, sz int, min int64, key readBytesFunc, val readBytesFunc) (offset int64, timestamp int64, remain int, err error) {
	for {
		// TODO: read attributes and decompress the message
		if offset, _, timestamp, remain, err = readMessageHeader(r, sz); err != nil {
			return
		}

		// When the messages are compressed kafka may return messages at an
		// earlier offset than the one that was requested, apparently it's the
		// client's responsibility to ignore those.
		if offset >= min {
			if remain, err = readBytesWith(r, remain, key); err != nil {
				return
			}
			remain, err = readBytesWith(r, remain, val)
			return
		}

		if remain, err = discardBytes(r, remain); err != nil {
			return
		}
		if remain, err = discardBytes(r, remain); err != nil {
			return
		}
	}
}
