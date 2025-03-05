package main

import (
	"testing"
)

// TestAdd checks if the Add function works correctly
func TestAdd(t *testing.T) {
	result := Add(2, 3)
	expected := 5
	if result != expected {
		t.Errorf("Add(2, 3) = %d; want %d", result, expected)
	}
}

// TestSubtract checks if the Subtract function works correctly
func TestSubtract(t *testing.T) {
	result := Subtract(5, 3)
	expected := 2
	if result != expected {
		t.Errorf("Subtract(5, 3) = %d; want %d", result, expected)
	}
}

// TestMultiply checks if the Multiply function works correctly
func TestMultiply(t *testing.T) {
	result := Multiply(3, 4)
	expected := 12
	if result != expected {
		t.Errorf("Multiply(3, 4) = %d; want %d", result, expected)
	}
}

// TestDivide checks if the Divide function works correctly
func TestDivide(t *testing.T) {
	// Test valid division
	result, err := Divide(6, 2)
	expected := 3.0
	if err != nil || result != expected {
		t.Errorf("Divide(6, 2) = %f, %v; want %f", result, err, expected)
	}

	// Test division by zero
	_, err = Divide(6, 0)
	if err == nil {
		t.Errorf("Divide(6, 0) should return an error")
	}
}

// TestPerformCalculations checks the logic inside performCalculations
func TestPerformCalculations(t *testing.T) {
	// This function call will execute the logic inside performCalculations
	performCalculations()
}
