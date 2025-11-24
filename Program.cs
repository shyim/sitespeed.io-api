using System.Diagnostics;
using System.Text.Json;
using sitespeed_service.Infrastructure;
using sitespeed_service.Models;
using sitespeed_service.Services;
using System.IO.Compression;
using Microsoft.AspNetCore.OpenApi;
using Microsoft.AspNetCore.StaticFiles;
using Microsoft.Net.Http.Headers;

using Microsoft.OpenApi;

var builder = WebApplication.CreateSlimBuilder(args);

builder.Services.ConfigureHttpJsonOptions(options =>
{
    options.SerializerOptions.TypeInfoResolverChain.Insert(0, AppJsonSerializerContext.Default);
});

builder.Services.AddEndpointsApiExplorer();
builder.Services.AddSwaggerGen(c =>
{
    c.AddSecurityDefinition("Bearer", new OpenApiSecurityScheme
    {
        Description = "JWT Authorization header using the Bearer scheme. Example: \"Authorization: Bearer {token}\"",
        Name = "Authorization",
        In = ParameterLocation.Header,
        Type = SecuritySchemeType.ApiKey,
        Scheme = "Bearer"
    });

    c.AddSecurityRequirement(doc => new OpenApiSecurityRequirement
    {
        {
            new OpenApiSecuritySchemeReference("Bearer", doc, null),
            new List<string>()
        }
    });
});
builder.Services.AddOpenApi();
builder.Services.Configure<RouteOptions>(options => options.SetParameterPolicy<Microsoft.AspNetCore.Routing.Constraints.RegexInlineRouteConstraint>("regex"));
builder.Services.AddHostedService<CleanupService>();
builder.Services.AddSingleton<StorageService>();

var app = builder.Build();

app.UseSwagger();
app.UseSwaggerUI();

app.Use(async (context, next) =>
{
    if (context.Request.Path.StartsWithSegments("/api"))
    {
        var authToken = Environment.GetEnvironmentVariable("AUTH_TOKEN");
        if (!string.IsNullOrEmpty(authToken))
        {
            if (!context.Request.Headers.TryGetValue("Authorization", out var extractedAuthToken))
            {
                context.Response.StatusCode = 401;
                await context.Response.WriteAsync("Unauthorized");
                return;
            }

            if (!extractedAuthToken.ToString().StartsWith("Bearer ") || 
                extractedAuthToken.ToString().Substring(7) != authToken)
            {
                context.Response.StatusCode = 401;
                await context.Response.WriteAsync("Unauthorized");
                return;
            }
        }
    }
    await next();
});



