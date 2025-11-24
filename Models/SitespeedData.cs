using System.Text.Json.Serialization;

namespace sitespeed_service.Models;

public record BrowserTime
{
    [JsonPropertyName("timings")]
    public Timings? Timings { get; init; }

    [JsonPropertyName("googleWebVitals")]
    public GoogleWebVitals? GoogleWebVitals { get; init; }
}

public record Timings
{
    [JsonPropertyName("fullyLoaded")]
    public Metric? FullyLoaded { get; init; }
}

public record GoogleWebVitals
{
    [JsonPropertyName("ttfb")]
    public Metric? Ttfb { get; init; }
    [JsonPropertyName("largestContentfulPaint")]
    public Metric? LargestContentfulPaint { get; init; }
    [JsonPropertyName("firstContentfulPaint")]
    public Metric? FirstContentfulPaint { get; init; }
    [JsonPropertyName("cumulativeLayoutShift")]
    public Metric? CumulativeLayoutShift { get; init; }
    [JsonPropertyName("totalBlockingTime")]
    public Metric? TotalBlockingTime { get; init; }
}

public record PageXray
{
    [JsonPropertyName("transferSize")]
    public Metric? TransferSize { get; init; }
}

public record Metric
{
    [JsonPropertyName("median")]
    public double Median { get; init; }
}
