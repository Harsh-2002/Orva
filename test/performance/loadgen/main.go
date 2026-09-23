// Orva's scratch-instance load generator. It does not create or delete server
// resources; use only URLs for functions on an instance you own for testing.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type urlsFlag []string

func (u *urlsFlag) String() string { return strings.Join(*u, ",") }
func (u *urlsFlag) Set(value string) error {
	if value == "" {
		return errors.New("URL must not be empty")
	}
	*u = append(*u, value)
	return nil
}

type config struct {
	URLs        []string
	Requests    int
	Concurrency int
	Rate        float64 // scheduled requests/second; zero selects closed-loop
	Timeout     time.Duration
}

type sample struct {
	target  string
	status  int
	latency time.Duration
	err     string
	errCode string
}

type latencyStats struct {
	Count int     `json:"count"`
	P50MS float64 `json:"p50_ms"`
	P95MS float64 `json:"p95_ms"`
	P99MS float64 `json:"p99_ms"`
	MaxMS float64 `json:"max_ms"`
}

type report struct {
	URLs               []string             `json:"urls"`
	Mode               string               `json:"mode"`
	StartedAt          string               `json:"started_at"`
	Concurrency        int                  `json:"concurrency"`
	TargetRate         float64              `json:"target_rate"`
	TimeoutMS          int64                `json:"timeout_ms"`
	Scheduled          int                  `json:"scheduled"`
	Attempted          int                  `json:"attempted"`
	Unsent             int                  `json:"unsent"`
	ElapsedSeconds     float64              `json:"elapsed_seconds"`
	AttemptsPerSecond  float64              `json:"attempts_per_second"`
	SuccessesPerSecond float64              `json:"successes_per_second"`
	Status             map[int]int          `json:"status"`
	ErrorCodes         map[string]int       `json:"error_codes"`
	TransportErrors    map[string]int       `json:"transport_errors"`
	LatencyByStatus    map[int]latencyStats `json:"latency_by_status"`
	TransportLatency   latencyStats         `json:"transport_latency"`
	ByURL              map[string]urlReport `json:"by_url"`
}

type urlReport struct {
	Attempted       int                  `json:"attempted"`
	Status          map[int]int          `json:"status"`
	ErrorCodes      map[string]int       `json:"error_codes"`
	TransportErrors map[string]int       `json:"transport_errors"`
	LatencyByStatus map[int]latencyStats `json:"latency_by_status"`
}

type urlSamples struct {
	attempted int
	status    map[int]int
	codes     map[string]int
	errors    map[string]int
	latencies map[int][]time.Duration
}

func validate(c config) error {
	if len(c.URLs) == 0 || c.Requests <= 0 || c.Concurrency <= 0 ||
		c.Rate < 0 || math.IsNaN(c.Rate) || math.IsInf(c.Rate, 0) || c.Timeout <= 0 {
		return errors.New("require at least one URL, positive requests/concurrency/timeout, and nonnegative rate")
	}
	for _, target := range c.URLs {
		parsed, err := url.ParseRequestURI(target)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return fmt.Errorf("URL must use http or https: %q", target)
		}
	}
	return nil
}

