// Package add is a testdata fixture used to verify the Phase 0
// ember pipeline end-to-end. It intentionally contains only a single
// pure arithmetic function with no goroutines, no GC, no channels.
package add

// Add returns the sum of a and b.
func Add(a, b int) int {
	return a + b
}
