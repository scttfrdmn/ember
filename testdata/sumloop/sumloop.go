package sumloop

// SumN returns the sum 0 + 1 + ... + (n-1).
func SumN(n int64) int64 {
	var s int64
	for i := int64(0); i < n; i++ {
		s += i
	}
	return s
}
