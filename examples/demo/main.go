package main

import (
	"fmt"
	"time"
)

func main() {
	total := 0
	for attempt := 1; attempt <= 5; attempt++ {
		total = process(attempt, total)
	}
	fmt.Println("finished", total)
}

func process(attempt, total int) int {
	delta := attempt * 7
	total += delta // HANDOVER: attempt == 3
	fmt.Printf("attempt=%d total=%d\n", attempt, total)
	time.Sleep(150 * time.Millisecond)
	return total
}