app.MapPost("/api/result/{id}", async (string id, ApiAnalyzeRequest request, HttpContext context, StorageService storage, ILogger<Program> logger) =>
{
    if (string.IsNullOrEmpty(id) || id.Contains("..") || id.Contains('/') || id.Contains('\\'))
    {
        return Results.BadRequest("Invalid ID");
    }

    if (request.Urls == null || request.Urls.Length == 0 || request.Urls.Length > 5)
    {
        return Results.BadRequest(new ErrorResponse("URLs must be between 1 and 5 items"));
    }
    foreach (var url in request.Urls)
    {
        if (!Uri.TryCreate(url, UriKind.Absolute, out _))
        {
            return Results.BadRequest(new ErrorResponse($"Invalid URL: {url}"));
        }
    }

    var tempPath = Path.GetTempPath();
    var resultDir = Path.Combine(tempPath, "sitespeed", id);

    if (Directory.Exists(resultDir))
    {
        Directory.Delete(resultDir, true);
    }
    Directory.CreateDirectory(resultDir);

    logger.LogInformation("Starting sitespeed analysis for {Id} with URLs: {Urls}", id, string.Join(", ", request.Urls));

    var sitespeedBin = Environment.GetEnvironmentVariable("SITESPEED_BIN") ?? "sitespeed.io";

    var psi = new ProcessStartInfo
    {
        FileName = "node",
        RedirectStandardOutput = true,
        RedirectStandardError = true,
        UseShellExecute = false,
        CreateNoWindow = true
    };

    psi.ArgumentList.Add(sitespeedBin);
    psi.ArgumentList.Add("--outputFolder");
    psi.ArgumentList.Add(resultDir);
    psi.ArgumentList.Add("--plugins.add");
    psi.ArgumentList.Add("analysisstorer");
    psi.ArgumentList.Add("--visualMetrics");
    psi.ArgumentList.Add("--video");
    psi.ArgumentList.Add("--viewPort");
    psi.ArgumentList.Add("1920x1080");
    psi.ArgumentList.Add("--browsertime.chrome.cleanUserDataDir=true");
    psi.ArgumentList.Add("--browsertime.iterations");
    psi.ArgumentList.Add("1");

    foreach (var url in request.Urls)
    {
        psi.ArgumentList.Add(url);
    }

    logger.LogInformation("Running sitespeed analysis for shop {Id}", id);

    try
    {
        using var process = Process.Start(psi);
        if (process == null) throw new Exception("Failed to start process");

        await process.WaitForExitAsync();

        if (process.ExitCode != 0)
        {
            var error = await process.StandardError.ReadToEndAsync();
            logger.LogError("Sitespeed failed: {Error}", error);
            return Results.Json(new ErrorResponse("Failed to run sitespeed analysis", error), statusCode: 500, jsonTypeInfo: AppJsonSerializerContext.Default.ErrorResponse);
        }

        logger.LogInformation("Sitespeed analysis completed for shop {Id}", id);

        var pagesDir = Path.Combine(resultDir, "pages");
        if (!Directory.Exists(pagesDir))
        {
            return Results.Json(new ErrorResponse("Web vital data not found"), statusCode: 500, jsonTypeInfo: AppJsonSerializerContext.Default.ErrorResponse);
        }

        var pages = Directory.GetDirectories(pagesDir).Select(Path.GetFileName).ToArray();
        if (pages.Length == 0)
        {
            return Results.Json(new ErrorResponse("Web vital data not found"), statusCode: 500, jsonTypeInfo: AppJsonSerializerContext.Default.ErrorResponse);
        }

        var webvitalDataPath = Path.Combine(resultDir, "data", "browsertime.summary-total.json");
        var pagexrayDataPath = Path.Combine(resultDir, "data", "pagexray.summary-total.json");

        if (!File.Exists(webvitalDataPath))
        {
            return Results.Json(new ErrorResponse("Web vital data not found"), statusCode: 500, jsonTypeInfo: AppJsonSerializerContext.Default.ErrorResponse);
        }

        BrowserTime? browsertimeData = null;
        using (var stream = File.OpenRead(webvitalDataPath))
        {
            browsertimeData = await JsonSerializer.DeserializeAsync(stream, AppJsonSerializerContext.Default.BrowserTime);
        }

        PageXray? pagexrayData = new PageXray();
        if (File.Exists(pagexrayDataPath))
        {
            using (var stream = File.OpenRead(pagexrayDataPath))
            {
                pagexrayData = await JsonSerializer.DeserializeAsync(stream, AppJsonSerializerContext.Default.PageXray);
            }
        }

        var screenshotPath = Path.Combine(resultDir, "pages", pages[0]!, "data", "screenshots", "1", "afterPageCompleteCheck.png");
        var s3ScreenshotPath = $"results/{id}/screenshot.png";

        if (File.Exists(screenshotPath))
        {
            await storage.UploadFileAsync(s3ScreenshotPath, screenshotPath);
        }

        var zipPath = Path.Combine(tempPath, $"{id}.zip");
        if (File.Exists(zipPath)) File.Delete(zipPath);

        ZipFile.CreateFromDirectory(resultDir, zipPath);
        await storage.UploadFileAsync($"results/{id}/result.zip", zipPath);

        // Cleanup
        File.Delete(zipPath);
        Directory.Delete(resultDir, true);

        var cacheZipPath = Path.Combine(tempPath, "sitespeed-cache", $"{id}.zip");
        if (File.Exists(cacheZipPath))
        {
            File.Delete(cacheZipPath);
        }

        var response = new AnalyzeResponse
        {
            Ttfb = browsertimeData?.GoogleWebVitals?.Ttfb?.Median ?? 0,
            FullyLoaded = browsertimeData?.Timings?.FullyLoaded?.Median ?? 0,
            LargestContentfulPaint = browsertimeData?.GoogleWebVitals?.LargestContentfulPaint?.Median ?? 0,
            FirstContentfulPaint = browsertimeData?.GoogleWebVitals?.FirstContentfulPaint?.Median ?? 0,
            CumulativeLayoutShift = browsertimeData?.GoogleWebVitals?.CumulativeLayoutShift?.Median ?? 0,
            TransferSize = pagexrayData?.TransferSize?.Median ?? 0,
        };

        return Results.Ok(response);
    }
    catch (Exception ex)
    {
        logger.LogError(ex, "Error running sitespeed");
        return Results.Json(new ErrorResponse("Failed to run sitespeed analysis", ex.Message), statusCode: 500, jsonTypeInfo: AppJsonSerializerContext.Default.ErrorResponse);
    }
})
.WithName("Analyze")
.Produces<AnalyzeResponse>(StatusCodes.Status200OK)
.Produces<ErrorResponse>(StatusCodes.Status400BadRequest)
.Produces<ErrorResponse>(StatusCodes.Status500InternalServerError);

