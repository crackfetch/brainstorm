package workflow

import (
	"sync"
	"testing"
)

// TestConcurrentFieldAccess verifies that concurrent reads and writes to
// Executor fields (UserAgent, Page, SetEnv, IsHeaded) do not race.
// Run with: go test -count=1 -race ./workflow/ -run TestConcurrentFieldAccess
func TestConcurrentFieldAccess(t *testing.T) {
	w := &Workflow{
		Name: "mutex-test",
		Env:  map[string]string{"A": "1"},
	}
	e := NewExecutor(w)

	var wg sync.WaitGroup
	const goroutines = 10
	const iterations = 100

	// Concurrent readers and writers on exported fields.
	wg.Add(goroutines * 4)

	// Writer: SetEnv
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				e.SetEnv("key", "value")
			}
		}(i)
	}

	// Reader: UserAgent
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = e.UserAgent()
			}
		}()
	}

	// Reader: Page
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = e.Page()
			}
		}()
	}

	// Reader: IsHeaded
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = e.IsHeaded()
			}
		}()
	}

	wg.Wait()
}
