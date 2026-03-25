//go:build !darwin && !linux

package netmon

// Watch is a no-op on unsupported platforms. It blocks until done is closed.
func Watch(done <-chan struct{}) error {
	<-done
	return nil
}