func run(ctx context.Context, c config) (report, error) {
	if err := validate(c); err != nil {
		return report{}, err
	}
	transport := &http.Transport{
		MaxIdleConns:        c.Concurrency,
		MaxIdleConnsPerHost: c.Concurrency,
		MaxConnsPerHost:     c.Concurrency,
		IdleConnTimeout:     30 * time.Second,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: c.Timeout}
	results := make(chan sample, c.Concurrency*2)
	var wg sync.WaitGroup
	started := time.Now()
	latencies := make(map[int][]time.Duration)
	var errorLatencies []time.Duration
	status := make(map[int]int)
	errorCodes := make(map[string]int)
	errorsByKind := make(map[string]int)
	byURL := make(map[string]*urlSamples, len(c.URLs))
	for _, target := range c.URLs {
		byURL[target] = &urlSamples{
			status: make(map[int]int), codes: make(map[string]int),
			errors:    make(map[string]int),
			latencies: make(map[int][]time.Duration),
		}
	}
	attempted, successes := 0, 0
	collected := make(chan struct{})
	go func() {
		defer close(collected)
		for got := range results {
			attempted++
			bucket := byURL[got.target]
			bucket.attempted++
			if got.err != "" {
				errorsByKind[got.err]++
				bucket.errors[got.err]++
				errorLatencies = append(errorLatencies, got.latency)
				continue
			}
			status[got.status]++
			bucket.status[got.status]++
			if got.errCode != "" {
				errorCodes[got.errCode]++
				bucket.codes[got.errCode]++
			}
			latencies[got.status] = append(latencies[got.status], got.latency)
			bucket.latencies[got.status] = append(bucket.latencies[got.status], got.latency)
			if got.status >= 200 && got.status < 300 {
				successes++
			}
		}
	}()
	issue := func(i int) {
		target := c.URLs[i%len(c.URLs)]
		began := time.Now()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			results <- sample{target: target, latency: time.Since(began), err: "request_invalid"}
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			kind := "network"
			if errors.Is(err, context.DeadlineExceeded) || os.IsTimeout(err) {
				kind = "timeout"
			} else if errors.Is(err, context.Canceled) {
				kind = "canceled"
			}
			results <- sample{target: target, latency: time.Since(began), err: kind}
			return
		}
		var errCode string
		if resp.StatusCode >= 400 {
			prefix, prefixErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
			if prefixErr != nil {
				_ = resp.Body.Close()
				results <- sample{target: target, latency: time.Since(began), err: "body_read"}
				return
			}
			var envelope struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if json.Unmarshal(prefix, &envelope) == nil && len(envelope.Error.Code) <= 64 {
				errCode = envelope.Error.Code
			}
		}
		_, readErr := io.Copy(io.Discard, resp.Body)
		closeErr := resp.Body.Close()
		if readErr != nil || closeErr != nil {
			results <- sample{target: target, latency: time.Since(began), err: "body_read"}
			return
		}
		results <- sample{target: target, status: resp.StatusCode, latency: time.Since(began), errCode: errCode}
	}

	unsent := 0
	if c.Rate == 0 {
		// Closed-loop: each client sends its next request only after the last
		// response. A slow server therefore lowers the offered arrival rate.
		for worker := range c.Concurrency {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := worker; i < c.Requests; i += c.Concurrency {
					issue(i)
				}
			}()
		}
	} else {
		// Open-loop: schedule at the requested rate without creating an
		// unbounded waiter backlog. Unsent arrivals are reported separately
		// from HTTP 429 and network errors.
		jobs := make(chan int, c.Concurrency)
		for range c.Concurrency {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range jobs {
					issue(i)
				}
			}()
		}
		interval := time.Duration(float64(time.Second) / c.Rate)
		if interval < time.Nanosecond {
			interval = time.Nanosecond
		}
		for i := range c.Requests {
			due := started.Add(time.Duration(i) * interval)
			if sleep := time.Until(due); sleep > 0 {
				select {
				case <-time.After(sleep):
				case <-ctx.Done():
					close(jobs)
					wg.Wait()
					close(results)
					<-collected
					return report{}, ctx.Err()
				}
			}
			select {
			case jobs <- i:
			default:
				unsent++
			}
		}
		close(jobs)
	}
	go func() { wg.Wait(); close(results) }()
	<-collected
	elapsed := time.Since(started).Seconds()
	out := report{
		URLs: c.URLs, Mode: "closed", StartedAt: started.UTC().Format(time.RFC3339Nano),
		Concurrency: c.Concurrency, TargetRate: c.Rate,
		TimeoutMS: c.Timeout.Milliseconds(), Scheduled: c.Requests,
		Attempted: attempted, Unsent: unsent, ElapsedSeconds: elapsed,
		AttemptsPerSecond:  float64(attempted) / elapsed,
		SuccessesPerSecond: float64(successes) / elapsed,
		Status:             status, ErrorCodes: errorCodes,
		TransportErrors:  errorsByKind,
		LatencyByStatus:  make(map[int]latencyStats),
		TransportLatency: summarize(errorLatencies),
		ByURL:            make(map[string]urlReport, len(byURL)),
	}
	if c.Rate > 0 {
		out.Mode = "open"
	}
	for code, values := range latencies {
		out.LatencyByStatus[code] = summarize(values)
	}
	for target, bucket := range byURL {
		one := urlReport{
			Attempted: bucket.attempted, Status: bucket.status,
			ErrorCodes:      bucket.codes,
			TransportErrors: bucket.errors,
			LatencyByStatus: make(map[int]latencyStats),
		}
		for code, values := range bucket.latencies {
			one.LatencyByStatus[code] = summarize(values)
		}
		out.ByURL[target] = one
	}
	return out, nil
}

func summarize(values []time.Duration) latencyStats {
	if len(values) == 0 {
		return latencyStats{}
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	ms := func(n int) float64 { return float64(values[n]) / float64(time.Millisecond) }
	return latencyStats{
		Count: len(values), P50MS: ms((len(values) - 1) / 2),
		P95MS: ms((len(values) - 1) * 95 / 100),
		P99MS: ms((len(values) - 1) * 99 / 100), MaxMS: ms(len(values) - 1),
	}
}

func main() {
	var targets urlsFlag
	flag.Var(&targets, "url", "function URL; repeat for mixed-function load")
	requests := flag.Int("requests", 5000, "number of scheduled requests")
	concurrency := flag.Int("concurrency", 100, "maximum concurrent clients")
	rate := flag.Float64("rate", 0, "scheduled requests/second; 0 selects closed-loop")
	timeout := flag.Duration("timeout", 15*time.Second, "per-request timeout")
	flag.Parse()
	out, err := run(context.Background(), config{
		URLs: targets, Requests: *requests, Concurrency: *concurrency,
		Rate: *rate, Timeout: *timeout,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if len(out.TransportErrors) > 0 || out.Unsent > 0 {
		os.Exit(1)
	}
}
