package models

type AnalyzeRequest struct {
	ID   string   `json:"id"`
	URLs []string `json:"urls"`
}

type ApiAnalyzeRequest struct {
	URLs []string `json:"urls"`
}

type ErrorResponse struct {
	Error   string  `json:"error"`
	Details *string `json:"details,omitempty"`
}

type AnalyzeResponse struct {
	Ttfb                  float64 `json:"ttfb"`
	FullyLoaded           float64 `json:"fullyLoaded"`
	LargestContentfulPaint float64 `json:"largestContentfulPaint"`
	FirstContentfulPaint  float64 `json:"firstContentfulPaint"`
	CumulativeLayoutShift float64 `json:"cumulativeLayoutShift"`
	TransferSize          float64 `json:"transferSize"`
}

// Internal Sitespeed Data Models

type BrowserTime struct {
	Timings         *Timings         `json:"timings"`
	GoogleWebVitals *GoogleWebVitals `json:"googleWebVitals"`
}

type Timings struct {
	FullyLoaded *Metric `json:"fullyLoaded"`
}

type GoogleWebVitals struct {
	Ttfb                  *Metric `json:"ttfb"`
	LargestContentfulPaint *Metric `json:"largestContentfulPaint"`
	FirstContentfulPaint  *Metric `json:"firstContentfulPaint"`
	CumulativeLayoutShift *Metric `json:"cumulativeLayoutShift"`
	TotalBlockingTime     *Metric `json:"totalBlockingTime"`
}

type PageXray struct {
	TransferSize *Metric `json:"transferSize"`
}

type Metric struct {
	Median float64 `json:"median"`
}
