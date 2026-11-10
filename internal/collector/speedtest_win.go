//go:build windows

package collector

import (
	"bytes"
	"context"
	"io"
	"math"
	"net"
	"net/http"
	"time"
)

// SpeedTestResult holds the results of a bandwidth & bufferbloat test.
type SpeedTestResult struct {
	Timestamp       int64   `json:"timestamp"`
	UnloadedPingMs  float64 `json:"unloadedPingMs"`
	JitterMs        float64 `json:"jitterMs"`
	LoadedPingMs    float64 `json:"loadedPingMs"`
	DownloadMbps    float64 `json:"downloadMbps"`
	UploadMbps      float64 `json:"uploadMbps"`
	BufferbloatGrade string `json:"bufferbloatGrade"`
	ServerLabel     string  `json:"serverLabel"`
	Error           string  `json:"error,omitempty"`
}

// RunSpeedTest performs an unloaded latency/jitter check, an HTTP download throughput test
// with concurrent loaded-latency measurement, an HTTP upload throughput test, and calculates a Bufferbloat grade.
func RunSpeedTest(ctx context.Context) SpeedTestResult {
	res := SpeedTestResult{
		Timestamp:   time.Now().Unix(),
		ServerLabel: "Cloudflare Global Anycast (speed.cloudflare.com)",
	}

	// 1. Measure unloaded TCP handshake latency & jitter (5 samples to 1.1.1.1:443)
	var samples []float64
	for i := 0; i < 5; i++ {
		if ctx.Err() != nil {
			res.Error = "Test cancelled"
			return res
		}
		rtt := measureTCPHandshakeMs("1.1.1.1:443", 1500*time.Millisecond)
		if rtt > 0 {
			samples = append(samples, rtt)
		}
		time.Sleep(40 * time.Millisecond)
	}
	if len(samples) == 0 {
		// Fallback to 8.8.8.8:443
		for i := 0; i < 4; i++ {
			rtt := measureTCPHandshakeMs("8.8.8.8:443", 1500*time.Millisecond)
			if rtt > 0 {
				samples = append(samples, rtt)
			}
		}
	}
	if len(samples) > 0 {
		var sum float64
		for _, v := range samples {
			sum += v
		}
		res.UnloadedPingMs = math.Round((sum/float64(len(samples)))*10) / 10
		if len(samples) > 1 {
			var diffSum float64
			for i := 1; i < len(samples); i++ {
				diffSum += math.Abs(samples[i] - samples[i-1])
			}
			res.JitterMs = math.Round((diffSum/float64(len(samples)-1))*10) / 10
		}
	}

	client := &http.Client{Timeout: 12 * time.Second}

	// 2. Measure Download Throughput + Loaded Ping concurrently
	loadedStop := make(chan struct{})
	loadedPings := make(chan []float64, 1)
	go func() {
		var lp []float64
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-loadedStop:
				loadedPings <- lp
				return
			case <-ticker.C:
				rtt := measureTCPHandshakeMs("1.1.1.1:443", 1200*time.Millisecond)
				if rtt > 0 {
					lp = append(lp, rtt)
				}
			}
		}
	}()

	dlReq, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://speed.cloudflare.com/__down?bytes=10000000", nil)
	if err == nil {
		dlStart := time.Now()
		resp, err := client.Do(dlReq)
		if err == nil {
			n, _ := io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			dlElapsed := time.Since(dlStart).Seconds()
			if dlElapsed > 0.05 && n > 0 {
				mbps := (float64(n) * 8.0) / (dlElapsed * 1_000_000.0)
				res.DownloadMbps = math.Round(mbps*100) / 100
			}
		}
	}
	close(loadedStop)
	lpSamples := <-loadedPings
	if len(lpSamples) > 0 {
		var sum float64
		for _, v := range lpSamples {
			sum += v
		}
		res.LoadedPingMs = math.Round((sum/float64(len(lpSamples)))*10) / 10
	} else {
		res.LoadedPingMs = res.UnloadedPingMs
	}

	// 3. Measure Upload Throughput (2 MB payload to speed.cloudflare.com/__up)
	payload := make([]byte, 2_000_000)
	upReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://speed.cloudflare.com/__up", bytes.NewReader(payload))
	if err == nil {
		upReq.Header.Set("Content-Type", "application/octet-stream")
		upStart := time.Now()
		resp, err := client.Do(upReq)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			upElapsed := time.Since(upStart).Seconds()
			if upElapsed > 0.05 {
				mbps := (float64(len(payload)) * 8.0) / (upElapsed * 1_000_000.0)
				res.UploadMbps = math.Round(mbps*100) / 100
			}
		}
	}

	// 4. Compute Bufferbloat Grade based on Loaded vs Unloaded latency delta
	deltaMs := res.LoadedPingMs - res.UnloadedPingMs
	if deltaMs < 0 {
		deltaMs = 0
	}
	switch {
	case res.DownloadMbps == 0 && res.UnloadedPingMs == 0:
		res.BufferbloatGrade = "—"
		if res.Error == "" {
			res.Error = "Could not reach speed test endpoint"
		}
	case deltaMs <= 5:
		res.BufferbloatGrade = "A+"
	case deltaMs <= 30:
		res.BufferbloatGrade = "A"
	case deltaMs <= 60:
		res.BufferbloatGrade = "B"
	case deltaMs <= 150:
		res.BufferbloatGrade = "C"
	default:
		res.BufferbloatGrade = "D"
	}

	return res
}

func measureTCPHandshakeMs(addr string, timeout time.Duration) float64 {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return 0
	}
	elapsed := float64(time.Since(start).Microseconds()) / 1000.0
	_ = conn.Close()
	return elapsed
}
