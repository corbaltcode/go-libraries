package athenalib

import "io"

// discardWithClose uses io.Discard to discard writes, but also has a no-op Close method
type discardWithClose struct{}

func (d discardWithClose) Write(p []byte) (int, error) {
	return io.Discard.Write(p)
}
func (d discardWithClose) Close() error {
	return nil
}
