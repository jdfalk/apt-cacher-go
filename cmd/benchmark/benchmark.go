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

// Command line flags
var (
	targetURL       string
	concurrency     int
	duration        int
	outputFile      string
	requestPatterns []string
)

// Results of the benchmark
type BenchmarkResults struct {
	TotalRequests      int
	SuccessfulRequests int
	FailedRequests     int
	TotalBytes         int64
	TotalDuration      time.Duration
	MinResponseTime    time.Duration
	MaxResponseTime    time.Duration
	AvgResponseTime    time.Duration
}

// NewCommand creates the benchmark command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "benchmark",
		Short: "Run performance benchmark",
		Long:  `Run performance benchmark against a running apt-cacher-go server or other compatible service.`,
		Run: func(cmd *cobra.Command, args []string) {
			runBenchmark()
		},
	}

	// Define flags
	cmd.Flags().StringVarP(&targetURL, "target", "t", "http://localhost:3142", "Target server URL")
	cmd.Flags().IntVarP(&concurrency, "concurrency", "c", 10, "Number of concurrent clients")
	cmd.Flags().IntVarP(&duration, "duration", "d", 30, "Duration of benchmark in seconds")
	cmd.Flags().StringVarP(&outputFile, "output", "o", "", "Output file for benchmark results (default: stdout)")
	cmd.Flags().StringSliceVarP(&requestPatterns, "patterns", "p", []string{
		"/ubuntu/dists/jammy/main/binary-amd64/Packages.gz",
		"/ubuntu/pool/main/h/hello/hello_2.10-2ubuntu2_amd64.deb",
	}, "Request patterns to benchmark")

	// Bind flags to viper
	viper.BindPFlag("benchmark.target", cmd.Flags().Lookup("target"))
	viper.BindPFlag("benchmark.concurrency", cmd.Flags().Lookup("concurrency"))
	viper.BindPFlag("benchmark.duration", cmd.Flags().Lookup("duration"))
	viper.BindPFlag("benchmark.output", cmd.Flags().Lookup("output"))
	viper.BindPFlag("benchmark.patterns", cmd.Flags().Lookup("patterns"))

	return cmd
}

func runBenchmark() {
	// Get values from viper with fallback to command line flags
	target := viper.GetString("benchmark.target")
	if target == "" {
		target = targetURL
	}

	conc := viper.GetInt("benchmark.concurrency")
	if conc == 0 {
		conc = concurrency
	}

	dur := viper.GetInt("benchmark.duration")
	if dur == 0 {
		dur = duration
	}

	output := viper.GetString("benchmark.output")
	if output == "" {
		output = outputFile
	}

	patterns := viper.GetStringSlice("benchmark.patterns")
	if len(patterns) == 0 {
		patterns = requestPatterns
	}

	fmt.Printf("Starting benchmark against %s\n", target)
	fmt.Printf("Concurrency: %d, Duration: %d seconds\n", conc, dur)
	fmt.Printf("Request patterns: %v\n", patterns)

	// Prepare benchmark
	results := &BenchmarkResults{
		MinResponseTime: time.Hour, // Start with a large value to be reduced
	}
	var wg sync.WaitGroup
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Create channels for communication
	stopCh := make(chan struct{})
	resultsCh := make(chan struct {
		success      bool
		bytes        int64
		responseTime time.Duration
	}, conc*100)

	// Start workers
	for i := 0; i < conc; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			benchmarkWorker(client, target, patterns, stopCh, resultsCh, workerID)
		}(i)
	}

	// Start results collector
	var resultsWg sync.WaitGroup
	resultsWg.Add(1)
	go func() {
		defer resultsWg.Done()
		for result := range resultsCh {
			results.TotalRequests++
			if result.success {
				results.SuccessfulRequests++
				results.TotalBytes += result.bytes
			} else {
				results.FailedRequests++
			}

			if result.responseTime < results.MinResponseTime {
				results.MinResponseTime = result.responseTime
			}
			if result.responseTime > results.MaxResponseTime {
				results.MaxResponseTime = result.responseTime
			}
			results.TotalDuration += result.responseTime
		}
	}()

	// Run benchmark for the specified duration
	time.Sleep(time.Duration(dur) * time.Second)
	close(stopCh)

	// Wait for workers to finish
	wg.Wait()
	close(resultsCh)
	resultsWg.Wait()

	// Calculate average response time
	if results.SuccessfulRequests > 0 {
		results.AvgResponseTime = results.TotalDuration / time.Duration(results.SuccessfulRequests)
	}

	// Print results
	reportResults(results, output)
}

func benchmarkWorker(client *http.Client, baseURL string, patterns []string, stop <-chan struct{}, results chan<- struct {
	success      bool
	bytes        int64
	responseTime time.Duration
}, workerID int) {
	patternCount := len(patterns)
	counter := 0

	for {
		select {
		case <-stop:
			return
		default:
			// Select a pattern using a simple round-robin approach
			pattern := patterns[counter%patternCount]
			counter++

			start := time.Now()
			success, bytes := makeRequest(client, baseURL+pattern)
			responseTime := time.Since(start)

			results <- struct {
				success      bool
				bytes        int64
				responseTime time.Duration
			}{
				success:      success,
				bytes:        bytes,
				responseTime: responseTime,
			}

			// Small sleep to prevent overwhelming the system
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func makeRequest(client *http.Client, url string) (bool, int64) {
	resp, err := client.Get(url)
	if err != nil {
		return false, 0
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, 0
	}

	// Read and discard the body to measure bytes
	bytes, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		return false, 0
	}

	return true, bytes
}

func reportResults(results *BenchmarkResults, outputFile string) {
	report := fmt.Sprintf(`
Benchmark Results:
-----------------
Total Requests:        %d
Successful Requests:   %d (%.2f%%)
Failed Requests:       %d
Total Data Transferred: %.2f MB
Requests Per Second:   %.2f
Average Response Time: %s
Min Response Time:     %s
Max Response Time:     %s
`,
		results.TotalRequests,
		results.SuccessfulRequests,
		float64(results.SuccessfulRequests)/float64(results.TotalRequests)*100.0,
		results.FailedRequests,
		float64(results.TotalBytes)/(1024*1024),
		float64(results.TotalRequests)/float64(duration),
		results.AvgResponseTime,
		results.MinResponseTime,
		results.MaxResponseTime,
	)

	if outputFile == "" {
		// Print to stdout
		fmt.Println(report)
	} else {
		// Write to file
		err := os.WriteFile(outputFile, []byte(report), 0644)
		if err != nil {
			log.Fatalf("Failed to write results to file: %v", err)
		}
		fmt.Printf("Results written to %s\n", outputFile)
	}
}