app.MapGet("/result/{id}/{*path}", async (string id, string? path, StorageService storage, HttpContext context) =>
{
    if (string.IsNullOrEmpty(id) || id.Contains("..") || id.Contains('/') || id.Contains('\\'))
    {
        return Results.BadRequest("Invalid ID");
    }

    var tempPath = Path.GetTempPath();
    var zipPath = Path.Combine(tempPath, "sitespeed-cache", $"{id}.zip");

    if (!File.Exists(zipPath))
    {
        Directory.CreateDirectory(Path.GetDirectoryName(zipPath)!);
        try
        {
            await storage.DownloadFileAsync($"results/{id}/result.zip", zipPath);
        }
        catch (Amazon.S3.AmazonS3Exception ex) when (ex.StatusCode == System.Net.HttpStatusCode.NotFound)
        {
            return Results.NotFound();
        }
    }

    var targetPath = string.IsNullOrEmpty(path) ? "index.html" : path;
    targetPath = targetPath.Replace('\\', '/');

    try 
    {
        var archive = ZipFile.OpenRead(zipPath);
        var entry = archive.GetEntry(targetPath);

        if (entry == null && !targetPath.EndsWith("/"))
        {
            entry = archive.GetEntry(targetPath + "/index.html");
        }

        if (entry == null)
        {
            archive.Dispose();
            return Results.NotFound();
        }

        var provider = new FileExtensionContentTypeProvider();
        if (!provider.TryGetContentType(entry.Name, out var contentType))
        {
            contentType = "application/octet-stream";
        }

        context.Response.Headers.CacheControl = "public, max-age=604800";
        return Results.Stream(new DisposableStream(entry.Open(), archive), contentType, lastModified: entry.LastWriteTime);
    }
    catch (Exception)
    {
        return Results.NotFound();
    }
});

app.MapDelete("/api/result/{id}", async (string id, StorageService storage) =>
{
    if (string.IsNullOrEmpty(id) || id.Contains("..") || id.Contains('/') || id.Contains('\\'))
    {
        return Results.BadRequest("Invalid ID");
    }

    await storage.DeleteFileAsync($"results/{id}/result.zip");
    await storage.DeleteFileAsync($"results/{id}/screenshot.png");

    var tempPath = Path.GetTempPath();
    var zipPath = Path.Combine(tempPath, "sitespeed-cache", $"{id}.zip");

    if (File.Exists(zipPath))
    {
        File.Delete(zipPath);
    }

    return Results.Ok();
});

app.MapGet("/screenshot/{id}", async (string id, StorageService storage, HttpContext context) =>
{
    if (string.IsNullOrEmpty(id) || id.Contains("..") || id.Contains('/') || id.Contains('\\'))
    {
        return Results.BadRequest("Invalid ID");
    }

    try
    {
        var (stream, contentType, lastModified, etag) = await storage.GetFileAsync($"results/{id}/screenshot.png");
        
        context.Response.Headers.CacheControl = "public, max-age=604800";
        
        EntityTagHeaderValue? entityTag = null;
        if (!string.IsNullOrEmpty(etag))
        {
             if (!etag.StartsWith("\"")) etag = $"\"{etag}\"";
             EntityTagHeaderValue.TryParse(etag, out entityTag);
        }

        return Results.Stream(stream, contentType, lastModified: lastModified, entityTag: entityTag);
    }
    catch (Amazon.S3.AmazonS3Exception ex) when (ex.StatusCode == System.Net.HttpStatusCode.NotFound)
    {
        return Results.NotFound();
    }
});

app.Run();
