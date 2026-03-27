package sum

func add(a, b int) int { return a + b }

func Sum(a, b, c int) int { return add(add(a, b), c) }
