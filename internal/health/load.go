package health

import (
	"fmt"
	"io"
	"math"
	"net/url"
	"sync"
	"time"
)

// loadResult is the outcome of the sustained-load check.
type loadResult struct {
	AggregateBPS float64 // total bytes/sec across all connections
	StdDevBPS    float64 // stddev of per-connection throughput
	Conns        int     // connections attempted
	Dropped      int     // connections that errored mid-stream
}

// testLoad opens cfg.LoadConns concurrent download streams through the proxy
// for cfg.LoadDuration and measures aggregate throughput, per-connection
// stability (stddev) and dropped connections. Unlike the single-shot
// download check this surfaces behaviour under real concurrent load.
func testLoad(d dialer, cfg Config) (loadResult, error) {
	u, err := url.Parse(cfg.DownloadURL)
	if err != nil {
		return loadResult{}, err
	}

	conns := cfg.LoadConns
	if conns < 1 {
		conns = 1
	}
	deadline := time.Now().Add(cfg.LoadDuration)

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		perConn  = make([]float64, 0, conns)
		dropped  int
		totalDur = cfg.LoadDuration.Seconds()
	)

	for i := 0; i < conns; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var bytesRead int64
			// Keep pulling the download URL until the shared deadline.
			for time.Now().Before(deadline) {
				client := httpClientFor(d, u, cfg.LoadDuration)
				resp, err := client.Get(cfg.DownloadURL)
				if err != nil {
					mu.Lock()
					dropped++
					mu.Unlock()
					return
				}
				n, _ := io.Copy(io.Discard, io.LimitReader(resp.Body, 8<<20))
				resp.Body.Close()
				bytesRead += n
				if n == 0 {
					break
				}
			}
			mu.Lock()
			perConn = append(perConn, float64(bytesRead)/totalDur)
			mu.Unlock()
		}()
	}
	wg.Wait()

	var sum float64
	for _, v := range perConn {
		sum += v
	}
	mean := 0.0
	if len(perConn) > 0 {
		mean = sum / float64(len(perConn))
	}
	var variance float64
	for _, v := range perConn {
		variance += (v - mean) * (v - mean)
	}
	if len(perConn) > 0 {
		variance /= float64(len(perConn))
	}

	return loadResult{
		AggregateBPS: sum,
		StdDevBPS:    math.Sqrt(variance),
		Conns:        conns,
		Dropped:      dropped,
	}, nil
}

func (r loadResult) extra() string {
	return fmt.Sprintf("agg=%s conns=%d dropped=%d stddev=%s",
		FormatThroughput(r.AggregateBPS), r.Conns, r.Dropped, FormatThroughput(r.StdDevBPS))
}
