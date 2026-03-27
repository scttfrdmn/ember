package divmod

// DivMod returns the quotient and remainder of a divided by b.
func DivMod(a, b int64) (int64, int64) {
	return a / b, a % b
}
