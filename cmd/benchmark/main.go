package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"
)

// BenchmarkResult stores the results of a benchmark run
type BenchmarkResult struct {
	URL              string
	TotalRequests    int
	SuccessRequests  int
	FailedRequests   int
	TotalBytes       int64
	AverageTime      time.Duration
	MinTime          time.Duration
	MaxTime          time.Duration
	TotalElapsedTime time.Duration
}

// RequestResult stores the result of a single request
type RequestResult struct {
	URL       string
	Success   bool
	BytesRead int64
	Duration  time.Duration
	Error     error
}

func main() {
	// Parse command-line flags
	proxyURL := flag.String("proxy", "http://localhost:3142", "URL of the apt-cacher-go server")
	concurrency := flag.Int("concurrency", 10, "Number of concurrent requests")
	iterations := flag.Int("iterations", 100, "Number of iterations per URL")
	outputFile := flag.String("output", "", "Write results to file (optional)")

	flag.Parse()

	// Sample package URLs for benchmarking
	urls := []string{
		"/ubuntu/pool/main/l/linux/linux-headers-5.15.0-60-generic_5.15.0-60.66_amd64.deb",
		"/ubuntu/pool/main/p/python3.10/python3.10_3.10.6-1~22.04_amd64.deb",
		"/ubuntu/pool/main/libc/libcurl4-openssl-dev_7.81.0-1ubuntu1.10_amd64.deb",
		"/ubuntu/pool/main/o/openjdk-18/openjdk-18-jre-headless_18.0.2+9-1_amd64.deb",
		"/debian/pool/main/g/gcc-12/gcc-12_12.2.0-14_amd64.deb",
	}

	// Create a client with timeout
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Run benchmark for each URL
	results := make([]BenchmarkResult, len(urls))

	for i, path := range urls {
		fmt.Printf("Benchmarking %s...\n", path)
		results[i] = benchmarkURL(client, *proxyURL, path, *concurrency, *iterations)

		// Print result summary
		fmt.Printf("  Completed: %d/%d requests successful\n",
			results[i].SuccessRequests, results[i].TotalRequests)
		fmt.Printf("  Average time: %v\n", results[i].AverageTime)
		fmt.Printf("  Throughput: %.2f MB/s\n\n",
			float64(results[i].TotalBytes)/1024/1024/results[i].TotalElapsedTime.Seconds())
	}

	// Print overall summary
	fmt.Println("=== BENCHMARK SUMMARY ===")
	var totalBytes int64
	var totalTime time.Duration
	var totalRequests, successRequests int

	for _, result := range results {
		totalBytes += result.TotalBytes
		totalTime += result.TotalElapsedTime
		totalRequests += result.TotalRequests
		successRequests += result.SuccessRequests
	}

	fmt.Printf("Total requests: %d (Success: %d, Failed: %d)\n",
		totalRequests, successRequests, totalRequests-successRequests)
	fmt.Printf("Total data transferred: %.2f MB\n", float64(totalBytes)/1024/1024)
	fmt.Printf("Overall average throughput: %.2f MB/s\n",
		float64(totalBytes)/1024/1024/totalTime.Seconds())

	// Write results to file if requested
	if *outputFile != "" {
		writeResultsToFile(results, *outputFile)
	}
}

func benchmarkURL(client *http.Client, proxyURL, path string, concurrency, iterations int) BenchmarkResult {
	// Calculate the full URL
	fullURL, err := url.JoinPath(proxyURL, path)
	if err != nil {
		fmt.Printf("Error creating URL: %v\n", err)
		return BenchmarkResult{URL: path}
	}

	// Create channel for results
	resultChan := make(chan RequestResult, iterations)

	// Create wait group for goroutines
	var wg sync.WaitGroup

	// Start benchmark
	startTime := time.Now()

	// Create worker pool
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			// Calculate how many iterations each worker should do
			workerIterations := iterations / concurrency
			if i < iterations%concurrency {
				workerIterations++
			}

			for j := 0; j < workerIterations; j++ {
				result := makeRequest(client, fullURL)
				resultChan <- result
			}
		}()
	}

	// Wait for all workers to finish
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Collect results
	var totalBytes int64
	var totalDuration time.Duration
	var minTime = time.Hour
	var maxTime time.Duration
	var successCount, failCount int

	for result := range resultChan {
		if result.Success {
			successCount++
			totalBytes += result.BytesRead
			totalDuration += result.Duration

			if result.Duration < minTime {
				minTime = result.Duration
			}
			if result.Duration > maxTime {
				maxTime = result.Duration
			}
		} else {
			failCount++
			fmt.Printf("  Request failed: %v\n", result.Error)
		}
	}

	totalElapsedTime := time.Since(startTime)

	// Calculate average
	var avgTime time.Duration
	if successCount > 0 {
		avgTime = totalDuration / time.Duration(successCount)
	}

	return BenchmarkResult{
		URL:              path,
		TotalRequests:    successCount + failCount,
		SuccessRequests:  successCount,
		FailedRequests:   failCount,
		TotalBytes:       totalBytes,
		AverageTime:      avgTime,
		MinTime:          minTime,
		MaxTime:          maxTime,
		TotalElapsedTime: totalElapsedTime,
	}
}

func makeRequest(client *http.Client, url string) RequestResult {
	startTime := time.Now()

	// Make request
	resp, err := client.Get(url)
	if err != nil {
		return RequestResult{
			URL:     url,
			Success: false,
			Error:   err,
		}
	}
	defer resp.Body.Close()

	// Check status
	if resp.StatusCode != http.StatusOK {
		return RequestResult{
			URL:     url,
			Success: false,
			Error:   fmt.Errorf("unexpected status code: %d", resp.StatusCode),
		}
	}

	// Read and discard body to measure download speed
	bytesRead, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		return RequestResult{
			URL:     url,
			Success: false,
			Error:   err,
		}
	}

	// Calculate elapsed time
	elapsed := time.Since(startTime)

	return RequestResult{
		URL:       url,
		Success:   true,
		BytesRead: bytesRead,
		Duration:  elapsed,
	}
}

func writeResultsToFile(results []BenchmarkResult, filename string) {
	file, err := os.Create(filename)
	if err != nil {
		fmt.Printf("Error creating output file: %v\n", err)
		return
	}
	defer file.Close()

	// Write CSV header
	fmt.Fprintln(file, "URL,TotalRequests,SuccessRequests,FailedRequests,TotalBytes,AverageTime(ms),MinTime(ms),MaxTime(ms),TotalTime(s),Throughput(MB/s)")

	// Write results
	for _, result := range results {
		throughput := float64(result.TotalBytes) / 1024 / 1024 / result.TotalElapsedTime.Seconds()

		fmt.Fprintf(file, "%s,%d,%d,%d,%d,%.2f,%.2f,%.2f,%.2f,%.2f\n",
			result.URL,
			result.TotalRequests,
			result.SuccessRequests,
			result.FailedRequests,
			result.TotalBytes,
			float64(result.AverageTime.Milliseconds()),
			float64(result.MinTime.Milliseconds()),
			float64(result.MaxTime.Milliseconds()),
			result.TotalElapsedTime.Seconds(),
			throughput,
		)
	}

	fmt.Printf("Results written to %s\n", filename)
}
