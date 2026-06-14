package health

import (
	"fmt"
	"io"
	"math"
	"net/url"
	"time"
)

// entropyResult holds measured traffic-shape statistics of the tunnel.
type entropyResult struct {
	Shannon     float64 // 0..1, entropy of the chunk-SIZE distribution (framing regularity)
	byteEntropy float64 // 0..1, Shannon entropy of payload bytes (content-dependent)
	SampleLen   int
	ReadCount   int     // number of distinct Read() chunks (proxy for packets)
	MeanChunk   float64 // mean bytes per Read
	MeanGapMs   float64 // mean inter-read gap (timing)
}

// sizeDistEntropy returns the normalised Shannon entropy of the distribution of
// chunk sizes (bucketed). Low values mean the tunnel emits regular fixed-size
// frames (a detectable fingerprint); high values mean varied, natural sizes.
func sizeDistEntropy(sizes []int) float64 {
	if len(sizes) < 2 {
		return 0
	}
	// Bucket sizes into 64 logarithmic-ish bins by 512-byte granularity.
	buckets := map[int]int{}
	for _, s := range sizes {
		buckets[s/512]++
	}
	n := float64(len(sizes))
	var h float64
	for _, cnt := range buckets {
		p := float64(cnt) / n
		h -= p * math.Log2(p)
	}
	// Normalise by max possible entropy for the number of distinct buckets.
	maxBits := math.Log2(float64(len(buckets)))
	if maxBits <= 0 {
		return 0
	}
	return h / maxBits
}

// sampleReader wraps an io.Reader and records, for each Read, the chunk size,
// inter-read timing, and a bounded sample of the bytes for entropy analysis.
type sampleReader struct {
	r        io.Reader
	max      int
	sample   []byte
	sizes    []int
	gaps     []time.Duration
	lastRead time.Time
}

func (s *sampleReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	now := time.Now()
	if !s.lastRead.IsZero() {
		s.gaps = append(s.gaps, now.Sub(s.lastRead))
	}
	s.lastRead = now
	if n > 0 {
		s.sizes = append(s.sizes, n)
		if len(s.sample) < s.max {
			take := n
			if room := s.max - len(s.sample); take > room {
				take = room
			}
			s.sample = append(s.sample, p[:take]...)
		}
	}
	return n, err
}

// testEntropy downloads through the proxy while sampling the ciphertext and
// returns the measured Shannon entropy plus packet-shape statistics. A
// well-obfuscated proxy stream is high-entropy (near 1.0) and lacks obvious
// fixed-size framing; a leaking protocol shows lower entropy or regular sizes.
func testEntropy(d dialer, cfg Config) (entropyResult, error) {
	u, err := url.Parse(cfg.DownloadURL)
	if err != nil {
		return entropyResult{}, err
	}
	resp, err := httpClientFor(d, u, cfg.Timeout).Get(cfg.DownloadURL)
	if err != nil {
		return entropyResult{}, err
	}
	defer resp.Body.Close()

	sr := &sampleReader{r: resp.Body, max: 256 << 10} // sample up to 256 KiB
	if _, err := io.Copy(io.Discard, sr); err != nil && err != io.EOF {
		return entropyResult{}, err
	}

	// Two signals:
	//  - byteEntropy: Shannon entropy of the payload bytes. High for
	//    ciphertext/compressed data, low for structured/zero content. Note
	//    that for a locally-proxied speed-test endpoint the HTTP body may be
	//    null bytes; byteEntropy is only meaningful for real content targets.
	//  - shapeEntropy: entropy of the chunk-SIZE distribution. Low shape
	//    entropy = regular fixed-size framing (a fingerprintable leak, e.g.
	//    constant record sizes); high = varied sizes like real traffic. This
	//    is observable regardless of payload and is the primary signal.
	byteEntropy := shannonNorm(sr.sample)
	shapeEntropy := sizeDistEntropy(sr.sizes)
	res := entropyResult{
		Shannon:   shapeEntropy, // primary reported metric
		SampleLen: len(sr.sample),
		ReadCount: len(sr.sizes),
	}
	res.byteEntropy = byteEntropy
	if len(sr.sizes) > 0 {
		var tot int
		for _, s := range sr.sizes {
			tot += s
		}
		res.MeanChunk = float64(tot) / float64(len(sr.sizes))
	}
	if len(sr.gaps) > 0 {
		var tot time.Duration
		for _, g := range sr.gaps {
			tot += g
		}
		res.MeanGapMs = float64((tot / time.Duration(len(sr.gaps))).Microseconds()) / 1000.0
	}
	return res, nil
}

// shannonNorm returns the Shannon entropy of b normalised to 0..1 (bits/byte / 8).
func shannonNorm(b []byte) float64 {
	if len(b) == 0 {
		return 0
	}
	var freq [256]int
	for _, c := range b {
		freq[c]++
	}
	n := float64(len(b))
	var h float64
	for _, f := range freq {
		if f == 0 {
			continue
		}
		p := float64(f) / n
		h -= p * math.Log2(p)
	}
	return h / 8.0 // max entropy for a byte is 8 bits
}

func (r entropyResult) extra() string {
	return fmt.Sprintf("shapeH=%.3f byteH=%.3f reads=%d meanChunk=%.0fB gap=%.2fms",
		r.Shannon, r.byteEntropy, r.ReadCount, r.MeanChunk, r.MeanGapMs)
}
