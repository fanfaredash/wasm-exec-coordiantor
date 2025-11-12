package main

//export affine
func affine(x, scale, offset uint64) uint64 {
	return x*scale + offset
}

func main() {}
