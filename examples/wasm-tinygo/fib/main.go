package main

//export fib
func fib(n uint64) uint64 {
	if n < 2 {
		return n
	}
	var a, b uint64 = 0, 1
	for i := uint64(2); i <= n; i++ {
		a, b = b, a+b
	}
	return b
}

func main() {}
