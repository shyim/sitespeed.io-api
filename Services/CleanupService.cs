namespace sitespeed_service.Services;

public class CleanupService : BackgroundService
{
    private readonly ILogger<CleanupService> _logger;

    public CleanupService(ILogger<CleanupService> logger)
    {
        _logger = logger;
    }

    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        _logger.LogInformation("Chromium temp file cleanup scheduled every 5 minutes");
        // Run initial cleanup
        await CleanupChromiumTempFiles(5);

        using var timer = new PeriodicTimer(TimeSpan.FromMinutes(5));
        while (await timer.WaitForNextTickAsync(stoppingToken))
        {
            await CleanupChromiumTempFiles(5);
        }
    }

    private async Task CleanupChromiumTempFiles(int maxAgeMinutes)
    {
        var tmpDir = Path.GetTempPath();
        var maxAge = TimeSpan.FromMinutes(maxAgeMinutes);
        var now = DateTime.UtcNow;

        try
        {
            if (Directory.Exists(tmpDir)) 
            {
                var dirs = Directory.GetDirectories(tmpDir);
                foreach (var dir in dirs)
                {
                    var dirName = Path.GetFileName(dir);
                    if (dirName.StartsWith(".org.chromium.Chromium."))
                    {
                        try
                        {
                            var dirInfo = new DirectoryInfo(dir);
                            if (now - dirInfo.LastWriteTimeUtc > maxAge)
                            {
                                dirInfo.Delete(true);
                                _logger.LogInformation("Cleaned up Chromium temp directory ({Age}min old): {Dir}", Math.Round((now - dirInfo.LastWriteTimeUtc).TotalMinutes), dir);
                            }
                        }
                        catch (Exception ex)
                        {
                            _logger.LogError(ex, "Failed to clean up {Dir}", dir);
                        }
                    }
                }
            }
        }
        catch (Exception ex)
        {
             _logger.LogError(ex, "Failed to clean up Chromium temp files");
        }
    }
}
