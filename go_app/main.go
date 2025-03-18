package main

import (
	"fmt"
)
 
// Add function that adds two integers
func Add(a, b int) int {
	return a + b
}

// Subtract function that subtracts b from a
func Subtract(a, b int) int {
	return a - b
}

// Multiply function that multiplies two integers
func Multiply(a, b int) int {
	return a * b
}

// Divide function that divides a by b and returns the result and error if division by zero
func Divide(a, b int) (float64, error) {
	if b == 0 {
		return 0, fmt.Errorf("cannot divide by zero")
	}
	return float64(a) / float64(b), nil
}

// performCalculations function encapsulates the logic you want to test
func performCalculations() {
	fmt.Println("Addition of 2 and 3:", Add(2, 3))
	fmt.Println("Subtraction of 5 and 3:", Subtract(5, 3))
	fmt.Println("Multiplication of 3 and 4:", Multiply(3, 4))

	result, err := Divide(6, 2)
	if err != nil {
		fmt.Println("Error:", err)
	} else {
		fmt.Println("Division of 6 by 2:", result)
	}
}

// main function is the entry point of the Go application
func main() {
	performCalculations()
}
