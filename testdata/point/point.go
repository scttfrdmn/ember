package point

// Point is a 2D integer point.
type Point struct{ X, Y int64 }

// SumFields allocates a Point, sets its fields, and returns X+Y.
func SumFields() int64 {
	var p Point
	p.X = 3
	p.Y = 4
	return p.X + p.Y
}
