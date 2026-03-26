package ringbuf

import "fmt"

var ErrBufferEmpty = fmt.Errorf("ringbuf: buffer empty")

type errInvalidSliceRange [2]int

func (e errInvalidSliceRange) Error() string {
	return fmt.Sprintf("ringbuf: invalid slice range: (%d, %d)", e[0], e[1])
}
