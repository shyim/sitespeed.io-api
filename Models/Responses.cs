namespace sitespeed_service.Models;

public record ErrorResponse(string Error, string? Details = null);

public record AnalyzeResponse
{
    public double Ttfb { get; init; }
    public double FullyLoaded { get; init; }
    public double LargestContentfulPaint { get; init; }
    public double FirstContentfulPaint { get; init; }
    public double CumulativeLayoutShift { get; init; }
    public double TransferSize { get; init; }
}
