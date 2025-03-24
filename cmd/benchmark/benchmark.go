package benchmark

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Flags
var (
	target     string
	conc       int
	duration   string
	outputFile string
	patterns   []string
)

// NewCommand creates the benchmark command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "benchmark",
		Short: "Run benchmarks against the server",
		Long:  `Run performance benchmarks against a running apt-cacher-go server`,
		Run: func(cmd *cobra.Command, args []string) {
			runBenchmark(target, conc, duration, patterns, outputFile)
		},
	}

	// Add flags
	cmd.Flags().StringVarP(&target, "target", "t", "http://localhost:3142", "Target server URL")
	cmd.Flags().IntVarP(&conc, "concurrency", "c", 10, "Number of concurrent requests")
	cmd.Flags().StringVarP(&duration, "duration", "d", "30s", "Test duration")
	cmd.Flags().StringVarP(&outputFile, "output", "o", "", "Output file for results")
	cmd.Flags().StringSliceVarP(&patterns, "patterns", "p", []string{
		"/debian/dists/stable/Release",
		"/ubuntu/dists/focal/main/binary-amd64/Packages.gz",
		"/debian/pool/main/h/hello/hello_2.10-2_amd64.deb",
	}, "Request patterns")

	// Bind flags to viper for config file support
	if err := viper.BindPFlag("benchmark.target", cmd.Flags().Lookup("target")); err != nil {
		log.Printf("Warning: Failed to bind flag 'target': %v", err)
	}
	if err := viper.BindPFlag("benchmark.concurrency", cmd.Flags().Lookup("concurrency")); err != nil {
		log.Printf("Warning: Failed to bind flag 'concurrency': %v", err)
	}
	if err := viper.BindPFlag("benchmark.duration", cmd.Flags().Lookup("duration")); err != nil {
		log.Printf("Warning: Failed to bind flag 'duration': %v", err)
	}

	return cmd
}

// runBenchmark runs the benchmark test
func runBenchmark(target string, conc int, durationStr string, patterns []string, outputFile string) {
	// Parse duration
	testDuration, err := time.ParseDuration(durationStr)
	if err != nil {
		log.Fatalf("Invalid duration: %v", err)
	}

	log.Printf("Starting benchmark against %s with %d concurrent clients for %s", target, conc, testDuration)

	// Create HTTP client with reasonable timeouts
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        conc,
			MaxIdleConnsPerHost: conc,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	// Channel to signal workers to stop
	stopCh := make(chan struct{})

	// Channel for benchmark results
	resultsCh := make(chan struct {
		success      bool
		bytes        int64
		responseTime time.Duration
	}, 1000)

	// Start workers
	var wg sync.WaitGroup
	for i := range make([]struct{}, conc) {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			benchmarkWorker(client, target, patterns, stopCh, resultsCh, workerID)
		}(i)
	}

	// Collect results in a separate goroutine
	var totalRequests, successRequests, failedRequests int64
	var totalBytes, totalTime int64
	var minTime, maxTime int64 = 1<<63 - 1, 0

	resultsDone := make(chan struct{})
	go func() {
		for result := range resultsCh {
			totalRequests++
			if result.success {
				successRequests++
				totalBytes += result.bytes
				responseNs := result.responseTime.Nanoseconds()
				totalTime += responseNs
				if responseNs < minTime {
					minTime = responseNs
				}
				if responseNs > maxTime {
					maxTime = responseNs
				}
			} else {
				failedRequests++
			}
		}
		close(resultsDone)
	}()

	// Run for the specified duration
	time.Sleep(testDuration)

	// Signal workers to stop
	close(stopCh)

	// Wait for all workers to finish
	wg.Wait()

	// Close the results channel and wait for the collector to finish
	close(resultsCh)
	<-resultsDone

	// Calculate final results
	elapsedSeconds := testDuration.Seconds()
	reqPerSec := float64(totalRequests) / elapsedSeconds
	bytesPerSec := float64(totalBytes) / elapsedSeconds
	avgTime := "N/A"
	if successRequests > 0 {
		avgTime = time.Duration(totalTime / successRequests).String()
	}

	// Prepare results
	results := fmt.Sprintf(`
Benchmark Results:
-----------------
Duration:           %s
Concurrency:        %d
Total Requests:     %d
Successful:         %d
Failed:             %d
Requests/sec:       %.2f
Transfer:           %.2f MB
Bandwidth:          %.2f MB/s
Min Response Time:  %s
Max Response Time:  %s
Avg Response Time:  %s
`,
		testDuration,
		conc,
		totalRequests,
		successRequests,
		failedRequests,
		reqPerSec,
		float64(totalBytes)/(1024*1024),
		bytesPerSec/(1024*1024),
		time.Duration(minTime).String(),
		time.Duration(maxTime).String(),
		avgTime,
	)

	// Output results
	if outputFile == "" {
		fmt.Println(results)
	} else {
		err := os.WriteFile(outputFile, []byte(results), 0644)
		if err != nil {
			log.Printf("Error writing results: %v", err)
			fmt.Println(results)
		} else {
			fmt.Printf("Results written to %s\n", outputFile)
		}
	}
}

// benchmarkWorker is a worker that sends requests in a loop until stopped
func benchmarkWorker(client *http.Client, baseURL string, patterns []string, stop <-chan struct{}, results chan<- struct {
	success      bool
	bytes        int64
	responseTime time.Duration
}, workerID int) {
	patternCount := len(patterns)
	counter := 0

	log.Printf("Worker %d started", workerID) // Using the workerID parameter for logging

	for {
		select {
		case <-stop:
			return
		default:
			// Select a pattern using round-robin
			pattern := patterns[counter%patternCount]
			counter++

			// Make the request
			start := time.Now()
			success := true
			bytesRead := int64(0)

			resp, err := client.Get(baseURL + pattern)
			if err != nil {
				success = false
			} else {
				// Read and discard the body
				bytesRead, err = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()

				if err != nil {
					success = false
				}

				// Consider non-2xx responses as failures
				if resp.StatusCode < 200 || resp.StatusCode >= 300 {
					success = false
				}
			}

			// Send result
			results <- struct {
				success      bool
				bytes        int64
				responseTime time.Duration
			}{
				success:      success,
				bytes:        bytesRead,
				responseTime: time.Since(start),
			}

			// Small delay to prevent overwhelming the system
			time.Sleep(10 * time.Millisecond)
		}
	}
}
